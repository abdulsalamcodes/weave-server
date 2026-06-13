package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type WalletRepo struct {
	pool *pgxpool.Pool
}

func NewWalletRepo(pool *pgxpool.Pool) *WalletRepo {
	return &WalletRepo{pool: pool}
}

func (r *WalletRepo) Create(ctx context.Context, wallet *model.Wallet) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO wallets (user_id, type, balance, ledger_balance, currency)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`, wallet.UserID, wallet.Type, wallet.Balance, wallet.LedgerBalance, wallet.Currency).Scan(
		&wallet.ID, &wallet.CreatedAt, &wallet.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create wallet: %w", err)
	}
	return nil
}

func (r *WalletRepo) GetByUserID(ctx context.Context, userID uuid.UUID) (*model.Wallet, error) {
	wallet := &model.Wallet{}
	err := r.pool.QueryRow(ctx, `
		SELECT id, COALESCE(user_id, '00000000-0000-0000-0000-000000000000'), type,
		       balance, ledger_balance, currency, created_at, updated_at
		FROM wallets WHERE user_id = $1 AND type = 'user'
	`, userID).Scan(
		&wallet.ID, &wallet.UserID, &wallet.Type,
		&wallet.Balance, &wallet.LedgerBalance, &wallet.Currency,
		&wallet.CreatedAt, &wallet.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet by user: %w", err)
	}
	return wallet, nil
}

func (r *WalletRepo) GetByID(ctx context.Context, id uuid.UUID) (*model.Wallet, error) {
	wallet := &model.Wallet{}
	err := r.pool.QueryRow(ctx, `
		SELECT id, COALESCE(user_id, '00000000-0000-0000-0000-000000000000'), type,
		       balance, ledger_balance, currency, created_at, updated_at
		FROM wallets WHERE id = $1
	`, id).Scan(
		&wallet.ID, &wallet.UserID, &wallet.Type,
		&wallet.Balance, &wallet.LedgerBalance, &wallet.Currency,
		&wallet.CreatedAt, &wallet.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return wallet, nil
}

func (r *WalletRepo) Credit(ctx context.Context, walletID uuid.UUID, amount model.Amount) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE wallets SET balance = balance + $2, ledger_balance = ledger_balance + $2, updated_at = NOW()
		WHERE id = $1
	`, walletID, amount)
	if err != nil {
		return fmt.Errorf("credit wallet: %w", err)
	}
	return nil
}

func (r *WalletRepo) Debit(ctx context.Context, walletID uuid.UUID, amount model.Amount) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE wallets SET balance = balance - $2, ledger_balance = ledger_balance - $2, updated_at = NOW()
		WHERE id = $1 AND balance >= $2 AND ledger_balance >= $2
	`, walletID, amount)
	if err != nil {
		return fmt.Errorf("debit wallet: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("insufficient wallet balance")
	}
	return nil
}

func (r *WalletRepo) Hold(ctx context.Context, walletID uuid.UUID, amount model.Amount) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE wallets SET ledger_balance = ledger_balance - $2, updated_at = NOW()
		WHERE id = $1 AND ledger_balance >= $2
	`, walletID, amount)
	if err != nil {
		return fmt.Errorf("hold wallet: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("insufficient available balance for hold")
	}
	return nil
}

func (r *WalletRepo) ReleaseHold(ctx context.Context, walletID uuid.UUID, amount model.Amount) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE wallets SET ledger_balance = ledger_balance + $2, updated_at = NOW()
		WHERE id = $1
	`, walletID, amount)
	if err != nil {
		return fmt.Errorf("release hold: %w", err)
	}
	return nil
}

func (r *WalletRepo) RecordTransaction(ctx context.Context, wt *model.WalletTransaction) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO wallet_transactions (wallet_id, type, amount, reference, description, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, wt.WalletID, wt.Type, wt.Amount, wt.Reference, wt.Description, wt.Status).Scan(
		&wt.ID, &wt.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record wallet transaction: %w", err)
	}
	return nil
}

// Wallet Account operations

func (r *WalletRepo) CreateWalletAccount(ctx context.Context, wa *model.WalletAccount) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO wallet_accounts (user_id, provider, provider_ref, account_number, account_name, bank_name, bank_code, is_default)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`, wa.UserID, wa.Provider, wa.ProviderRef, wa.AccountNumber, wa.AccountName, wa.BankName, wa.BankCode, wa.IsDefault).Scan(
		&wa.ID, &wa.CreatedAt, &wa.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create wallet account: %w", err)
	}
	return nil
}

func (r *WalletRepo) GetWalletAccountByUserID(ctx context.Context, userID uuid.UUID) (*model.WalletAccount, error) {
	wa := &model.WalletAccount{}
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, provider, provider_ref, account_number, account_name,
		       bank_name, bank_code, is_active, is_default, created_at, updated_at
		FROM wallet_accounts WHERE user_id = $1 AND is_active = true
		ORDER BY is_default DESC, created_at DESC LIMIT 1
	`, userID).Scan(
		&wa.ID, &wa.UserID, &wa.Provider, &wa.ProviderRef, &wa.AccountNumber, &wa.AccountName,
		&wa.BankName, &wa.BankCode, &wa.IsActive, &wa.IsDefault, &wa.CreatedAt, &wa.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet account: %w", err)
	}
	return wa, nil
}

func (r *WalletRepo) GetWalletAccountByNumber(ctx context.Context, number string) (*model.WalletAccount, error) {
	wa := &model.WalletAccount{}
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, provider, provider_ref, account_number, account_name,
		       bank_name, bank_code, is_active, is_default, created_at, updated_at
		FROM wallet_accounts WHERE account_number = $1 AND is_active = true
	`, number).Scan(
		&wa.ID, &wa.UserID, &wa.Provider, &wa.ProviderRef, &wa.AccountNumber, &wa.AccountName,
		&wa.BankName, &wa.BankCode, &wa.IsActive, &wa.IsDefault, &wa.CreatedAt, &wa.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet account by number: %w", err)
	}
	return wa, nil
}

func (r *WalletRepo) RecordDeposit(ctx context.Context, deposit *model.WalletDeposit) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO wallet_deposits (wallet_account_id, user_id, amount, fee, provider, provider_ref,
		                             sender_account, sender_bank, status, webhook_raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at
	`, deposit.WalletAccountID, deposit.UserID, deposit.Amount, deposit.Fee,
		deposit.Provider, deposit.ProviderRef, deposit.SenderAccount,
		deposit.SenderBank, deposit.Status, nil).Scan(
		&deposit.ID, &deposit.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record deposit: %w", err)
	}
	return nil
}
