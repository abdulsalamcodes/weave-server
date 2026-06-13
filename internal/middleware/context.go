package middleware

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey string

const (
	UserIDKey      ctxKey = "user_id"
	RequestIDKey   ctxKey = "request_id"
	IdempotencyKey ctxKey = "idempotency_key"
)

func GetUserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(UserIDKey).(uuid.UUID)
	return id, ok
}

func GetRequestID(ctx context.Context) string {
	id, _ := ctx.Value(RequestIDKey).(string)
	return id
}

func GetIdempotencyKey(ctx context.Context) string {
	key, _ := ctx.Value(IdempotencyKey).(string)
	return key
}

func contextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, RequestIDKey, id)
}
