package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/llm"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

// keywordIntent short-circuits the LLM for unambiguous single-word/phrase commands.
// Returns an intent string or "" to fall through to LLM parsing.
func keywordIntent(msg string) string {
	switch strings.ToLower(strings.TrimSpace(msg)) {
	case "help", "?", "commands", "what can you do", "what can i do here":
		return string(llm.IntentHelp)
	case "balance", "my balance", "check balance", "wallet", "wallet balance":
		return string(llm.IntentCheckBal)
	case "banks", "my banks", "linked banks", "accounts", "my accounts":
		return string(llm.IntentListBanks)
	case "history", "transfers", "recent transfers", "my transfers":
		return string(llm.IntentTxHistory)
	case "yes", "confirm", "ok", "okay", "proceed", "go ahead", "do it", "sure", "yep", "yh", "yeah", "alright":
		return string(llm.IntentConfirmTx)
	case "no", "cancel", "stop", "abort", "nevermind", "nope", "don't":
		return string(llm.IntentCancelTx)
	}
	return ""
}

func hashMessage(msg string) string {
	h := sha256.Sum256([]byte(msg))
	return hex.EncodeToString(h[:16])
}

func formatAmount(a model.Amount) string {
	return fmt.Sprintf("%.2f", a.NGN())
}

type ChatHandler struct {
	transferService *service.TransferService
	walletService   *service.WalletService
	authService     *service.AuthService
	bankRepo        repository.BankAccountRepository
	txnRepo         repository.TransactionRepository
	paystack        *paystack.Client
	rdb             *redis.Client
	llm             *llm.Client
	logger          *slog.Logger
}

func NewChatHandler(
	transferService *service.TransferService,
	walletService *service.WalletService,
	authService *service.AuthService,
	bankRepo repository.BankAccountRepository,
	txnRepo repository.TransactionRepository,
	paystackClient *paystack.Client,
	rdb *redis.Client,
	llmClient *llm.Client,
	logger *slog.Logger,
) *ChatHandler {
	return &ChatHandler{
		transferService: transferService,
		walletService:   walletService,
		authService:     authService,
		bankRepo:        bankRepo,
		txnRepo:         txnRepo,
		paystack:        paystackClient,
		rdb:             rdb,
		llm:             llmClient,
		logger:          logger,
	}
}

func (h *ChatHandler) RegisterRoutes(r chi.Router) {
	r.Post("/chat/message", h.HandleMessage)
	r.Post("/chat/confirm", h.ConfirmTransfer)
}

type chatMessageRequest struct {
	Message string `json:"message"`
}

type intentResponse struct {
	Response string     `json:"response"`
	Intent   llm.Intent `json:"intent"`
	Data     interface{} `json:"data,omitempty"`
}

// pendingTransfer is stored in Redis while awaiting user confirmation.
type pendingTransfer struct {
	UserID           string  `json:"user_id"`
	Amount           int64   `json:"amount"` // kobo
	RecipientAccount string  `json:"recipient_account"`
	RecipientBank    string  `json:"recipient_bank"`
	RecipientName    string  `json:"recipient_name"`
	IdempotencyKey   string  `json:"idempotency_key"`
}

func pendingKey(userID uuid.UUID) string  { return "chat:pending:" + userID.String() }
func historyKey(userID uuid.UUID) string  { return "chat:history:" + userID.String() }

