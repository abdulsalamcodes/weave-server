package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"context"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/provider/mono"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

type BankHandler struct {
	bankRepo      repository.BankAccountRepository
	userRepo      repository.UserRepository
	walletService *service.WalletService
	monoClient    *mono.Client
	rdb           *redis.Client
	logger        *slog.Logger
}

func NewBankHandler(
	bankRepo repository.BankAccountRepository,
	userRepo repository.UserRepository,
	walletService *service.WalletService,
	monoClient *mono.Client,
	rdb *redis.Client,
	logger *slog.Logger,
) *BankHandler {
	return &BankHandler{
		bankRepo:      bankRepo,
		userRepo:      userRepo,
		walletService: walletService,
		monoClient:    monoClient,
		rdb:           rdb,
		logger:        logger,
	}
}

func (h *BankHandler) RegisterRoutes(r chi.Router) {
	r.Post("/banks/link", h.InitiateLink)
	r.Post("/banks/complete", h.CompleteLink)
	r.Post("/banks/exchange", h.ExchangeCode)
	r.Post("/banks/webhook/mono", h.MonoWebhook)
	r.Get("/banks", h.ListBanks)
	r.Put("/banks/{id}/priority", h.UpdatePriority)
	r.Delete("/banks/{id}", h.UnlinkBank)
	r.Post("/banks/{id}/refresh", h.RefreshBalance)
}

type initiateLinkRequest struct {
	Provider string `json:"provider"` // "mono"
}

func (h *BankHandler) InitiateLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req initiateLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	user, err := h.userRepo.GetByID(r.Context(), userID)
	if err != nil || user == nil {
		respondError(w, http.StatusNotFound, "user_not_found")
		return
	}

	switch req.Provider {
	case "mono":
		if h.monoClient == nil {
			respondError(w, http.StatusServiceUnavailable, "mono_not_configured")
			return
		}
		email := user.Email
		if email == "" {
			email = user.Phone + "@weave.ng"
		}
		ref := fmt.Sprintf("%s-%d", userID.String(), time.Now().UnixMilli())
		redirectURL := fmt.Sprintf("http://localhost:3000/app/banks?ref=%s", ref)
		result, err := h.monoClient.GenerateConnectURL(r.Context(), ref, user.FullName, email, redirectURL)
		if err != nil {
			h.logger.Error("mono connect failed", "error", err)
			respondErrorMsg(w, http.StatusInternalServerError, "connect_failed", err.Error())
			return
		}
		// Store ref → customer_id in Redis for 30 minutes
		if h.rdb != nil {
			customerID := result.Data.Customer
			h.logger.Info("mono connect initiated", "ref", ref, "customer_id", customerID)
			if customerID == "" {
				h.logger.Warn("mono returned empty customer_id — CompleteLink will fail for this ref", "ref", ref)
			}
			h.rdb.Set(context.Background(), "mono:ref:"+ref, customerID, 30*time.Minute)
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"connect_url": result.Data.MonoURL,
			"reference":   ref,
			"provider":    "mono",
		})

	default:
		respondError(w, http.StatusBadRequest, "invalid_provider")
	}
}

type completeLinkRequest struct {
	Ref string `json:"ref"`
}

