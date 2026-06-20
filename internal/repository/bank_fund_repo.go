package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type BankFundRepo struct {
	pool *pgxpool.Pool
}

func NewBankFundRepo(pool *pgxpool.Pool) *BankFundRepo {
	return &BankFundRepo{pool: pool}
}

func (r *BankFundRepo) Create(ctx context.Context, req *model.BankFundRequest) error {
	return getQuerier(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO bank_fund_requests (user_id, bank_account_id, reference, amount, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`, req.UserID, req.BankAccountID, req.Reference, req.Amount.Kobo(), req.Status,
	).Scan(&req.ID, &req.CreatedAt)
}

func (r *BankFundRepo) GetByReference(ctx context.Context, ref string) (*model.BankFundRequest, error) {
	var req model.BankFundRequest
	var amountKobo int64
	var completedAt *time.Time

	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, bank_account_id, reference, amount, status,
		       COALESCE(provider_ref,''), COALESCE(failure_reason,''), created_at, completed_at
		FROM bank_fund_requests
		WHERE reference = $1
	`, ref).Scan(
		&req.ID, &req.UserID, &req.BankAccountID, &req.Reference,
		&amountKobo, &req.Status, &req.ProviderRef, &req.FailureReason,
		&req.CreatedAt, &completedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get fund request by ref: %w", err)
	}
	req.Amount = model.Amount(amountKobo)
	req.CompletedAt = completedAt
	return &req, nil
}

func (r *BankFundRepo) UpdateStatus(ctx context.Context, ref, status, providerRef, failureReason string) error {
	var completedAt *time.Time
	if status == model.BankFundStatusCompleted {
		t := time.Now()
		completedAt = &t
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE bank_fund_requests
		SET status=$2, provider_ref=NULLIF($3,''), failure_reason=NULLIF($4,''), completed_at=$5
		WHERE reference=$1
	`, ref, status, providerRef, failureReason, completedAt)
	return err
}

func (r *BankFundRepo) SumCompletedToday(ctx context.Context, userID uuid.UUID) (model.Amount, error) {
	var total int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM bank_fund_requests
		WHERE user_id = $1
		  AND status = 'completed'
		  AND created_at > NOW() - INTERVAL '24 hours'
	`, userID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum completed today: %w", err)
	}
	return model.Amount(total), nil
}