const maxHistoryMessages = 10 // last 5 turns (user+assistant pairs)

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

	if h.llm == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "AI assistant is not configured. Please set up the LLM API key.",
			Intent:   llm.IntentUnknown,
		})
		return
	}

	// Load conversation history
	history := h.loadHistory(r.Context(), userID)
	history = append(history, llm.Message{Role: "user", Content: req.Message})

	// Keyword pre-filter: short-circuit unambiguous single-word commands before hitting the LLM.
	if shortcut := keywordIntent(req.Message); shortcut != "" {
		crw := &capturingResponseWriter{ResponseWriter: w}
		w = crw
		defer func() {
			if crw.body != nil {
				var resp intentResponse
				if json.Unmarshal(crw.body, &resp) == nil && resp.Response != "" {
					history = append(history, llm.Message{Role: "assistant", Content: resp.Response})
					h.saveHistory(r.Context(), userID, history)
				}
			}
		}()
		switch shortcut {
		case string(llm.IntentHelp):
			h.handleHelp(w)
		case string(llm.IntentCheckBal):
			h.handleCheckBalance(w, r, userID)
		case string(llm.IntentListBanks):
			h.handleListBanks(w, r, userID)
		case string(llm.IntentTxHistory):
			h.handleTransferHistory(w, r, userID)
		case string(llm.IntentConfirmTx):
			h.handleConfirmTransfer(w, r, userID)
		case string(llm.IntentCancelTx):
			h.handleCancelTransfer(w, r, userID)
		}
		return
	}

	// Build L1 system context: user state snapshot on every call
	systemContext := h.buildSystemContext(r.Context(), userID)

	parsed, err := h.llm.ParseIntentWithContext(r.Context(), history, systemContext)
	if err != nil {
		h.logger.Error("llm parse failed", "error", err, "message", req.Message)
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Sorry, I couldn't understand that. Try something like: 'send 2000 naira to 0123456789'",
			Intent:   llm.IntentUnknown,
		})
		return
	}

	h.logger.Info("intent parsed", "user_id", userID, "intent", parsed.Intent, "raw", req.Message)

	// Wrap response writer to capture the assistant reply for history
	crw := &capturingResponseWriter{ResponseWriter: w}
	w = crw
	defer func() {
		if crw.body != nil {
			var resp intentResponse
			if json.Unmarshal(crw.body, &resp) == nil && resp.Response != "" {
				history = append(history, llm.Message{Role: "assistant", Content: resp.Response})
				h.saveHistory(r.Context(), userID, history)
			}
		}
	}()

	switch parsed.Intent {
	case llm.IntentSendMoney:
		h.handleSendMoney(w, r, userID, parsed)
	case llm.IntentConfirmTx:
		h.handleConfirmTransfer(w, r, userID)
	case llm.IntentCancelTx:
		h.handleCancelTransfer(w, r, userID)
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

// --- Send Money (now shows preview and waits for confirmation) ---

func (h *ChatHandler) handleSendMoney(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	amount := model.NewAmount(int64(parsed.Amount))
	if amount.IsZero() {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "How much would you like to send? For example: 'send 5000 naira to 0123456789'",
			Intent:   parsed.Intent,
		})
		return
	}

	if parsed.RecipientAccount == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("Got it — ₦%s. What account number should I send to?", formatAmount(amount)),
			Intent:   parsed.Intent,
		})
		return
	}

	// Try to resolve account name if paystack available
	recipientName := parsed.RecipientName
	if recipientName == "" && h.paystack != nil && parsed.RecipientBank != "" {
		banks, _ := h.paystack.ListBanks(r.Context())
		bankCode := resolveBankCode(banks, parsed.RecipientBank)
		if bankCode != "" {
			if res, err := h.paystack.ResolveAccount(r.Context(), parsed.RecipientAccount, bankCode); err == nil {
				recipientName = res.Data.AccountName
			}
		}
	}

	// Store pending transfer in Redis
	pending := pendingTransfer{
		UserID:           userID.String(),
		Amount:           amount.Kobo(),
		RecipientAccount: parsed.RecipientAccount,
		RecipientBank:    parsed.RecipientBank,
		RecipientName:    recipientName,
		IdempotencyKey:   userID.String() + ":" + hashMessage(parsed.Raw),
	}
	if h.rdb != nil {
		data, _ := json.Marshal(pending)
		h.rdb.Set(r.Context(), pendingKey(userID), data, 10*time.Minute)
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Here's the transfer plan:\n\n"))
	msg.WriteString(fmt.Sprintf("  Amount:    ₦%s\n", formatAmount(amount)))
	msg.WriteString(fmt.Sprintf("  To:        %s", parsed.RecipientAccount))
	if parsed.RecipientBank != "" {
		msg.WriteString(fmt.Sprintf(" (%s)", parsed.RecipientBank))
	}
	msg.WriteString("\n")
	if recipientName != "" {
		msg.WriteString(fmt.Sprintf("  Name:      %s\n", recipientName))
	}
	msg.WriteString("\nReply \"yes\" to confirm or \"cancel\" to abort.")

	respondJSON(w, http.StatusOK, intentResponse{
		Response: msg.String(),
		Intent:   parsed.Intent,
		Data: map[string]interface{}{
			"amount":            amount.NGN(),
			"recipient_account": parsed.RecipientAccount,
			"recipient_bank":    parsed.RecipientBank,
			"recipient_name":    recipientName,
			"awaiting_confirm":  true,
		},
	})
}

