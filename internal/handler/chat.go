package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/abdulsalamcodes/weave-server/internal/middleware"
)

type ChatHandler struct {
	logger *slog.Logger
}

func NewChatHandler(logger *slog.Logger) *ChatHandler {
	return &ChatHandler{logger: logger}
}

func (h *ChatHandler) RegisterRoutes(r chi.Router) {
	r.Post("/chat/message", h.SendMessage)
	r.Get("/chat/history", h.GetHistory)
}

type chatRequest struct {
	Message string `json:"message"`
}

func (h *ChatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request_body")
		return
	}

	// Parse message with LLM (to be implemented)
	// For now, echo a placeholder
	h.logger.Info("chat message received",
		"user_id", userID,
		"message", req.Message,
	)

	respondJSON(w, http.StatusOK, map[string]string{
		"response": "I received your message. LLM integration coming soon.",
		"intent":   "unknown",
	})
}

func (h *ChatHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, []interface{}{})
}
