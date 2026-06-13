package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/llm"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

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
	llm             *llm.Client
	logger          *slog.Logger
}

func NewChatHandler(
	transferService *service.TransferService,
	walletService *service.WalletService,
	authService *service.AuthService,
	llmClient *llm.Client,
	logger *slog.Logger,
) *ChatHandler {
	return &ChatHandler{
		transferService: transferService,
		walletService:   walletService,
		authService:     authService,
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
	Response string      `json:"response"`
	Intent   llm.Intent  `json:"intent"`
	Data     interface{} `json:"data,omitempty"`
}

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

	parsed, err := h.llm.ParseIntent(r.Context(), req.Message)
	if err != nil {
		h.logger.Error("llm parse failed", "error", err, "message", req.Message)
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Sorry, I couldn't understand that. Try something like: 'send 2000 naira to 0123456789'",
			Intent:   llm.IntentUnknown,
		})
		return
	}

	h.logger.Info("intent parsed",
		"user_id", userID,
		"intent", parsed.Intent,
		"raw", req.Message,
	)

	switch parsed.Intent {
	case llm.IntentSendMoney:
		h.handleSendMoney(w, r, userID, parsed)
	case llm.IntentCheckBal:
		h.handleCheckBalance(w, r, userID)
	case llm.IntentLinkBank:
		h.handleLinkBank(w, userID)
	case llm.IntentHelp:
		h.handleHelp(w)
	default:
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "I'm not sure what you want to do. You can say:\n" +
				"- 'send 2000 to 0123456789'\n" +
				"- 'check my balance'\n" +
				"- 'link my GTBank account'\n" +
				"- 'help'",
			Intent: parsed.Intent,
		})
	}
}

func (h *ChatHandler) handleSendMoney(w http.ResponseWriter, r *http.Request, userID uuid.UUID, parsed *llm.ParsedIntent) {
	amount := model.NewAmount(int64(parsed.Amount))
	if amount.IsZero() {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "How much would you like to send? For example: 'send 2000 naira to 0123456789'",
			Intent:   parsed.Intent,
		})
		return
	}

	if parsed.RecipientAccount == "" {
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "What account number should I send to?",
			Intent:   parsed.Intent,
			Data:     map[string]interface{}{"amount": amount.NGN()},
		})
		return
	}

	idempotencyKey := userID.String() + ":" + hashMessage(parsed.Raw)

	result, err := h.transferService.InitiateTransfer(r.Context(), userID, service.TransferRequest{
		Amount:           amount,
		RecipientAccount: parsed.RecipientAccount,
		RecipientBank:    parsed.RecipientBank,
		RecipientName:    parsed.RecipientName,
		IdempotencyKey:   idempotencyKey,
	})
	if err != nil {
		h.logger.Error("transfer failed", "error", err, "user_id", userID)

		var response string
		switch {
		case errors.Is(err, service.ErrInsufficientFunds):
			response = "You don't have enough funds. You can fund your wallet via bank transfer."
		default:
			response = "Sorry, the transfer couldn't be completed. Please try again."
		}

		respondJSON(w, http.StatusOK, intentResponse{
			Response: response,
			Intent:   parsed.Intent,
		})
		return
	}

	var breakdown strings.Builder
	breakdown.WriteString("Transfer completed!\n\n")
	if result.DebitPlan != nil {
		for _, leg := range result.DebitPlan.Legs {
			breakdown.WriteString("  - ")
			breakdown.WriteString(leg.BankName)
			breakdown.WriteString(": -")
			breakdown.WriteString(formatAmount(leg.Amount))
			if leg.Fee > 0 {
				breakdown.WriteString(" (+")
				breakdown.WriteString(formatAmount(leg.Fee))
				breakdown.WriteString(" fee)")
			}
			breakdown.WriteString("\n")
		}
	}
	breakdown.WriteString("\nRef: ")
	breakdown.WriteString(result.OurRef)
	breakdown.WriteString("\nRecipient: ")
	if parsed.RecipientName != "" {
		breakdown.WriteString(parsed.RecipientName)
		breakdown.WriteString(" - ")
	}
	breakdown.WriteString(parsed.RecipientAccount)

	respondJSON(w, http.StatusOK, intentResponse{
		Response: breakdown.String(),
		Intent:   parsed.Intent,
		Data: map[string]interface{}{
			"transaction_id": result.TransactionID,
			"our_ref":        result.OurRef,
			"amount":         amount.NGN(),
			"recipient":      parsed.RecipientAccount,
		},
	})
}

func (h *ChatHandler) handleCheckBalance(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	wallet, err := h.walletService.GetBalance(r.Context(), userID)
	if err != nil {
		h.logger.Error("balance check failed", "error", err)
		respondJSON(w, http.StatusOK, intentResponse{
			Response: "Sorry, I couldn't fetch your balance right now.",
			Intent:   llm.IntentCheckBal,
		})
		return
	}

	respondJSON(w, http.StatusOK, intentResponse{
		Response: "Your Weave wallet balance is " + formatAmount(wallet.Balance) + "\nAvailable: " + formatAmount(wallet.LedgerBalance),
		Intent:   llm.IntentCheckBal,
		Data: map[string]interface{}{
			"balance":        wallet.Balance.NGN(),
			"ledger_balance": wallet.LedgerBalance.NGN(),
		},
	})
}

func (h *ChatHandler) handleLinkBank(w http.ResponseWriter, userID uuid.UUID) {
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "To link your bank account, go to Settings > Link Bank or type 'link my GTBank account'.",
		Intent:   llm.IntentLinkBank,
	})
}

func (h *ChatHandler) handleHelp(w http.ResponseWriter) {
	respondJSON(w, http.StatusOK, intentResponse{
		Response: "I can help you with:\n\n" +
			"1. Send money: 'send 5000 to 0123456789'\n" +
			"2. Check balance: 'what's my balance?'\n" +
			"3. Link bank: 'link my account'\n" +
			"4. Recent transfers: 'show my transactions'",
		Intent: llm.IntentHelp,
	})
}

func (h *ChatHandler) ConfirmTransfer(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"status": "not_implemented",
	})
}
