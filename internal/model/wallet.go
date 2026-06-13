package model

import (
	"time"

	"github.com/google/uuid"
)

type Amount int64 // kobo (1 NGN = 100 kobo)

func NewAmount(ngn int64) Amount { return Amount(ngn * 100) }

func (a Amount) Kobo() int64    { return int64(a) }
func (a Amount) NGN() float64   { return float64(a) / 100 }
func (a Amount) IsZero() bool   { return a == 0 }
func (a Amount) IsNegative() bool { return a < 0 }
func (a Amount) Add(b Amount) Amount    { return a + b }
func (a Amount) Sub(b Amount) Amount    { return a - b }
func (a Amount) Mul(n int64) Amount     { return a * Amount(n) }
func (a Amount) CanCover(b Amount) bool { return a >= b }

type WalletType string

const (
	WalletTypeUser       WalletType = "user"
	WalletTypeSettlement WalletType = "settlement"
	WalletTypeFee        WalletType = "fee"
)

type Wallet struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id,omitempty"` // nil for settlement/fee wallets
	Type          WalletType `json:"type"`
	Balance       Amount     `json:"balance"`
	LedgerBalance Amount     `json:"ledger_balance"` // balance - holds
	Currency      string     `json:"currency"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// WalletAccount is a real NUBAN account issued to user via Paystack DVA
type WalletAccount struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	Provider      string    `json:"provider"` // paystack, flutterwave, mono
	ProviderRef   string    `json:"provider_ref"`
	AccountNumber string    `json:"account_number"`
	AccountName   string    `json:"account_name"`
	BankName      string    `json:"bank_name"`
	BankCode      string    `json:"bank_code"`
	IsActive      bool      `json:"is_active"`
	IsDefault     bool      `json:"is_default"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type WalletTransaction struct {
	ID          uuid.UUID `json:"id"`
	WalletID    uuid.UUID `json:"wallet_id"`
	Type        string    `json:"type"` // credit, debit, hold, release
	Amount      Amount    `json:"amount"`
	Reference   string    `json:"reference"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type WalletDeposit struct {
	ID              uuid.UUID `json:"id"`
	WalletAccountID uuid.UUID `json:"wallet_account_id"`
	UserID          uuid.UUID `json:"user_id"`
	Amount          Amount    `json:"amount"`
	Fee             Amount    `json:"fee"`
	Provider        string    `json:"provider"`
	ProviderRef     string    `json:"provider_ref"`
	SenderAccount   string    `json:"sender_account,omitempty"`
	SenderBank      string    `json:"sender_bank,omitempty"`
	Status          string    `json:"status"`
	Reconciled      bool      `json:"reconciled"`
	CreatedAt       time.Time `json:"created_at"`
}