// --- Confirm Transfer ---

func (h *ChatHandler) handleConfirmTransfer(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	if h.rdb == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "No pending transfer found. Start a new transfer by saying \"send 5000 to 0123456789\".",
			Intent:   llm.IntentConfirmTx,
		})
		return
	}

	raw, err := h.rdb.Get(r.Context(), pendingKey(userID)).Bytes()
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "No pending transfer to confirm. It may have expired (10 min limit). Start a new one.",
			Intent:   llm.IntentConfirmTx,
		})
		return
	}

	var pending pendingTransfer
	if err := json.Unmarshal(raw, &pending); err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Something went wrong. Please try your transfer again.", Intent: llm.IntentConfirmTx})
		return
	}

	h.rdb.Del(r.Context(), pendingKey(userID))

	amount := model.Amount(pending.Amount)
	result, err := h.transferService.InitiateTransfer(r.Context(), userID, service.TransferRequest{
		Amount:           amount,
		RecipientAccount: pending.RecipientAccount,
		RecipientBank:    pending.RecipientBank,
		RecipientName:    pending.RecipientName,
		IdempotencyKey:   pending.IdempotencyKey,
	})
	if err != nil {
		var response string
		switch {
		case errors.Is(err, service.ErrInsufficientFunds):
			response = "You don't have enough funds to complete this transfer. Fund your wallet or link a bank account with sufficient balance."
		default:
			response = "The transfer couldn't be completed: " + err.Error()
		}
		respondJSON(w, http.StatusOK, intentResponse{Response: response, Intent: llm.IntentConfirmTx})
		return
	}

	var msg strings.Builder
	msg.WriteString("✅ Done!\n\n")
	totalFees := model.Amount(0)
	if result.DebitPlan != nil {
		for _, leg := range result.DebitPlan.Legs {
			totalFees += leg.Fee
			feeStr := ""
			if leg.Fee > 0 {
				feeStr = fmt.Sprintf(" + ₦%s fee", formatAmount(leg.Fee))
			}
			msg.WriteString(fmt.Sprintf("  %s  -₦%s%s\n", leg.BankName, formatAmount(leg.Amount), feeStr))
		}
	}
	recipient := pending.RecipientAccount
	if pending.RecipientName != "" {
		recipient = fmt.Sprintf("%s (%s)", pending.RecipientName, pending.RecipientAccount)
	}
	msg.WriteString(fmt.Sprintf("\nSent ₦%s to %s", formatAmount(model.Amount(pending.Amount)), recipient))
	if totalFees > 0 {
		msg.WriteString(fmt.Sprintf(" · fees ₦%s", formatAmount(totalFees)))
	}
	msg.WriteString(fmt.Sprintf("\nRef: %s", result.OurRef))

	respondJSON(w, http.StatusOK, intentResponse{
		Response: msg.String(),
		Intent:   llm.IntentConfirmTx,
		Data:     map[string]interface{}{"transaction_id": result.TransactionID, "our_ref": result.OurRef},
	})
}

// --- Cancel Transfer ---

func (h *ChatHandler) handleCancelTransfer(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	if h.rdb != nil {
		h.rdb.Del(r.Context(), pendingKey(userID))
	}
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "Transfer cancelled. Let me know if you'd like to do something else.",
		Intent:   llm.IntentCancelTx,
	})
}

// --- Check Balance ---

func (h *ChatHandler) handleCheckBalance(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	wallet, err := h.walletService.GetBalance(r.Context(), userID)
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Sorry, I couldn't fetch your balance right now.", Intent: llm.IntentCheckBal})
		return
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("💰 Wallet: ₦%s", formatAmount(wallet.Balance)))
	if wallet.Balance != wallet.LedgerBalance {
		msg.WriteString(fmt.Sprintf(" (₦%s available)", formatAmount(wallet.LedgerBalance)))
	}

	banks, _ := h.bankRepo.GetByUserID(r.Context(), userID, 10, 0)
	if len(banks) > 0 {
		msg.WriteString(fmt.Sprintf("\n\n🏦 Linked banks (%d):", len(banks)))
		for _, b := range banks {
			msg.WriteString(fmt.Sprintf("\n  • %s — ₦%s", b.BankName, formatAmount(b.LastBalance)))
		}
	} else {
		msg.WriteString("\n\nNo linked banks yet. Tap Banks to add one.")
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: msg.String(),
		Intent:   llm.IntentCheckBal,
		Data:     map[string]interface{}{"balance": wallet.Balance.NGN(), "ledger_balance": wallet.LedgerBalance.NGN()},
	})
}

