package model

import (
	"time"

	"github.com/google/uuid"
)

type ChatMessage struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Role      string    `json:"role"`    // "user" | "assistant"
	Content   string    `json:"content"`
	IsError   bool      `json:"is_error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
