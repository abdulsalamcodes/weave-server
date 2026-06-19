package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
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
	logger        *slog.Logger
}

func NewWebhookHandler(walletService *service.WalletService, paystackClient *paystack.Client, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{
		walletService: walletService,
		paystack:      paystackClient,
		logger:        logger,
	}
}

func (h *WebhookHandler) RegisterRoutes(r chi.Router) {
	r.Post("/webhook/paystack", h.PaystackDeposit)
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
