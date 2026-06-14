package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						slog.Any("panic", rec),
						slog.String("stack", string(debug.Stack())),
						slog.String("request_id", GetRequestID(r.Context())),
					)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_server_error"})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
