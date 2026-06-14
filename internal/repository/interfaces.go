package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type UserRepository interface {
	Create(ctx context.Context, input model.CreateUserInput) (*model.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (*model.User, error)
	GetByPhone(ctx context.Context, phone string) (*model.User, error)
	RecordPINAttempt(ctx context.Context, userID uuid.UUID, success bool, ip string) error
	RecentFailedPINAttempts(ctx context.Context, userID uuid.UUID, within time.Duration) (int, error)
	RecentFailedPINAttemptsByIP(ctx context.Context, ip string, within time.Duration) (int, error)
	UpdatePIN(ctx context.Context, userID uuid.UUID, pinHash string) error
	UpdateKYC(ctx context.Context, userID uuid.UUID, level model.KYCLevel, bvnHash, ninHash string) error
}

type WalletRepository interface {
	Create(ctx context.Context, wallet *model.Wallet) error
	GetByUserID(ctx context.Context, userID uuid.UUID) (*model.Wallet, error)
	GetByID(ctx context.Context, id uuid.UUID) (*model.Wallet, error)
	Credit(ctx context.Context, walletID uuid.UUID, amount model.Amount) error
	Debit(ctx context.Context, walletID uuid.UUID, amount model.Amount) error
	Hold(ctx context.Context, walletID uuid.UUID, amount model.Amount) error
	ReleaseHold(ctx context.Context, walletID uuid.UUID, amount model.Amount) error
	RecordTransaction(ctx context.Context, wt *model.WalletTransaction) error
	CreateWalletAccount(ctx context.Context, wa *model.WalletAccount) error
	GetWalletAccountByUserID(ctx context.Context, userID uuid.UUID) (*model.WalletAccount, error)
	GetWalletAccountByNumber(ctx context.Context, number string) (*model.WalletAccount, error)
	RecordDeposit(ctx context.Context, deposit *model.WalletDeposit) error
}

type TransactionRepository interface {
	Create(ctx context.Context, input model.CreateTransactionInput) (*model.Transaction, error)
	GetByID(ctx context.Context, id uuid.UUID) (*model.Transaction, error)
	GetByOurRef(ctx context.Context, ourRef string) (*model.Transaction, error)
	GetByParentID(ctx context.Context, parentID uuid.UUID) ([]model.Transaction, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status model.TransactionStatus, failureReason string) error
	UpdateProviderRef(ctx context.Context, id uuid.UUID, providerRef string) error
	GetByIdempotencyKey(ctx context.Context, key string) (*model.Transaction, error)
}

type BankAccountRepository interface {
	Create(ctx context.Context, ba *model.BankAccount) error
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.BankAccount, error)
	GetByID(ctx context.Context, id uuid.UUID) (*model.BankAccount, error)
	UpdatePriority(ctx context.Context, id uuid.UUID, priority int) error
	UpdateBalance(ctx context.Context, id uuid.UUID, balance model.Amount) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// Compile-time checks that concrete types implement interfaces
var _ UserRepository = (*UserRepo)(nil)
var _ WalletRepository = (*WalletRepo)(nil)
var _ TransactionRepository = (*TransactionRepo)(nil)
var _ BankAccountRepository = (*BankAccountRepo)(nil)
