package model

import (
	"time"

	"github.com/google/uuid"
)

type TransactionType string

const (
	TxnTypeDebitLeg  TransactionType = "debit_leg"
	TxnTypePayoutLeg TransactionType = "payout_leg"
	TxnTypeDeposit   TransactionType = "deposit"
	TxnTypeFee       TransactionType = "fee"
	TxnTypeRefund    TransactionType = "refund"
	TxnTypeHold      TransactionType = "hold"
	TxnTypeRelease   TransactionType = "release"
)

type TransactionStatus string

const (
	TxnStatusPending    TransactionStatus = "PENDING"
	TxnStatusProcessing TransactionStatus = "PROCESSING"
	TxnStatusCompleted  TransactionStatus = "COMPLETED"
	TxnStatusFailed     TransactionStatus = "FAILED"
	TxnStatusRefunded   TransactionStatus = "REFUNDED"
)

type Transaction struct {
	ID                  uuid.UUID         `json:"id"`
	UserID              uuid.UUID         `json:"user_id"`
	ParentID            *uuid.UUID        `json:"parent_id,omitempty"` // NULL for parent transaction
	Type                TransactionType   `json:"type"`
	Amount              Amount            `json:"amount"`
	Fee                 Amount            `json:"fee"`
	Currency            string            `json:"currency"`
	Status              TransactionStatus `json:"status"`
	SourceAccountID     *uuid.UUID        `json:"source_account_id,omitempty"`
	SourceProvider      string            `json:"source_provider,omitempty"` // okra, mono, wallet
	RecipientAccount    string            `json:"recipient_account,omitempty"`
	RecipientBankCode   string            `json:"recipient_bank_code,omitempty"`
	RecipientName       string            `json:"recipient_name,omitempty"`
	ProviderRef         string            `json:"provider_ref,omitempty"` // external reference (Paystack, Okra)
	OurRef              string            `json:"our_ref"`                // Weave reference
	IdempotencyKey      string            `json:"idempotency_key,omitempty"`
	FailureReason       string            `json:"failure_reason,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty"`
}

type CreateTransactionInput struct {
	UserID            uuid.UUID
	ParentID          *uuid.UUID
	Type              TransactionType
	Amount            Amount
	Fee               Amount
	Currency          string
	SourceAccountID   *uuid.UUID
	SourceProvider    string
	RecipientAccount  string
	RecipientBankCode string
	RecipientName     string
	OurRef            string
	IdempotencyKey    string
}

// BankAccount is a user's linked bank account (via Okra/Mono)
type BankAccount struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"user_id"`
	Provider         string    `json:"provider"`     // okra, mono
	ProviderToken    string    `json:"-"`            // encrypted, never returned
	AccountNumber    string    `json:"account_number"`
	AccountName      string    `json:"account_name"`
	BankCode         string    `json:"bank_code"`
	BankName         string    `json:"bank_name"`
	Priority         int       `json:"priority"`     // 1=highest
	MinBalance       Amount    `json:"min_balance"`  // cushion to preserve
	LastBalance      Amount    `json:"last_balance,omitempty"`
	IsActive         bool      `json:"is_active"`
	IsVerified       bool      `json:"is_verified"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type DebitPlan struct {
	Legs   []DebitLeg `json:"legs"`
	Total  Amount     `json:"total"`
	Fees   Amount     `json:"fees"`
}

type DebitLeg struct {
	Source string `json:"source"` // bank_account_id or "wallet"
	Amount Amount `json:"amount"`
	Fee    Amount `json:"fee"`
	BankName string `json:"bank_name,omitempty"`
}
