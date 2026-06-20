package model

import (
	"time"

	"github.com/google/uuid"
)

type AuditLog struct {
	ID        uuid.UUID      `json:"id"`
	UserID    *uuid.UUID     `json:"user_id,omitempty"`
	Action    string         `json:"action"`
	Status    string         `json:"status"` // "ok" | "failed"
	Metadata  map[string]any `json:"metadata,omitempty"`
	IPAddress string         `json:"ip_address,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
