package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/llm"
	"github.com/abdulsalamcodes/weave-server/internal/provider/mono"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

type ChatHandler struct {
	transferService *service.TransferService
	walletService   *service.WalletService
	bankRepo        repository.BankAccountRepository
	txnRepo         repository.TransactionRepository
	chatRepo        repository.ChatMessageRepository
	paystack        *paystack.Client
	mono            *mono.Client
	rdb             *redis.Client
	llm             *llm.Client
	logger          *slog.Logger
}

func NewChatHandler(
	transferService *service.TransferService,
	walletService *service.WalletService,
	bankRepo repository.BankAccountRepository,
	txnRepo repository.TransactionRepository,
	chatRepo repository.ChatMessageRepository,
	paystackClient *paystack.Client,
	monoClient *mono.Client,
	rdb *redis.Client,
	llmClient *llm.Client,
	logger *slog.Logger,
) *ChatHandler {
	return &ChatHandler{
		transferService: transferService,
		walletService:   walletService,
		bankRepo:        bankRepo,
		txnRepo:         txnRepo,
		chatRepo:        chatRepo,
		paystack:        paystackClient,
		mono:            monoClient,
		rdb:             rdb,
		llm:             llmClient,
		logger:          logger,
	}
}

func (h *ChatHandler) RegisterRoutes(r chi.Router) {
	r.Post("/chat/message", h.HandleMessage)
	r.Post("/chat/confirm", h.ConfirmTransfer)
	r.Get("/chat/history", h.GetHistory)
	r.Delete("/chat/history", h.ClearHistory)
}

// --- Wire types ---

type chatMessageRequest struct {
	Message string `json:"message"`
}

type chatResponse struct {
	Response string `json:"response"`
}

// pendingActionKind discriminates what confirmation is pending.
type pendingActionKind string

const (
	kindTransfer pendingActionKind = "transfer"
	kindUnlink   pendingActionKind = "unlink"
)

type pendingAction struct {
	Kind     pendingActionKind `json:"kind"`
	Transfer *pendingTransfer  `json:"transfer,omitempty"`
	Unlink   *pendingUnlink    `json:"unlink,omitempty"`
}

type pendingTransfer struct {
	Amount           int64  `json:"amount"` // kobo
	RecipientAccount string `json:"recipient_account"`
	RecipientBank    string `json:"recipient_bank"`
	RecipientName    string `json:"recipient_name"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type pendingUnlink struct {
	BankID        string `json:"bank_id"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
}

func pendingKey(userID uuid.UUID) string { return "chat:pending:" + userID.String() }
func historyKey(userID uuid.UUID) string { return "chat:history:" + userID.String() }

const maxHistoryMessages = 20

// --- Entry point ---

func (h *ChatHandler) HandleMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req chatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		respondError(w, http.StatusBadRequest, "empty_message")
		return
	}

	if h.llm == nil {
		respondJSON(w, http.StatusOK, chatResponse{Response: "AI assistant is not configured."})
		return
	}

	ctx := r.Context()
	history := h.loadHistory(ctx, userID)

	// Keyword shortcuts handle unambiguous single-word inputs instantly,
	// without paying the cost of an LLM round-trip.
	if reply, handled := h.tryKeywordShortcut(ctx, userID, msg); handled {
		h.persistPair(ctx, userID, msg, reply, false)
		h.saveHistory(ctx, userID, appendPair(history, msg, reply))
		respondJSON(w, http.StatusOK, chatResponse{Response: reply})
		return
	}

	// Everything else goes through the agent loop.
	systemPrompt := h.buildAgentPrompt(ctx, userID)

	reply, err := h.llm.RunAgent(
		ctx,
		systemPrompt,
		history,
		msg,
		llm.BankingTools(),
		func(toolCtx context.Context, name, args string) (interface{}, error) {
			h.logger.Info("agent tool call", "user_id", userID, "tool", name)
			return h.executeTool(toolCtx, name, args, userID)
		},
	)
	if err != nil {
		h.logger.Error("agent loop failed", "error", err, "user_id", userID)
		errMsg := "Sorry, something went wrong. Please try again."
		h.persistPair(ctx, userID, msg, errMsg, true)
		respondJSON(w, http.StatusOK, chatResponse{Response: errMsg})
		return
	}

	h.persistPair(ctx, userID, msg, reply, false)
	h.saveHistory(ctx, userID, appendPair(history, msg, reply))
	respondJSON(w, http.StatusOK, chatResponse{Response: reply})
}

