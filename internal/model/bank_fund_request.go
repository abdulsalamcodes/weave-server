package model

import (
	"time"

	"github.com/google/uuid"
)

const (
	BankFundStatusPending   = "pending"
	BankFundStatusCompleted = "completed"
	BankFundStatusFailed    = "failed"
)

type BankFundRequest struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	BankAccountID uuid.UUID  `json:"bank_account_id"`
	Reference     string     `json:"reference"`
	Amount        Amount     `json:"amount"`
	Status        string     `json:"status"`
	ProviderRef   string     `json:"provider_ref,omitempty"`
	PaymentURL    string     `json:"payment_url,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}