// --- Transfer History ---

func (h *ChatHandler) handleTransferHistory(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	txns, err := h.txnRepo.ListByUserID(r.Context(), userID, repository.TransactionFilter{Limit: 5})
	if err != nil || len(txns) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{Response: "You have no recent transfers.", Intent: llm.IntentTxHistory})
		return
	}

	var msg strings.Builder
	var needsReview []string

	msg.WriteString(fmt.Sprintf("Last %d transfer(s):\n", len(txns)))
	for _, t := range txns {
		statusIcon := "✅"
		if t.Status == "FAILED" {
			statusIcon = "❌"
			needsReview = append(needsReview, fmt.Sprintf("  ⚠️  %s failed: %s", t.OurRef, t.FailureReason))
		} else if t.Status == "PENDING" {
			statusIcon = "⏳"
		}
		msg.WriteString(fmt.Sprintf("\n%s ₦%s → %s  %s  %s",
			statusIcon, formatAmount(t.Amount), t.RecipientAccount,
			t.OurRef, t.CreatedAt.Format("Jan 2 15:04"),
		))
	}

	if len(needsReview) > 0 {
		msg.WriteString("\n\nNeeds attention:")
		for _, r := range needsReview {
			msg.WriteString("\n" + r)
		}
	}

	// Compressed summary for data field — not raw structs
	type txnSummary struct {
		Ref       string `json:"ref"`
		Amount    float64 `json:"amount"`
		Recipient string `json:"recipient"`
		Status    string `json:"status"`
		Date      string `json:"date"`
	}
	summaries := make([]txnSummary, len(txns))
	for i, t := range txns {
		summaries[i] = txnSummary{t.OurRef, t.Amount.NGN(), t.RecipientAccount, string(t.Status), t.CreatedAt.Format(time.RFC3339)}
	}

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentTxHistory, Data: summaries})
}

// --- Transfer Status ---

func (h *ChatHandler) handleTransferStatus(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	if parsed.Reference == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which transfer? Please provide the reference (e.g. WVF-xxx) or say \"show my recent transfers\" to find it.",
			Intent:   llm.IntentTxStatus,
		})
		return
	}

	txn, err := h.txnRepo.GetByOurRef(r.Context(), parsed.Reference)
	if err != nil || txn == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("No transfer found with reference \"%s\".", parsed.Reference),
			Intent:   llm.IntentTxStatus,
		})
		return
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Transfer %s:\n\n", txn.OurRef))
	msg.WriteString(fmt.Sprintf("  Amount:    ₦%s\n", formatAmount(txn.Amount)))
	msg.WriteString(fmt.Sprintf("  To:        %s\n", txn.RecipientAccount))
	msg.WriteString(fmt.Sprintf("  Status:    %s\n", txn.Status))
	msg.WriteString(fmt.Sprintf("  Date:      %s", txn.CreatedAt.Format("Jan 2, 2006 15:04")))
	if txn.FailureReason != "" {
		msg.WriteString(fmt.Sprintf("\n  ⚠️ Reason: %s", txn.FailureReason))
	}

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentTxStatus, Data: txn})
}

// --- Link Bank ---

func (h *ChatHandler) handleLinkBank(w http.ResponseWriter) {
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "To link a bank account, tap the Banks tab at the bottom then tap \"Link Bank\". You'll go through the Mono secure flow to connect your account.",
		Intent:   llm.IntentLinkBank,
	})
}

// --- List Banks ---

