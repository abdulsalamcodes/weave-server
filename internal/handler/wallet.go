package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/mono"
	"github.com/abdulsalamcodes/weave-server/internal/provider/paystack"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

type WalletHandler struct {
	walletService *service.WalletService
	logger        *slog.Logger
}

func NewWalletHandler(walletService *service.WalletService, logger *slog.Logger) *WalletHandler {
	return &WalletHandler{walletService: walletService, logger: logger}
}

func (h *WalletHandler) RegisterRoutes(r chi.Router) {
	r.Get("/wallet", h.GetWallet)
	r.Get("/wallet/account", h.GetAccount)
	r.Post("/wallet/account", h.IssueAccount)
	r.Post("/wallet/fund", h.FundFromBank)
}

type fundFromBankRequest struct {
	BankAccountID uuid.UUID `json:"bank_account_id"`
	Amount        int64     `json:"amount"` // kobo
}

type fundFromBankResponse struct {
	Reference string  `json:"reference"`
	Status    string  `json:"status"`
	AmountNGN float64 `json:"amount_ngn"`
}

func (h *WalletHandler) FundFromBank(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req fundFromBankRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}
	if req.BankAccountID == uuid.Nil {
		respondError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	fundReq, err := h.walletService.FundFromBank(r.Context(), userID, req.BankAccountID, model.Amount(req.Amount))
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAmountTooSmall):
			respondError(w, http.StatusBadRequest, "amount_too_small")
		case errors.Is(err, service.ErrAmountTooLarge):
			respondError(w, http.StatusBadRequest, "amount_too_large")
		case errors.Is(err, service.ErrDailyLimitExceeded):
			respondError(w, http.StatusUnprocessableEntity, "daily_limit_exceeded")
		case errors.Is(err, service.ErrForbidden):
			respondError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, service.ErrBankNotActive):
			respondError(w, http.StatusUnprocessableEntity, "bank_not_active")
		case errors.Is(err, service.ErrMonoUnavailable):
			respondError(w, http.StatusServiceUnavailable, "mono_unavailable")
		case errors.Is(err, service.ErrDirectDebitFailed):
			respondError(w, http.StatusServiceUnavailable, "direct_debit_failed")
		default:
			h.logger.Error("fund_from_bank failed", "error", err, "user_id", userID)
			respondError(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}

	respondJSON(w, http.StatusAccepted, fundFromBankResponse{
		Reference: fundReq.Reference,
		Status:    fundReq.Status,
		AmountNGN: model.Amount(req.Amount).NGN(),
	})
}

func (h *WalletHandler) GetWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	wallet, err := h.walletService.GetBalance(r.Context(), userID)
	if err != nil {
		if errors.Is(err, service.ErrWalletNotFound) {
			respondError(w, http.StatusNotFound, "wallet_not_found")
			return
		}
		h.logger.Error("get wallet failed", "error", err)
		respondError(w, http.StatusInternalServerError, "get_wallet_failed")
		return
	}

	respondJSON(w, http.StatusOK, wallet)
}

type issueAccountResponse struct {
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
	BankName      string `json:"bank_name"`
	BankCode      string `json:"bank_code"`
}

func (h *WalletHandler) GetAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	account, err := h.walletService.GetWalletAccount(r.Context(), userID)
	if err != nil {
		h.logger.Error("get account failed", "error", err)
		respondError(w, http.StatusInternalServerError, "get_account_failed")
		return
	}
	if account == nil {
		respondError(w, http.StatusNotFound, "no_wallet_account")
		return
	}

	respondJSON(w, http.StatusOK, issueAccountResponse{
		AccountNumber: account.AccountNumber,
		AccountName:   account.AccountName,
		BankName:      account.BankName,
		BankCode:      account.BankCode,
	})
}

