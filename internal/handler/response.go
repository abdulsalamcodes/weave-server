package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
)

// Standard API error codes.
const (
	ErrCodeInvalidRequest       = "invalid_request"
	ErrCodeUnauthorized         = "unauthorized"
	ErrCodeForbidden            = "forbidden"
	ErrCodeNotFound             = "not_found"
	ErrCodeConflict             = "conflict"
	ErrCodeInternal             = "internal_error"
	ErrCodeValidation           = "validation_error"
	ErrCodeInsufficientFunds    = "insufficient_funds"
	ErrCodeTransferFailed       = "transfer_failed"
	ErrCodeDuplicateTransfer    = "duplicate_transfer"
	ErrCodeRateLimited          = "rate_limited"
	ErrCodeInvalidSignature     = "invalid_signature"
	ErrCodeInvalidBody          = "invalid_body"
	ErrCodeUnsupportedMediaType = "unsupported_media_type"
)

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type apiResponse struct {
	Error *APIError `json:"error,omitempty"`
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func respondError(w http.ResponseWriter, status int, code string) {
	respondErrorMsg(w, status, code, "")
}

func respondErrorMsg(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiResponse{Error: &APIError{Code: code, Message: message}})
}

func parsePagination(r *http.Request, defaultPage, defaultPerPage int) (page, perPage int) {
	page = defaultPage
	perPage = defaultPerPage

	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	if pp := r.URL.Query().Get("per_page"); pp != "" {
		if v, err := strconv.Atoi(pp); err == nil && v > 0 {
			perPage = v
		}
	}
	perPage = int(math.Min(float64(perPage), 100))
	return
}
