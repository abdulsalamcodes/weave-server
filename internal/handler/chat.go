package handler

import (
	"encoding/json"
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
}

// --- Wire types ---

type chatMessageRequest struct {
	Message string `json:"message"`
}

type intentResponse struct {
	Response string      `json:"response"`
	Intent   llm.Intent  `json:"intent"`
	Data     interface{} `json:"data,omitempty"`
}

// pendingActionKind discriminates what confirmation is pending.
type pendingActionKind string

const (
	kindTransfer pendingActionKind = "transfer"
	kindUnlink   pendingActionKind = "unlink"
)

// pendingAction is the envelope stored in Redis while awaiting confirmation.
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

const maxHistoryMessages = 10

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
	if strings.TrimSpace(req.Message) == "" {
		respondError(w, http.StatusBadRequest, "empty_message")
		return
	}

	if h.llm == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "AI assistant is not configured. Please set up the LLM API key.",
			Intent:   llm.IntentUnknown,
		})
		return
	}

	history := h.loadHistory(r.Context(), userID)
	history = append(history, llm.Message{Role: "user", Content: req.Message})

	// Try keyword shortcut first — avoids an LLM call for unambiguous inputs.
	parsed := keywordIntent(req.Message)
	if parsed == nil {
		systemCtx := h.buildSystemContext(r.Context(), userID)
		var err error
		parsed, err = h.llm.ParseIntentWithContext(r.Context(), history, systemCtx)
		if err != nil {
			h.logger.Error("llm parse failed", "error", err, "message", req.Message)
			respondJSON(w, http.StatusOK, intentResponse{
				Response: "Sorry, I couldn't understand that. Try something like: \"send 2000 to 0123456789 at GTBank\"",
				Intent:   llm.IntentUnknown,
			})
			return
		}
		h.logger.Info("intent parsed", "user_id", userID, "intent", parsed.Intent)
	}

	// Capture the response body so we can save the assistant reply to history.
	crw := &capturingResponseWriter{ResponseWriter: w}
	defer func() {
		if len(crw.body) > 0 {
			var resp intentResponse
			if json.Unmarshal(crw.body, &resp) == nil && resp.Response != "" {
				history = append(history, llm.Message{Role: "assistant", Content: resp.Response})
				h.saveHistory(r.Context(), userID, history)
			}
		}
	}()

	h.dispatch(crw, r, userID, parsed)
}

func (h *ChatHandler) dispatch(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	switch parsed.Intent {
	case llm.IntentSendMoney:
		h.handleSendMoney(w, r, userID, parsed)
	case llm.IntentConfirmTx:
		h.handleConfirm(w, r, userID)
	case llm.IntentCancelTx:
		h.handleCancel(w, r, userID)
	case llm.IntentCheckBal:
		h.handleCheckBalance(w, r, userID)
	case llm.IntentTxHistory:
		h.handleTransferHistory(w, r, userID)
	case llm.IntentTxStatus:
		h.handleTransferStatus(w, r, userID, parsed)
	case llm.IntentLinkBank:
		h.handleLinkBank(w)
	case llm.IntentListBanks:
		h.handleListBanks(w, r, userID)
	case llm.IntentUnlinkBank:
		h.handleUnlinkBank(w, r, userID, parsed)
	case llm.IntentSetPriority:
		h.handleSetPriority(w, r, userID, parsed)
	case llm.IntentRefreshBalance:
		h.handleRefreshBalance(w, r, userID, parsed)
	case llm.IntentFundWallet:
		h.handleFundWallet(w, r, userID)
	case llm.IntentWalletHistory:
		h.handleWalletHistory(w, r, userID)
	case llm.IntentLookupAccount:
		h.handleLookupAccount(w, r, parsed)
	case llm.IntentHelp:
		h.handleHelp(w)
	default:
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "I didn't quite catch that. Here's what I can help with:\n\n" +
				"💸 Send money — \"send 5000 to 0123456789\"\n" +
				"💰 Check balance — \"what's my balance?\"\n" +
				"🏦 Linked banks — \"show my bank accounts\"\n" +
				"➕ Fund wallet — \"how do I fund my wallet?\"\n" +
				"🔗 Link a bank — \"link my GTBank account\"\n" +
				"🔍 Lookup account — \"who is 0123456789 at GTBank?\"\n" +
				"📋 Transfer history — \"show my recent transfers\"\n" +
				"❓ Help — \"help\"",
			Intent: parsed.Intent,
		})
	}
}

// ConfirmTransfer is a legacy REST endpoint that mirrors the chat confirm flow.
func (h *ChatHandler) ConfirmTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.handleConfirm(w, r, userID)
}

// txnSummary is the compact representation returned in history responses.
type txnSummary struct {
	Ref       string  `json:"ref"`
	Amount    float64 `json:"amount"`
	Recipient string  `json:"recipient"`
	Status    string  `json:"status"`
	Date      string  `json:"date"`
}

// toSummary converts a transaction to the compact wire type.
func toSummary(t model.Transaction) txnSummary {
	return txnSummary{
		Ref:       t.OurRef,
		Amount:    t.Amount.NGN(),
		Recipient: t.RecipientAccount,
		Status:    string(t.Status),
		Date:      t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
