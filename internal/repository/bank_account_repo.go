package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type BankAccountRepo struct {
	pool *pgxpool.Pool
}

func NewBankAccountRepo(pool *pgxpool.Pool) *BankAccountRepo {
	return &BankAccountRepo{pool: pool}
}

func (r *BankAccountRepo) Create(ctx context.Context, ba *model.BankAccount) error {
	err := getQuerier(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO bank_accounts (user_id, provider, provider_token, account_number,
		                           account_name, bank_code, bank_name, priority, min_balance, last_balance,
		                           is_active, is_verified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (user_id, account_number) DO UPDATE
		  SET last_balance    = EXCLUDED.last_balance,
		      provider_token  = EXCLUDED.provider_token,
		      is_active       = EXCLUDED.is_active,
		      is_verified     = EXCLUDED.is_verified,
		      updated_at      = NOW()
		RETURNING id, created_at, updated_at
	`, ba.UserID, ba.Provider, ba.ProviderToken, ba.AccountNumber,
		ba.AccountName, ba.BankCode, ba.BankName, ba.Priority, ba.MinBalance, ba.LastBalance,
		ba.IsActive, ba.IsVerified,
	).Scan(&ba.ID, &ba.CreatedAt, &ba.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create bank account: %w", err)
	}
	return nil
}

func (r *BankAccountRepo) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.BankAccount, error) {
	rows, err := getQuerier(ctx, r.pool).Query(ctx, `
		SELECT id, user_id, provider, account_number, account_name,
		       bank_code, bank_name, priority, min_balance,
		       COALESCE(last_balance, 0), is_active, is_verified,
		       created_at, updated_at
		FROM bank_accounts WHERE user_id = $1
		ORDER BY priority ASC, created_at ASC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get bank accounts: %w", err)
	}
	defer rows.Close()

	var accounts []model.BankAccount
	for rows.Next() {
		var a model.BankAccount
		err := rows.Scan(
			&a.ID, &a.UserID, &a.Provider, &a.AccountNumber, &a.AccountName,
			&a.BankCode, &a.BankName, &a.Priority, &a.MinBalance,
			&a.LastBalance, &a.IsActive, &a.IsVerified,
			&a.CreatedAt, &a.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan bank account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

func (r *BankAccountRepo) GetByID(ctx context.Context, id uuid.UUID) (*model.BankAccount, error) {
	a := &model.BankAccount{}
	err := 	getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT id, user_id, provider, COALESCE(provider_token,''), account_number, account_name,
		       bank_code, bank_name, priority, min_balance,
		       COALESCE(last_balance, 0), is_active, is_verified,
		       created_at, updated_at
		FROM bank_accounts WHERE id = $1
	`, id).Scan(
		&a.ID, &a.UserID, &a.Provider, &a.ProviderToken, &a.AccountNumber, &a.AccountName,
		&a.BankCode, &a.BankName, &a.Priority, &a.MinBalance,
		&a.LastBalance, &a.IsActive, &a.IsVerified,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bank account: %w", err)
	}
	return a, nil
}

func (r *BankAccountRepo) UpdatePriority(ctx context.Context, id uuid.UUID, priority int) error {
	_, err := 	getQuerier(ctx, r.pool).Exec(ctx, `
		UPDATE bank_accounts SET priority = $2, updated_at = NOW() WHERE id = $1
	`, id, priority)
	if err != nil {
		return fmt.Errorf("update bank priority: %w", err)
	}
	return nil
}

func (r *BankAccountRepo) UpdateBalance(ctx context.Context, id uuid.UUID, balance model.Amount) error {
	_, err := 	getQuerier(ctx, r.pool).Exec(ctx, `
		UPDATE bank_accounts SET last_balance = $2, last_balance_fetched_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, id, balance)
	if err != nil {
		return fmt.Errorf("update bank balance: %w", err)
	}
	return nil
}

func (r *BankAccountRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := 	getQuerier(ctx, r.pool).Exec(ctx, `DELETE FROM bank_accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete bank account: %w", err)
	}
	return nil
}