// tryKeywordShortcut handles unambiguous single-word/phrase inputs directly.
// Returns (reply, true) when handled, or ("", false) to fall through to the agent.
func (h *ChatHandler) tryKeywordShortcut(ctx context.Context, userID uuid.UUID, msg string) (string, bool) {
	switch strings.ToLower(msg) {
	case "help", "?", "commands":
		return helpText(), true

	case "balance", "my balance", "check balance", "wallet", "wallet balance":
		return h.directBalance(ctx, userID), true

	case "banks", "my banks", "linked banks", "accounts", "my accounts":
		result, err := h.toolGetLinkedBanks(ctx, userID)
		if err != nil {
			return "Couldn't fetch your banks right now.", true
		}
		return formatLinkedBanks(result), true

	case "yes", "confirm", "ok", "okay", "proceed", "go ahead", "do it", "sure", "yep", "yh", "yeah", "alright":
		return h.shortcutConfirm(ctx, userID), true

	case "no", "cancel", "stop", "abort", "nevermind", "nope", "don't":
		_, _ = h.toolCancelAction(ctx, userID)
		return "Cancelled. Let me know if you'd like to do something else.", true
	}
	return "", false
}

// shortcutConfirm executes whatever pending action exists.
func (h *ChatHandler) shortcutConfirm(ctx context.Context, userID uuid.UUID) string {
	action, ok := h.loadPending(ctx, userID)
	if !ok {
		return "No pending action to confirm. Start a new transfer by saying \"send 5000 to 0123456789\"."
	}
	switch action.Kind {
	case kindTransfer:
		result, err := h.toolConfirmTransfer(ctx, userID)
		if err != nil {
			return err.Error()
		}
		return formatConfirmedTransfer(result)
	case kindUnlink:
		result, err := h.toolConfirmUnlink(ctx, userID)
		if err != nil {
			return err.Error()
		}
		data := normalizeToMap(result)
		return fmt.Sprintf("✅ %s (%s) has been unlinked.", data["bank_name"], data["account"])
	}
	return "Nothing to confirm."
}

// ConfirmTransfer is a legacy REST endpoint that mirrors the chat confirm flow.
func (h *ChatHandler) ConfirmTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	reply := h.shortcutConfirm(r.Context(), userID)
	respondJSON(w, http.StatusOK, chatResponse{Response: reply})
}

// --- Response formatters for keyword shortcuts ---

