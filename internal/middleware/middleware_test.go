package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	logger := newTestLogger()
	handler := Auth(AuthConfig{JWTSecret: "test-secret"}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidFormat(t *testing.T) {
	logger := newTestLogger()
	handler := Auth(AuthConfig{JWTSecret: "test-secret"}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
	req.Header.Set("Authorization", "NotBearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	logger := newTestLogger()
	secret := "test-secret"
	handler := Auth(AuthConfig{JWTSecret: secret}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	expiredToken, err := GenerateAccessToken(secret, uuid.New(), -1*time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	logger := newTestLogger()
	secret := "test-secret"
	userID := uuid.New()

	handler := Auth(AuthConfig{JWTSecret: secret}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := GetUserID(r.Context())
		if !ok {
			t.Error("expected user ID in context")
		}
		if uid != userID {
			t.Errorf("expected user ID %s, got %s", userID, uid)
		}
		w.WriteHeader(http.StatusOK)
	}))

	token, err := GenerateAccessToken(secret, userID, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_BypassPaths(t *testing.T) {
	logger := newTestLogger()
	handler := Auth(AuthConfig{
		JWTSecret:   "test-secret",
		BypassPaths: []string{"/api/v1/auth/register"},
	}, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("bypass path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for bypass path, got %d", rec.Code)
		}
	})

	t.Run("health path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for health path, got %d", rec.Code)
		}
	})

	t.Run("auth path GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for auth GET, got %d", rec.Code)
		}
	})

	t.Run("non-bypass path without token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for protected path, got %d", rec.Code)
		}
	})
}

func TestGenerateTokens(t *testing.T) {
	secret := "test-secret"
	userID := uuid.New()

	t.Run("access token", func(t *testing.T) {
		token, err := GenerateAccessToken(secret, userID, 15*time.Minute)
		if err != nil {
			t.Fatalf("failed: %v", err)
		}
		if token == "" {
			t.Fatal("expected non-empty token")
		}
	})

	t.Run("refresh token", func(t *testing.T) {
		token, err := GenerateRefreshToken(secret, userID, 7*24*time.Hour)
		if err != nil {
			t.Fatalf("failed: %v", err)
		}
		if token == "" {
			t.Fatal("expected non-empty token")
		}
	})
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)

	t.Run("allows within limit", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if !rl.Allow("test-key") {
				t.Errorf("request %d should be allowed", i+1)
			}
		}
	})

	t.Run("denies after limit", func(t *testing.T) {
		if rl.Allow("test-key") {
			t.Error("should be denied after exceeding limit")
		}
	})

	t.Run("separate keys are independent", func(t *testing.T) {
		if !rl.Allow("other-key") {
			t.Error("different key should be allowed")
		}
	})
}

func TestRateLimiter_WindowRefill(t *testing.T) {
	rl := NewRateLimiter(1, 50*time.Millisecond)
	if !rl.Allow("refill-key") {
		t.Fatal("first request should be allowed")
	}
	if rl.Allow("refill-key") {
		t.Fatal("second request should be denied")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow("refill-key") {
		t.Error("should be allowed after window passes")
	}
}

func TestIdempotencyMiddleware(t *testing.T) {
	store := NewInMemoryIDKeyStore(30 * time.Minute)
	logger := newTestLogger()

	var callCount int
	handler := Idempotency(store, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"status":"ok"}`))
	}))

	t.Run("first request with key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
		req.Header.Set("Idempotency-Key", "test-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d", rec.Code)
		}
		if rec.Header().Get("Idempotency-Replayed") == "true" {
			t.Error("first request should not be a replay")
		}
	})

	t.Run("second request with same key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
		req.Header.Set("Idempotency-Key", "test-key")
		rec := httptest.NewRecorder()
		callCountBefore := callCount
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d", rec.Code)
		}
		if rec.Header().Get("Idempotency-Replayed") != "true" {
			t.Error("second request should be a replay")
		}
		if callCount != callCountBefore {
			t.Error("handler should not have been called again")
		}
	})

	t.Run("no key passes through", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d", rec.Code)
		}
	})

	t.Run("GET requests bypass", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/transfer/123", nil)
		req.Header.Set("Idempotency-Key", "test-key")
		rec := httptest.NewRecorder()
		callCountBefore := callCount
		handler.ServeHTTP(rec, req)
		if callCount == callCountBefore {
			t.Error("GET request should reach handler even with idempotency key")
		}
	})

	t.Run("key too long", func(t *testing.T) {
		longKey := make([]byte, 300)
		for i := range longKey {
			longKey[i] = 'a'
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/transfer", nil)
		req.Header.Set("Idempotency-Key", string(longKey))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for long key, got %d", rec.Code)
		}
	})
}

func TestInMemoryIDKeyStore(t *testing.T) {
	store := NewInMemoryIDKeyStore(5 * time.Minute)
	resp := &IDKeyResponse{Status: 200, Body: []byte(`{"ok":true}`)}

	err := store.Set(context.Background(), "/test", "key1", resp)
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}

	got, found, err := store.Get(context.Background(), "/test", "key1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if got.Status != 200 {
		t.Errorf("expected status 200, got %d", got.Status)
	}

	_, found, _ = store.Get(context.Background(), "/test", "nonexistent")
	if found {
		t.Fatal("expected nonexistent key to not be found")
	}
}