func (h *BankHandler) CompleteLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req completeLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ref == "" {
		respondError(w, http.StatusBadRequest, "ref_required")
		return
	}

	if h.monoClient == nil {
		respondError(w, http.StatusServiceUnavailable, "mono_not_configured")
		return
	}

	// Look up customer_id from Redis
	customerID, err := h.rdb.Get(r.Context(), "mono:ref:"+req.Ref).Result()
	if err != nil {
		h.logger.Error("ref not found in redis", "ref", req.Ref, "error", err)
		respondError(w, http.StatusBadRequest, "ref_expired_or_invalid")
		return
	}

	if customerID == "" {
		h.logger.Error("customer_id is empty for ref", "ref", req.Ref)
		respondErrorMsg(w, http.StatusBadRequest, "invalid_session", "Bank linking session is invalid — please try linking again.")
		return
	}

	accounts, err := h.monoClient.GetCustomerAccounts(r.Context(), customerID)
	if err != nil {
		h.logger.Error("get customer accounts failed", "customer_id", customerID, "error", err)
		respondErrorMsg(w, http.StatusBadGateway, "fetch_accounts_failed", err.Error())
		return
	}

	h.logger.Info("mono customer accounts fetched", "customer_id", customerID, "count", len(accounts.Data))

	if len(accounts.Data) == 0 {
		respondErrorMsg(w, http.StatusNotFound, "no_accounts_found", "No accounts were found for this bank link. Try unlinking and re-linking.")
		return
	}

	var saved []model.BankAccount
	for _, acc := range accounts.Data {
		bank := &model.BankAccount{
			UserID:        userID,
			Provider:      "mono",
			ProviderToken: customerID,
			AccountNumber: acc.AccountNumber,
			AccountName:   acc.AccountName,
			BankCode:      "",
			BankName:      acc.Bank,
			LastBalance:   model.Amount(int64(acc.Balance)),
			Priority:      5,
			IsActive:      true,
			IsVerified:    true,
		}
		if err := h.bankRepo.Create(r.Context(), bank); err != nil {
			h.logger.Error("save bank account failed", "account_number", acc.AccountNumber, "error", err)
			continue
		}
		saved = append(saved, *bank)
	}

	h.rdb.Del(r.Context(), "mono:ref:"+req.Ref)
	h.logger.Info("bank accounts linked via mono", "count", len(saved), "user_id", userID)

	if len(saved) == 0 {
		respondErrorMsg(w, http.StatusInternalServerError, "save_failed", "Accounts were found but could not be saved. Please try again.")
		return
	}

	respondJSON(w, http.StatusOK, saved)
}

type exchangeCodeRequest struct {
	Code string `json:"code"`
}

func (h *BankHandler) ExchangeCode(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req exchangeCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		respondError(w, http.StatusBadRequest, "code_required")
		return
	}

	if h.monoClient == nil {
		respondError(w, http.StatusServiceUnavailable, "mono_not_configured")
		return
	}

	exchange, err := h.monoClient.ExchangeCode(r.Context(), req.Code)
	if err != nil {
		h.logger.Error("mono exchange failed", "error", err)
		respondErrorMsg(w, http.StatusInternalServerError, "exchange_failed", err.Error())
		return
	}

	accountID := exchange.Data.ID
	details, err := h.monoClient.SyncAccount(r.Context(), accountID)
	if err != nil {
		h.logger.Error("mono sync failed", "error", err)
		respondErrorMsg(w, http.StatusInternalServerError, "sync_failed", err.Error())
		return
	}

	bank := &model.BankAccount{
		UserID:        userID,
		Provider:      "mono",
		ProviderToken: accountID,
		AccountNumber: details.Data.AccountNumber,
		AccountName:   details.Data.AccountName,
		BankCode:      details.Data.BankCode,
		BankName:      details.Data.BankName,
		LastBalance:   model.NewAmount(int64(details.Data.Balance)),
		Priority:      5,
		IsActive:      true,
		IsVerified:    true,
	}

	if err := h.bankRepo.Create(r.Context(), bank); err != nil {
		h.logger.Error("save mono bank failed", "error", err)
		respondError(w, http.StatusInternalServerError, "save_failed")
		return
	}

	h.logger.Info("bank linked via mono exchange", "account", bank.AccountNumber, "user_id", userID)
	respondJSON(w, http.StatusOK, bank)
}

type monoWebhookPayload struct {
	Event string `json:"event"`
	Data  struct {
		// bank linking fields
		ID            string  `json:"id"`
		AccountNumber string  `json:"accountNumber"`
		AccountName   string  `json:"accountName"`
		BankName      string  `json:"bankName"`
		BankCode      string  `json:"bankCode"`
		Balance       float64 `json:"balance"`
		// payment fields
		Reference string  `json:"reference"`
		Amount    float64 `json:"amount"` // in kobo
		Status    string  `json:"status"`
	} `json:"data"`
}