func (h *WalletHandler) IssueAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	account, err := h.walletService.IssueWalletAccount(r.Context(), userID)
	if err != nil {
		h.logger.Error("issue account failed", "error", err)
		respondErrorMsg(w, http.StatusInternalServerError, "issue_account_failed", err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, issueAccountResponse{
		AccountNumber: account.AccountNumber,
		AccountName:   account.AccountName,
		BankName:      account.BankName,
		BankCode:      account.BankCode,
	})
}

// Webhook handler
type WebhookHandler struct {
	walletService *service.WalletService
	paystack      *paystack.Client
	mono          *mono.Client
	logger        *slog.Logger
}

func NewWebhookHandler(walletService *service.WalletService, paystackClient *paystack.Client, monoClient *mono.Client, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{
		walletService: walletService,
		paystack:      paystackClient,
		mono:          monoClient,
		logger:        logger,
	}
}

func (h *WebhookHandler) RegisterRoutes(r chi.Router) {
	r.Post("/webhook/paystack", h.PaystackDeposit)
	r.Post("/webhook/mono", h.MonoDirectDebit)
}

type monoDirectDebitPayload struct {
	Event string `json:"event"`
	Data  struct {
		Reference string `json:"reference"`
		Amount    int64  `json:"amount"` // kobo
		Status    string `json:"status"`
	} `json:"data"`
}

func (h *WebhookHandler) MonoDirectDebit(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	if h.mono != nil {
		sig := r.Header.Get("mono-signature")
		if sig != "" && !h.mono.VerifyWebhook(sig, body) {
			h.logger.Warn("invalid mono webhook signature")
			respondError(w, http.StatusUnauthorized, "invalid_signature")
			return
		}
	}

	var payload monoDirectDebitPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error("invalid mono webhook payload", "error", err)
		respondError(w, http.StatusBadRequest, "invalid_payload")
		return
	}

	if payload.Event != "mono.events.direct_debit.successful" {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	ref := payload.Data.Reference
	amount := model.Amount(payload.Data.Amount)

	if err := h.walletService.CompleteFundFromBank(r.Context(), ref, amount); err != nil {
		// Log but still return 200 — a non-200 causes Mono to retry, which risks double-credit.
		h.logger.Error("complete_fund_from_bank failed", "reference", ref, "error", err)
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

type paystackWebhookPayload struct {
	Event string `json:"event"`
	Data  struct {
		Amount          int64  `json:"amount"`
		Reference       string `json:"reference"`
		Fees            int64  `json:"fees"`
		DedicatedAccount struct {
			AccountNumber string `json:"account_number"`
		} `json:"dedicated_account"`
		SenderAccount string `json:"sender_account,omitempty"`
		SenderBank    string `json:"sender_bank,omitempty"`
	} `json:"data"`
}

func (h *WebhookHandler) PaystackDeposit(w http.ResponseWriter, r *http.Request) {
	if h.paystack != nil {
		sig := r.Header.Get("x-paystack-signature")
		if sig != "" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				respondError(w, http.StatusBadRequest, "invalid_body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			if !h.paystack.VerifyWebhook(sig, body) {
				h.logger.Warn("invalid paystack webhook signature")
				respondError(w, http.StatusUnauthorized, "invalid_signature")
				return
			}
		}
	}

	var payload paystackWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.logger.Error("invalid webhook payload", "error", err)
		respondError(w, http.StatusBadRequest, "invalid_payload")
		return
	}

	// Only process successful charges
	if payload.Event != "charge.success" {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	accountNumber := payload.Data.DedicatedAccount.AccountNumber
	if accountNumber == "" {
		h.logger.Warn("webhook missing account number")
		respondError(w, http.StatusBadRequest, "missing_account_number")
		return
	}

	if err := h.walletService.ProcessDepositWebhook(
		r.Context(),
		accountNumber,
		model.Amount(payload.Data.Amount),
		model.Amount(payload.Data.Fees),
		"paystack",
		payload.Data.Reference,
	); err != nil {
		h.logger.Error("process deposit failed", "error", err)
		// Return 200 to prevent Paystack from retrying
		respondJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}