func (h *ChatHandler) handleListBanks(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	banks, err := h.bankRepo.GetByUserID(r.Context(), userID, 20, 0)
	if err != nil || len(banks) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "You don't have any linked bank accounts yet. Tap the Banks tab to link one.",
			Intent:   llm.IntentListBanks,
		})
		return
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("You have %d linked bank account(s):\n", len(banks)))
	for i, b := range banks {
		msg.WriteString(fmt.Sprintf("\n%d. %s · %s\n   Balance: ₦%s · Priority: %d",
			i+1, b.BankName, b.AccountNumber, formatAmount(b.LastBalance), b.Priority,
		))
	}
	msg.WriteString("\n\nSay \"refresh [bank name] balance\" or \"set [bank] as priority 1\" to manage them.")

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentListBanks, Data: banks})
}

// --- Unlink Bank ---

func (h *ChatHandler) handleUnlinkBank(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	if parsed.BankIdentifier == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which bank would you like to unlink? Say \"unlink GTBank\" or \"show my banks\" to see your linked accounts.",
			Intent:   llm.IntentUnlinkBank,
		})
		return
	}

	bank := h.findBankByIdentifier(r.Context(), userID, parsed.BankIdentifier)
	if bank == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("I couldn't find a linked bank matching \"%s\". Say \"show my banks\" to see your accounts.", parsed.BankIdentifier),
			Intent:   llm.IntentUnlinkBank,
		})
		return
	}

	if err := h.bankRepo.Delete(r.Context(), bank.ID); err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Failed to unlink the bank. Please try again.", Intent: llm.IntentUnlinkBank})
		return
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf("✅ %s (%s) has been unlinked.", bank.BankName, bank.AccountNumber),
		Intent:   llm.IntentUnlinkBank,
	})
}

// --- Set Priority ---

func (h *ChatHandler) handleSetPriority(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	if parsed.BankIdentifier == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which bank should I update? Say something like \"set GTBank as priority 1\".",
			Intent:   llm.IntentSetPriority,
		})
		return
	}

	priority := parsed.Priority
	if priority < 1 || priority > 5 {
		priority = 1
	}

	bank := h.findBankByIdentifier(r.Context(), userID, parsed.BankIdentifier)
	if bank == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("I couldn't find a linked bank matching \"%s\".", parsed.BankIdentifier),
			Intent:   llm.IntentSetPriority,
		})
		return
	}

	if err := h.bankRepo.UpdatePriority(r.Context(), bank.ID, priority); err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Failed to update priority. Please try again.", Intent: llm.IntentSetPriority})
		return
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf("✅ %s is now priority %d. It will be %s when sourcing funds.",
			bank.BankName, priority,
			map[int]string{1: "used first", 2: "used second", 3: "used third", 4: "used fourth", 5: "used last"}[priority],
		),
		Intent: llm.IntentSetPriority,
	})
}

// --- Refresh Balance ---

func (h *ChatHandler) handleRefreshBalance(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	banks, _ := h.bankRepo.GetByUserID(r.Context(), userID, 20, 0)
	if len(banks) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{Response: "You have no linked banks to refresh.", Intent: llm.IntentRefreshBalance})
		return
	}

	// Filter to specific bank if mentioned
	targets := banks
	if parsed.BankIdentifier != "" {
		b := h.findBankByIdentifier(r.Context(), userID, parsed.BankIdentifier)
		if b != nil {
			targets = []model.BankAccount{*b}
		}
	}

	var msg strings.Builder
	msg.WriteString("Balance refresh results:\n")
	for _, b := range targets {
		msg.WriteString(fmt.Sprintf("\n  • %s — ₦%s", b.BankName, formatAmount(b.LastBalance)))
	}
	msg.WriteString("\n\nNote: Balances shown are the last fetched values. Tap Refresh on the Banks tab for live data.")

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentRefreshBalance})
}

// --- Fund Wallet ---

func (h *ChatHandler) handleFundWallet(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	account, err := h.walletService.GetWalletAccount(r.Context(), userID)
	if err != nil || account == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "You don't have a virtual account yet. Go to the Accounts tab and tap \"Create Account\" to get a dedicated account number you can fund from any bank.",
			Intent:   llm.IntentFundWallet,
		})
		return
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf(
			"Transfer money to your Weave wallet:\n\n"+
				"  Bank:    %s\n"+
				"  Account: %s\n"+
				"  Name:    %s\n\n"+
				"Your wallet balance will update automatically once received.",
			account.BankName, account.AccountNumber, account.AccountName,
		),
		Intent: llm.IntentFundWallet,
		Data:   account,
	})
}

