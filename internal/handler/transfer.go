package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/model"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

type TransferHandler struct {
	transferService *service.TransferService
	logger          *slog.Logger
}

func NewTransferHandler(transferService *service.TransferService, logger *slog.Logger) *TransferHandler {
	return &TransferHandler{transferService: transferService, logger: logger}
}

func (h *TransferHandler) RegisterRoutes(r chi.Router) {
	r.Post("/transfers", h.InitiateTransfer)
	r.Get("/transfers/{id}", h.GetTransfer)
	r.Get("/transfers/ref/{ref}", h.GetTransferByRef)
}

type initiateTransferRequest struct {
	Amount           float64 `json:"amount" validate:"required,gt=0"`
	RecipientAccount string  `json:"recipient_account" validate:"required,account_number"`
	RecipientBank    string  `json:"recipient_bank" validate:"required,bank_code"`
	RecipientName    string  `json:"recipient_name" validate:"required,min=2,max=255"`
	Narration        string  `json:"narration,omitempty"`
}

func (h *TransferHandler) InitiateTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req initiateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	amount := model.NewAmount(int64(req.Amount))
	if amount.IsZero() || amount.IsNegative() {
		respondError(w, http.StatusBadRequest, "invalid_amount")
		return
	}

	idempotencyKey := middleware.GetIdempotencyKey(r.Context())

	result, err := h.transferService.InitiateTransfer(r.Context(), userID, service.TransferRequest{
		Amount:           amount,
		RecipientAccount: req.RecipientAccount,
		RecipientBank:    req.RecipientBank,
		RecipientName:    req.RecipientName,
		IdempotencyKey:   idempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrDuplicateTransfer):
			respondError(w, http.StatusConflict, "duplicate_transfer")
		case errors.Is(err, service.ErrInsufficientFunds):
			respondError(w, http.StatusBadRequest, "insufficient_funds")
		default:
			h.logger.Error("transfer failed", "error", err)
			respondError(w, http.StatusInternalServerError, "transfer_failed")
		}
		return
	}

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"transaction_id": result.TransactionID,
		"our_ref":        result.OurRef,
		"status":         result.Status,
		"debit_plan":     result.DebitPlan,
	})
}

func (h *TransferHandler) GetTransfer(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_transaction_id")
		return
	}

	txn, err := h.transferService.GetTransfer(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrTransferNotFound) {
			respondError(w, http.StatusNotFound, "transfer_not_found")
			return
		}
		h.logger.Error("get transfer failed", "error", err)
		respondError(w, http.StatusInternalServerError, "get_transfer_failed")
		return
	}

	respondJSON(w, http.StatusOK, txn)
}

func (h *TransferHandler) GetTransferByRef(w http.ResponseWriter, r *http.Request) {
	ref := chi.URLParam(r, "ref")
	txn, err := h.transferService.GetTransferByRef(r.Context(), ref)
	if err != nil {
		if errors.Is(err, service.ErrTransferNotFound) {
			respondError(w, http.StatusNotFound, "transfer_not_found")
			return
		}
		h.logger.Error("get transfer by ref failed", "error", err)
		respondError(w, http.StatusInternalServerError, "get_transfer_failed")
		return
	}

	respondJSON(w, http.StatusOK, txn)
}