func (h *BankHandler) MonoWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	if h.monoClient != nil {
		sig := r.Header.Get("mono-signature")
		if sig == "" || !h.monoClient.VerifyWebhook(sig, body) {
			h.logger.Warn("invalid mono webhook signature")
			respondError(w, http.StatusUnauthorized, "invalid_signature")
			return
		}
	}

	var payload monoWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_payload")
		return
	}

	h.logger.Info("mono webhook received", "event", payload.Event)

	switch payload.Event {
	case "payment.successful":
		ref := payload.Data.Reference
		amountKobo := model.Amount(int64(payload.Data.Amount))
		if err := h.walletService.CompleteFundFromBank(r.Context(), ref, amountKobo); err != nil {
			h.logger.Error("complete fund from bank failed", "reference", ref, "error", err)
			respondError(w, http.StatusInternalServerError, "complete_failed")
			return
		}
		h.logger.Info("wallet funded via mono payment", "reference", ref, "amount_kobo", amountKobo)
		respondJSON(w, http.StatusOK, map[string]string{"status": "success"})

	default:
		// Unrecognised events — acknowledge and ignore.
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "event": payload.Event})
	}
}

func (h *BankHandler) ListBanks(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	page, perPage := parsePagination(r, 1, 20)
	offset := (page - 1) * perPage

	accounts, err := h.bankRepo.GetByUserID(r.Context(), userID, perPage, offset)
	if err != nil {
		h.logger.Error("list banks failed", "error", err)
		respondError(w, http.StatusInternalServerError, "list_failed")
		return
	}

	if accounts == nil {
		accounts = []model.BankAccount{}
	}

	respondJSON(w, http.StatusOK, accounts)
}

type updatePriorityRequest struct {
	Priority int `json:"priority" validate:"min=1,max=5"`
}

func (h *BankHandler) UpdatePriority(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_id")
		return
	}

	var req updatePriorityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	if err := h.bankRepo.UpdatePriority(r.Context(), id, req.Priority); err != nil {
		h.logger.Error("update priority failed", "error", err)
		respondError(w, http.StatusInternalServerError, "update_failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *BankHandler) UnlinkBank(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_id")
		return
	}

	if err := h.bankRepo.Delete(r.Context(), id); err != nil {
		h.logger.Error("unlink bank failed", "error", err)
		respondError(w, http.StatusInternalServerError, "delete_failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *BankHandler) RefreshBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_id")
		return
	}

	bank, err := h.bankRepo.GetByID(r.Context(), id)
	if err != nil {
		h.logger.Error("get bank account failed", "error", err)
		respondError(w, http.StatusInternalServerError, "lookup_failed")
		return
	}
	if bank == nil || bank.UserID != userID {
		respondError(w, http.StatusNotFound, "bank_account_not_found")
		return
	}

	if bank.ProviderToken == "" {
		respondErrorMsg(w, http.StatusBadRequest, "no_provider_token",
			"This bank account has no provider token — please unlink and re-link it to restore the connection.")
		return
	}

	var balance float64
	switch bank.Provider {
	case "mono":
		if h.monoClient == nil {
			respondErrorMsg(w, http.StatusServiceUnavailable, "mono_not_configured",
				"Bank balance refresh is temporarily unavailable.")
			return
		}
		resp, err := h.monoClient.GetBalance(r.Context(), bank.ProviderToken)
		if err != nil {
			// If the token is a customer ID (not an account ID), GetBalance returns 404.
			// Fall back to GetCustomerAccounts which returns balance per account.
			accounts, fallbackErr := h.monoClient.GetCustomerAccounts(r.Context(), bank.ProviderToken)
			if fallbackErr != nil || len(accounts.Data) == 0 {
				h.logger.Error("mono balance refresh failed", "error", err, "account_id", bank.ID)
				respondErrorMsg(w, http.StatusBadGateway, "balance_refresh_failed",
					"Could not fetch live balance from your bank — the connection may have expired. Try unlinking and re-linking.")
				return
			}
			// Match by account number, or use the first account if number is missing.
			found := accounts.Data[0]
			for _, a := range accounts.Data {
				if a.AccountNumber == bank.AccountNumber {
					found = a
					break
				}
			}
			balance = found.Balance
		} else {
			balance = resp.Data.Balance
		}

	default:
		respondErrorMsg(w, http.StatusBadRequest, "unsupported_provider",
			"Balance refresh is not supported for this bank provider.")
		return
	}

	// Mono returns balance already in kobo — store directly without multiplying.
	koboBalance := model.Amount(int64(balance))
	if err := h.bankRepo.UpdateBalance(r.Context(), bank.ID, koboBalance); err != nil {
		h.logger.Error("update bank balance failed", "error", err)
		respondError(w, http.StatusInternalServerError, "update_failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "refreshed",
		"last_balance": koboBalance,
	})
}

// Compile-time check
var _ = errors.New