// --- Wallet History ---

func (h *ChatHandler) handleWalletHistory(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	txns, err := h.txnRepo.ListByUserID(r.Context(), userID, repository.TransactionFilter{Limit: 5})
	if err != nil || len(txns) == 0 {
		respondJSON(w, http.StatusOK, intentResponse{Response: "No wallet transactions found.", Intent: llm.IntentWalletHistory})
		return
	}

	var msg strings.Builder
	msg.WriteString("Recent wallet activity:\n")
	for _, t := range txns {
		msg.WriteString(fmt.Sprintf("\n• %s ₦%s · %s",
			t.Type, formatAmount(t.Amount), t.CreatedAt.Format("Jan 2, 15:04"),
		))
	}

	respondJSON(w, http.StatusOK, intentResponse{Response: msg.String(), Intent: llm.IntentWalletHistory, Data: txns})
}

// --- Lookup Account ---

func (h *ChatHandler) handleLookupAccount(w http.ResponseWriter, r *http.Request, parsed *llm.ParsedIntent) {
	if h.paystack == nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Account lookup is not available right now (Paystack not configured).",
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	if parsed.RecipientAccount == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Which account number would you like to look up? Say \"who is 0123456789 at GTBank?\"",
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	if parsed.RecipientBank == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("Which bank is account %s with?", parsed.RecipientAccount),
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	banks, err := h.paystack.ListBanks(r.Context())
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{Response: "Couldn't fetch bank list right now. Please try again.", Intent: llm.IntentLookupAccount})
		return
	}

	bankCode := resolveBankCode(banks, parsed.RecipientBank)
	if bankCode == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("I don't recognise \"%s\" as a bank. Try the full name, e.g. \"GTBank\", \"Access Bank\", \"Zenith Bank\".", parsed.RecipientBank),
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	result, err := h.paystack.ResolveAccount(r.Context(), parsed.RecipientAccount, bankCode)
	if err != nil {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: fmt.Sprintf("Account %s at %s could not be resolved. Double-check the number and bank.", parsed.RecipientAccount, parsed.RecipientBank),
			Intent:   llm.IntentLookupAccount,
		})
		return
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: fmt.Sprintf("✅ Found:\n\n  Name:    %s\n  Account: %s\n  Bank:    %s\n\nSay \"send [amount] to %s at %s\" to transfer to them.",
			result.Data.AccountName, result.Data.AccountNumber, parsed.RecipientBank,
			result.Data.AccountNumber, parsed.RecipientBank,
		),
		Intent: llm.IntentLookupAccount,
		Data:   result.Data,
	})
}

// --- Help ---

func (h *ChatHandler) handleHelp(w http.ResponseWriter) {
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "Here's everything I can help you with:\n\n" +
			"💸 Send money — \"send 5000 to 0123456789 at GTBank\"\n" +
			"✅ Confirm/cancel — \"yes\" or \"cancel\" after a transfer preview\n" +
			"💰 Check balance — \"what's my balance?\"\n" +
			"📋 Transfer history — \"show my recent transfers\"\n" +
			"🔎 Transfer status — \"what's the status of WVF-abc123?\"\n" +
			"🔍 Lookup account — \"who is 0123456789 at Zenith?\"\n" +
			"➕ Fund wallet — \"how do I fund my wallet?\"\n" +
			"📊 Wallet activity — \"show my wallet history\"\n" +
			"🏦 Linked banks — \"show my bank accounts\"\n" +
			"🔗 Link a bank — \"link my GTBank account\"\n" +
			"🗑️ Unlink a bank — \"unlink my Access Bank\"\n" +
			"⭐ Set priority — \"make Zenith my priority 1 account\"\n" +
			"🔄 Refresh balance — \"refresh my GTBank balance\"",
		Intent: llm.IntentHelp,
	})
}

// --- Confirm (legacy REST endpoint) ---

func (h *ChatHandler) ConfirmTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h.handleConfirmTransfer(w, r, userID)
}

// --- Helpers ---

func (h *ChatHandler) findBankByIdentifier(ctx context.Context, userID uuid.UUID, identifier string) *model.BankAccount {
	banks, err := h.bankRepo.GetByUserID(ctx, userID, 20, 0)
	if err != nil {
		return nil
	}
	lower := strings.ToLower(identifier)
	for i, b := range banks {
		if strings.Contains(strings.ToLower(b.BankName), lower) ||
			strings.Contains(b.AccountNumber, identifier) {
			return &banks[i]
		}
	}
	return nil
}

