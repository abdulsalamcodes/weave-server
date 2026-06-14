package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
)

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func respondError(w http.ResponseWriter, status int, code string) {
	respondJSON(w, status, map[string]string{"error": code})
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
