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
	"github.com/abdulsalamcodes/weave-server/internal/provider/okra"
	"github.com/abdulsalamcodes/weave-server/internal/repository"
)

type BankHandler struct {
	bankRepo   repository.BankAccountRepository
	userRepo   repository.UserRepository
	okraClient *okra.Client
	monoClient *mono.Client
	logger     *slog.Logger
}

func NewBankHandler(
	bankRepo repository.BankAccountRepository,
	userRepo repository.UserRepository,
	okraClient *okra.Client,
	monoClient *mono.Client,
	logger *slog.Logger,
) *BankHandler {
	return &BankHandler{
		bankRepo:   bankRepo,
		userRepo:   userRepo,
		okraClient: okraClient,
		monoClient: monoClient,
		logger:     logger,
	}
}

func (h *BankHandler) RegisterRoutes(r chi.Router) {
	r.Post("/banks/link", h.InitiateLink)
	r.Post("/banks/webhook/okra", h.OkraWebhook)
	r.Post("/banks/webhook/mono", h.MonoWebhook)
	r.Get("/banks", h.ListBanks)
	r.Put("/banks/{id}/priority", h.UpdatePriority)
	r.Delete("/banks/{id}", h.UnlinkBank)
	r.Post("/banks/{id}/refresh", h.RefreshBalance)
}

type initiateLinkRequest struct {
	Provider string `json:"provider"` // "okra" or "mono"
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
	case "okra":
		if h.okraClient == nil {
			respondError(w, http.StatusServiceUnavailable, "okra_not_configured")
			return
		}
		result, err := h.okraClient.GenerateConnectURL(r.Context(), &okra.ConnectRequest{
			CustomerID:  userID.String(),
			FirstName:   user.FullName,
			Phone:       user.Phone,
			CallbackURL: "",
		})
		if err != nil {
			h.logger.Error("okra connect failed", "error", err)
			respondError(w, http.StatusInternalServerError, "connect_failed")
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"connect_url": result.Data.ConnectURL,
			"reference":   result.Data.Reference,
			"provider":    "okra",
		})

	case "mono":
		if h.monoClient == nil {
			respondError(w, http.StatusServiceUnavailable, "mono_not_configured")
			return
		}
		result, err := h.monoClient.GenerateConnectURL(r.Context(), userID.String(), user.FullName, user.Email)
		if err != nil {
			h.logger.Error("mono connect failed", "error", err)
			respondError(w, http.StatusInternalServerError, "connect_failed")
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"connect_url": result.Data.ConnectURL,
			"reference":   result.Data.Reference,
			"provider":    "mono",
		})

	default:
		respondError(w, http.StatusBadRequest, "invalid_provider")
	}
}

type okraWebhookPayload struct {
	Event   string `json:"event"`
	Account struct {
		ID            string  `json:"id"`
		AccountNumber string  `json:"account_number"`
		AccountName   string  `json:"account_name"`
		BankName      string  `json:"bank_name"`
		BankCode      string  `json:"bank_code"`
		Balance       float64 `json:"balance"`
		AccessToken   string  `json:"access_token"`
	} `json:"account"`
	Reference string `json:"reference"`
}

func (h *BankHandler) OkraWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	if h.okraClient != nil {
		sig := r.Header.Get("X-Okra-Signature")
		if sig == "" || !h.okraClient.VerifyWebhook(sig, body) {
			h.logger.Warn("invalid okra webhook signature")
			respondError(w, http.StatusUnauthorized, "invalid_signature")
			return
		}
	}

	var payload okraWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_payload")
		return
	}

	if !okra.IsSupportedEvent(payload.Event) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	bank := &model.BankAccount{
		Provider:       "okra",
		ProviderToken:  payload.Account.AccessToken,
		AccountNumber:  payload.Account.AccountNumber,
		AccountName:    payload.Account.AccountName,
		BankCode:       payload.Account.BankCode,
		BankName:       payload.Account.BankName,
		LastBalance:    model.NewAmount(int64(payload.Account.Balance)),
		Priority:       5,
		IsActive:       true,
		IsVerified:     true,
	}

	if err := h.bankRepo.Create(r.Context(), bank); err != nil {
		h.logger.Error("save okra account failed", "error", err)
		respondError(w, http.StatusInternalServerError, "save_failed")
		return
	}

	h.logger.Info("bank linked via okra",
		"account", bank.AccountNumber,
		"bank", bank.BankName,
	)

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

type monoWebhookPayload struct {
	Event   string `json:"event"`
	Data    struct {
		ID            string  `json:"id"`
		AccountNumber string  `json:"accountNumber"`
		AccountName   string  `json:"accountName"`
		BankName      string  `json:"bankName"`
		BankCode      string  `json:"bankCode"`
		Balance       float64 `json:"balance"`
		Reference     string  `json:"reference"`
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

	bank := &model.BankAccount{
		Provider:       "mono",
		ProviderToken:  payload.Data.ID,
		AccountNumber:  payload.Data.AccountNumber,
		AccountName:    payload.Data.AccountName,
		BankCode:       payload.Data.BankCode,
		BankName:       payload.Data.BankName,
		LastBalance:    model.NewAmount(int64(payload.Data.Balance)),
		Priority:       5,
		IsActive:       true,
		IsVerified:     true,
	}

	if err := h.bankRepo.Create(r.Context(), bank); err != nil {
		h.logger.Error("save mono account failed", "error", err)
		respondError(w, http.StatusInternalServerError, "save_failed")
		return
	}

	h.logger.Info("bank linked via mono",
		"account", bank.AccountNumber,
		"bank", bank.BankName,
	)

	respondJSON(w, http.StatusOK, map[string]string{"status": "success"})
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
		respondError(w, http.StatusBadRequest, "no_provider_token")
		return
	}

	var balance float64
	switch bank.Provider {
	case "mono":
		if h.monoClient == nil {
			respondError(w, http.StatusServiceUnavailable, "mono_not_configured")
			return
		}
		resp, err := h.monoClient.GetBalance(r.Context(), bank.ProviderToken)
		if err != nil {
			h.logger.Error("mono balance refresh failed", "error", err, "account_id", bank.ID)
			respondError(w, http.StatusInternalServerError, "balance_refresh_failed")
			return
		}
		balance = resp.Data.Balance

	case "okra":
		if h.okraClient == nil {
			respondError(w, http.StatusServiceUnavailable, "okra_not_configured")
			return
		}
		resp, err := h.okraClient.GetBalance(r.Context(), bank.ProviderToken)
		if err != nil {
			h.logger.Error("okra balance refresh failed", "error", err, "account_id", bank.ID)
			respondError(w, http.StatusInternalServerError, "balance_refresh_failed")
			return
		}
		balance = resp.Data.Balance

	default:
		respondError(w, http.StatusBadRequest, "unsupported_provider")
		return
	}

	if err := h.bankRepo.UpdateBalance(r.Context(), bank.ID, model.NewAmount(int64(balance))); err != nil {
		h.logger.Error("update bank balance failed", "error", err)
		respondError(w, http.StatusInternalServerError, "update_failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "refreshed",
		"last_balance": model.NewAmount(int64(balance)),
	})
}

// Compile-time check
var _ = errors.New
