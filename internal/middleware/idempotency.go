package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type IDKeyStore interface {
	Get(ctx context.Context, namespace, key string) (*IDKeyResponse, bool, error)
	Set(ctx context.Context, namespace, key string, resp *IDKeyResponse) error
}

type IDKeyResponse struct {
	Status int
	Body   []byte
}

func Idempotency(store IDKeyStore, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			if len(key) > 255 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "idempotency_key_too_long"})
				return
			}

			namespace := r.URL.Path
			ctx := context.WithValue(r.Context(), IdempotencyKey, key)

			if existing, found, _ := store.Get(ctx, namespace, key); found {
				logger.Debug("idempotency cache hit", "key", key)
				w.Header().Set("Idempotency-Replayed", "true")
				w.WriteHeader(existing.Status)
				w.Write(existing.Body)
				return
			}

			rw := &idempotencyResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r.WithContext(ctx))

			if rw.status >= 200 && rw.status < 300 {
				resp := &IDKeyResponse{Status: rw.status, Body: rw.body}
				if err := store.Set(ctx, namespace, key, resp); err != nil {
					logger.Error("failed to cache idempotency key", "error", err)
				}
				w.Header().Set("Idempotency-Key", key)
			}
		})
	}
}

type idempotencyResponseWriter struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (rw *idempotencyResponseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *idempotencyResponseWriter) Write(b []byte) (int, error) {
	rw.body = append(rw.body, b...)
	return rw.ResponseWriter.Write(b)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

type InMemoryIDKeyStore struct {
	mu   sync.RWMutex
	data map[string]*IDKeyResponse
	ttl  time.Duration
}

func NewInMemoryIDKeyStore(ttl time.Duration) *InMemoryIDKeyStore {
	return &InMemoryIDKeyStore{
		data: make(map[string]*IDKeyResponse),
		ttl:  ttl,
	}
}

func (s *InMemoryIDKeyStore) Get(_ context.Context, namespace, key string) (*IDKeyResponse, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resp, ok := s.data[namespace+":"+key]
	if !ok {
		return nil, false, nil
	}
	return resp, true, nil
}

func (s *InMemoryIDKeyStore) Set(_ context.Context, namespace, key string, resp *IDKeyResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[namespace+":"+key] = resp
	return nil
}