func formatLinkedBanks(result interface{}) string {
	data := normalizeToMap(result)
	banks, _ := data["banks"].([]interface{})
	if len(banks) == 0 {
		return "You don't have any linked bank accounts yet. Tap the Banks tab to link one."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You have %d linked bank account(s):\n", len(banks)))
	for i, b := range banks {
		bm, _ := b.(map[string]interface{})
		sb.WriteString(fmt.Sprintf("\n%d. %s · %s\n   Balance: ₦%.2f · Priority: %.0f",
			i+1, bm["name"], bm["account_number"], bm["balance_ngn"], bm["priority"]))
	}
	sb.WriteString("\n\nSay \"refresh [bank] balance\" or \"set [bank] as priority 1\" to manage them.")
	return sb.String()
}

func formatConfirmedTransfer(result interface{}) string {
	data := normalizeToMap(result)
	var sb strings.Builder
	sb.WriteString("✅ Done!\n\n")
	if legs, ok := data["debit_legs"].([]interface{}); ok {
		for _, l := range legs {
			lm, _ := l.(map[string]interface{})
			feeStr := ""
			if fee, _ := lm["fee_ngn"].(float64); fee > 0 {
				feeStr = fmt.Sprintf(" + ₦%.2f fee", fee)
			}
			sb.WriteString(fmt.Sprintf("  %s  -₦%.2f%s\n", lm["source"], lm["amount_ngn"], feeStr))
		}
	}
	recipient := fmt.Sprintf("%v", data["recipient"])
	if name, _ := data["recipient_name"].(string); name != "" {
		recipient = fmt.Sprintf("%s (%s)", name, recipient)
	}
	sb.WriteString(fmt.Sprintf("\nSent ₦%.2f to %s", data["amount_ngn"], recipient))
	if fee, _ := data["total_fees_ngn"].(float64); fee > 0 {
		sb.WriteString(fmt.Sprintf(" · fees ₦%.2f", fee))
	}
	sb.WriteString(fmt.Sprintf("\nRef: %s", data["ref"]))
	return sb.String()
}

func helpText() string {
	return "Here's everything I can help you with:\n\n" +
		"💸 Send money — \"send 5000 to 0123456789 at GTBank\"\n" +
		"✅ Confirm/cancel — \"yes\" or \"cancel\" after a preview\n" +
		"💰 Check balance — \"what's my balance?\"\n" +
		"📋 Transfer history — \"show my recent transfers\"\n" +
		"🔎 Transfer status — \"what's the status of WVF-abc123?\"\n" +
		"🔍 Lookup account — \"who is 0123456789 at Zenith?\"\n" +
		"➕ Fund wallet — \"how do I fund my wallet?\"\n" +
		"📊 Wallet deposits — \"show my wallet history\"\n" +
		"🏦 Linked banks — \"show my bank accounts\"\n" +
		"🔗 Link a bank — \"link my GTBank account\"\n" +
		"🗑️  Unlink a bank — \"unlink my Access Bank\"\n" +
		"⭐ Set priority — \"make Zenith my priority 1 account\"\n" +
		"🔄 Refresh balance — \"refresh my GTBank balance\"\n\n" +
		"Just talk naturally — I'll figure out what you need."
}

func appendPair(history []llm.Message, user, assistant string) []llm.Message {
	return append(history,
		llm.Message{Role: "user", Content: user},
		llm.Message{Role: "assistant", Content: assistant},
	)
}

// persistPair writes both sides of a turn to the database.
func (h *ChatHandler) persistPair(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string, isError bool) {
	userRow := &model.ChatMessage{UserID: userID, Role: "user", Content: userMsg}
	if err := h.chatRepo.Create(ctx, userRow); err != nil {
		h.logger.Error("failed to persist user message", "error", err)
	}
	assistantRow := &model.ChatMessage{UserID: userID, Role: "assistant", Content: assistantMsg, IsError: isError}
	if err := h.chatRepo.Create(ctx, assistantRow); err != nil {
		h.logger.Error("failed to persist assistant message", "error", err)
	}
}

// GetHistory returns the user's full chat history for display in the UI.
func (h *ChatHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if n, err := fmt.Sscanf(limitStr, "%d", &limit); n != 1 || err != nil || limit < 1 {
			limit = 100
		}
	}

	msgs, err := h.chatRepo.ListByUserID(r.Context(), userID, limit, 0)
	if err != nil {
		h.logger.Error("failed to load chat history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed_to_load_history")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"messages": msgs})
}

// ClearHistory deletes all messages for the user and flushes the Redis context.
func (h *ChatHandler) ClearHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.chatRepo.DeleteByUserID(r.Context(), userID); err != nil {
		h.logger.Error("failed to clear chat history", "error", err)
		respondError(w, http.StatusInternalServerError, "failed_to_clear_history")
		return
	}

	// Also flush the Redis LLM context so the agent starts fresh.
	if h.rdb != nil {
		h.rdb.Del(r.Context(), historyKey(userID))
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}
