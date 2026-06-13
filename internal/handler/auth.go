package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
	"github.com/abdulsalamcodes/weave-server/internal/service"
)

type AuthHandler struct {
	authService *service.AuthService
	logger      *slog.Logger
}

func NewAuthHandler(authService *service.AuthService, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{authService: authService, logger: logger}
}

func (h *AuthHandler) RegisterRoutes(r chi.Router) {
	r.Post("/auth/register", h.Register)
	r.Post("/auth/login", h.Login)
	r.Post("/auth/pin/verify", h.VerifyPIN)
	r.Post("/auth/token/refresh", h.RefreshToken)
}

type registerRequest struct {
	Phone    string `json:"phone" validate:"required,phone"`
	FullName string `json:"full_name" validate:"required,min=2,max=255"`
	PIN      string `json:"pin" validate:"required,len=6"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	session, err := h.authService.Register(r.Context(), req.Phone, req.FullName, req.PIN)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrDuplicatePhone):
			respondError(w, http.StatusConflict, "phone_already_registered")
		default:
			h.logger.Error("registration failed", "error", err)
			respondError(w, http.StatusInternalServerError, "registration_failed")
		}
		return
	}

	respondJSON(w, http.StatusCreated, session)
}

type loginRequest struct {
	Phone string `json:"phone" validate:"required,phone"`
	PIN   string `json:"pin" validate:"required,len=6"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	session, err := h.authService.Login(r.Context(), req.Phone, req.PIN)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotFound):
			respondError(w, http.StatusUnauthorized, "invalid_credentials")
		case errors.Is(err, service.ErrInvalidPIN):
			respondError(w, http.StatusUnauthorized, "invalid_credentials")
		case errors.Is(err, service.ErrPINLocked):
			respondError(w, http.StatusTooManyRequests, "pin_locked")
		default:
			h.logger.Error("login failed", "error", err)
			respondError(w, http.StatusInternalServerError, "login_failed")
		}
		return
	}

	respondJSON(w, http.StatusOK, session)
}

type verifyPINRequest struct {
	PIN string `json:"pin" validate:"required,len=6"`
}

func (h *AuthHandler) VerifyPIN(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req verifyPINRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	if err := h.authService.VerifyPIN(r.Context(), userID, req.PIN); err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidPIN):
			respondError(w, http.StatusUnauthorized, "invalid_pin")
		case errors.Is(err, service.ErrPINLocked):
			respondError(w, http.StatusTooManyRequests, "pin_locked")
		default:
			respondError(w, http.StatusInternalServerError, "verification_failed")
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "verified"})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	session, err := h.authService.RefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "invalid_refresh_token")
		return
	}

	respondJSON(w, http.StatusOK, session)
}
