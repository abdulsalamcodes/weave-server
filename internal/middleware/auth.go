package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type AuthConfig struct {
	JWTSecret     string
	BypassPaths   []string
}

func Auth(cfg AuthConfig, logger *slog.Logger) func(http.Handler) http.Handler {
	bypass := make(map[string]bool)
	for _, p := range cfg.BypassPaths {
		bypass[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for bypass paths
			if bypass[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Also skip auth for GET/OPTIONS health and docs
			if (r.Method == http.MethodGet || r.Method == http.MethodOptions) &&
				(strings.HasPrefix(r.URL.Path, "/health") || strings.HasPrefix(r.URL.Path, "/api/v1/auth")) {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_authorization"})
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenStr == authHeader {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_auth_format"})
				return
			}

			claims := &jwtClaims{}
			token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(cfg.JWTSecret), nil
			})

			if err != nil || !token.Valid {
				logger.Warn("invalid jwt", "error", err)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_or_expired_token"})
				return
			}

			userID, err := uuid.Parse(claims.Subject)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token_subject"})
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type jwtClaims struct {
	jwt.RegisteredClaims
	Scope string `json:"scope,omitempty"`
}

func GenerateAccessToken(secret string, userID uuid.UUID, ttl time.Duration) (string, error) {
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		Scope: "access",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func GenerateRefreshToken(secret string, userID uuid.UUID, ttl time.Duration) (string, error) {
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		Scope: "refresh",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Token bucket rate limiter

type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int
	window   time.Duration
}

type bucket struct {
	tokens    int
	last      time.Time
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		window:  window,
	}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, exists := rl.buckets[key]
	now := time.Now()

	if !exists {
		rl.buckets[key] = &bucket{tokens: rl.rate - 1, last: now}
		return true
	}

	elapsed := now.Sub(b.last)
	b.last = now
	b.tokens += int(elapsed / rl.window * time.Duration(rl.rate))
	if b.tokens > rl.rate {
		b.tokens = rl.rate
	}

	if b.tokens <= 0 {
		return false
	}

	b.tokens--
	return true
}

func RateLimit(rl *RateLimiter, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.RemoteAddr
			if userID, ok := GetUserID(r.Context()); ok {
				key = userID.String()
			}

			if !rl.Allow(key) {
				logger.Warn("rate limit exceeded", "key", key, "path", r.URL.Path)
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limit_exceeded"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