func resolveBankCode(banks []paystack.Bank, name string) string {
	lower := strings.ToLower(name)
	for _, b := range banks {
		if strings.Contains(strings.ToLower(b.Name), lower) ||
			strings.Contains(strings.ToLower(b.Slug), lower) {
			return b.Code
		}
	}
	return ""
}

// --- L1 system context: always-resident user state snapshot ---

func (h *ChatHandler) buildSystemContext(ctx context.Context, userID uuid.UUID) string {
	var sb strings.Builder

	// Wallet balance
	if wallet, err := h.walletService.GetBalance(ctx, userID); err == nil {
		sb.WriteString(fmt.Sprintf("WALLET: ₦%s available (₦%s total)\n", formatAmount(wallet.LedgerBalance), formatAmount(wallet.Balance)))
	}

	// Linked banks — compressed to one line each
	if banks, err := h.bankRepo.GetByUserID(ctx, userID, 10, 0); err == nil && len(banks) > 0 {
		sb.WriteString(fmt.Sprintf("LINKED BANKS (%d):\n", len(banks)))
		for _, b := range banks {
			sb.WriteString(fmt.Sprintf("  • %s [%s] ₦%s priority=%d\n",
				b.BankName, b.AccountNumber, formatAmount(b.LastBalance), b.Priority))
		}
	} else {
		sb.WriteString("LINKED BANKS: none\n")
	}

	// Pending transfer (with confirm/cancel guidance)
	if h.rdb != nil {
		if raw, err := h.rdb.Get(ctx, pendingKey(userID)).Bytes(); err == nil {
			var p pendingTransfer
			if json.Unmarshal(raw, &p) == nil {
				sb.WriteString(fmt.Sprintf(
					"\nPENDING TRANSFER (awaiting confirmation):\n  Amount: ₦%.2f  To: %s  Bank: %s  Name: %s\n"+
						"  → Affirmative (yes/ok/proceed/go ahead/do it/sure/confirm/alright) = confirm_transfer\n"+
						"  → Negative (no/cancel/stop/abort/nevermind/don't) = cancel_transfer\n",
					float64(p.Amount)/100, p.RecipientAccount, p.RecipientBank, p.RecipientName,
				))
			}
		}
	}

	// Virtual account (for fund_wallet responses)
	if acct, err := h.walletService.GetWalletAccount(ctx, userID); err == nil && acct != nil {
		sb.WriteString(fmt.Sprintf("VIRTUAL ACCOUNT: %s at %s (name: %s)\n",
			acct.AccountNumber, acct.BankName, acct.AccountName))
	} else {
		sb.WriteString("VIRTUAL ACCOUNT: not yet created\n")
	}

	return sb.String()
}

// --- Conversation history (stored as JSON array in Redis) ---

func (h *ChatHandler) loadHistory(ctx context.Context, userID uuid.UUID) []llm.Message {
	if h.rdb == nil {
		return nil
	}
	raw, err := h.rdb.Get(ctx, historyKey(userID)).Bytes()
	if err != nil {
		return nil
	}
	var msgs []llm.Message
	_ = json.Unmarshal(raw, &msgs)
	return msgs
}

func (h *ChatHandler) saveHistory(ctx context.Context, userID uuid.UUID, msgs []llm.Message) {
	if h.rdb == nil {
		return
	}
	// Keep only the last N messages
	if len(msgs) > maxHistoryMessages {
		msgs = msgs[len(msgs)-maxHistoryMessages:]
	}
	data, err := json.Marshal(msgs)
	if err != nil {
		return
	}
	h.rdb.Set(ctx, historyKey(userID), data, 2*time.Hour)
}

// --- capturingResponseWriter lets us read what was written to capture the response ---

type capturingResponseWriter struct {
	http.ResponseWriter
	body       []byte
	statusCode int
}

func (c *capturingResponseWriter) WriteHeader(code int) {
	c.statusCode = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *capturingResponseWriter) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return c.ResponseWriter.Write(b)
}

// Suppress unused import errors
var _ = errors.New
var _ = time.Now
