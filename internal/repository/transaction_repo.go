package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type TransactionRepo struct {
	pool *pgxpool.Pool
}

func NewTransactionRepo(pool *pgxpool.Pool) *TransactionRepo {
	return &TransactionRepo{pool: pool}
}

func (r *TransactionRepo) Create(ctx context.Context, input model.CreateTransactionInput) (*model.Transaction, error) {
	txn := &model.Transaction{}
	now := time.Now()
	err := 	getQuerier(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO transactions (user_id, parent_id, type, amount, fee, currency, status,
		                          source_account_id, source_provider,
		                          recipient_account, recipient_bank_code, recipient_name,
		                          our_ref, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, status, created_at
	`, input.UserID, input.ParentID, input.Type, input.Amount, input.Fee, input.Currency,
		model.TxnStatusPending, input.SourceAccountID, input.SourceProvider,
		input.RecipientAccount, input.RecipientBankCode, input.RecipientName,
		input.OurRef, input.IdempotencyKey,
	).Scan(&txn.ID, &txn.Status, &txn.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create transaction: %w", err)
	}
	txn.UserID = input.UserID
	txn.ParentID = input.ParentID
	txn.Type = input.Type
	txn.Amount = input.Amount
	txn.Fee = input.Fee
	txn.Currency = input.Currency
	txn.SourceAccountID = input.SourceAccountID
	txn.SourceProvider = input.SourceProvider
	txn.RecipientAccount = input.RecipientAccount
	txn.RecipientBankCode = input.RecipientBankCode
	txn.RecipientName = input.RecipientName
	txn.OurRef = input.OurRef
	txn.IdempotencyKey = input.IdempotencyKey
	txn.CreatedAt = now
	return txn, nil
}

func scanTransaction(row pgx.Row) (*model.Transaction, error) {
	txn := &model.Transaction{}
	err := row.Scan(
		&txn.ID, &txn.UserID, &txn.ParentID, &txn.Type, &txn.Amount, &txn.Fee,
		&txn.Currency, &txn.Status, &txn.SourceAccountID, &txn.SourceProvider,
		&txn.RecipientAccount, &txn.RecipientBankCode, &txn.RecipientName,
		&txn.ProviderRef, &txn.OurRef, &txn.IdempotencyKey,
		&txn.FailureReason, &txn.CreatedAt, &txn.CompletedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return txn, nil
}

func (r *TransactionRepo) GetByID(ctx context.Context, id uuid.UUID) (*model.Transaction, error) {
	txn, err := scanTransaction(	getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT id, user_id, parent_id, type, amount, fee, currency, status,
		       source_account_id, COALESCE(source_provider, ''),
		       COALESCE(recipient_account, ''), COALESCE(recipient_bank_code, ''),
		       COALESCE(recipient_name, ''), COALESCE(provider_ref, ''),
		       our_ref, COALESCE(idempotency_key, ''),
		       COALESCE(failure_reason, ''), created_at, completed_at
		FROM transactions WHERE id = $1
	`, id))
	if err != nil {
		return nil, fmt.Errorf("get transaction: %w", err)
	}
	return txn, nil
}

func (r *TransactionRepo) GetByOurRef(ctx context.Context, ourRef string) (*model.Transaction, error) {
	txn, err := scanTransaction(	getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT id, user_id, parent_id, type, amount, fee, currency, status,
		       source_account_id, COALESCE(source_provider, ''),
		       COALESCE(recipient_account, ''), COALESCE(recipient_bank_code, ''),
		       COALESCE(recipient_name, ''), COALESCE(provider_ref, ''),
		       our_ref, COALESCE(idempotency_key, ''),
		       COALESCE(failure_reason, ''), created_at, completed_at
		FROM transactions WHERE our_ref = $1
	`, ourRef))
	if err != nil {
		return nil, fmt.Errorf("get transaction by ref: %w", err)
	}
	return txn, nil
}

func (r *TransactionRepo) GetByParentID(ctx context.Context, parentID uuid.UUID) ([]model.Transaction, error) {
	rows, err := 	getQuerier(ctx, r.pool).Query(ctx, `
		SELECT id, user_id, parent_id, type, amount, fee, currency, status,
		       source_account_id, COALESCE(source_provider, ''),
		       COALESCE(recipient_account, ''), COALESCE(recipient_bank_code, ''),
		       COALESCE(recipient_name, ''), COALESCE(provider_ref, ''),
		       our_ref, COALESCE(idempotency_key, ''),
		       COALESCE(failure_reason, ''), created_at, completed_at
		FROM transactions WHERE parent_id = $1
		ORDER BY created_at ASC
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("get child transactions: %w", err)
	}
	defer rows.Close()

	var txns []model.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan child transaction: %w", err)
		}
		txns = append(txns, *t)
	}
	return txns, nil
}

func (r *TransactionRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status model.TransactionStatus, failureReason string) error {
	var err error
	if status == model.TxnStatusCompleted || status == model.TxnStatusFailed {
		_, err = 	getQuerier(ctx, r.pool).Exec(ctx, `
			UPDATE transactions SET status = $2, failure_reason = $3, completed_at = NOW()
			WHERE id = $1
		`, id, status, failureReason)
	} else {
		_, err = 	getQuerier(ctx, r.pool).Exec(ctx, `
			UPDATE transactions SET status = $2, failure_reason = $3
			WHERE id = $1
		`, id, status, failureReason)
	}
	if err != nil {
		return fmt.Errorf("update transaction status: %w", err)
	}
	return nil
}

func (r *TransactionRepo) UpdateProviderRef(ctx context.Context, id uuid.UUID, providerRef string) error {
	_, err := 	getQuerier(ctx, r.pool).Exec(ctx, `
		UPDATE transactions SET provider_ref = $2 WHERE id = $1
	`, id, providerRef)
	if err != nil {
		return fmt.Errorf("update provider ref: %w", err)
	}
	return nil
}

func (r *TransactionRepo) GetByIdempotencyKey(ctx context.Context, key string) (*model.Transaction, error) {
	txn, err := scanTransaction(	getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT id, user_id, parent_id, type, amount, fee, currency, status,
		       source_account_id, COALESCE(source_provider, ''),
		       COALESCE(recipient_account, ''), COALESCE(recipient_bank_code, ''),
		       COALESCE(recipient_name, ''), COALESCE(provider_ref, ''),
		       our_ref, COALESCE(idempotency_key, ''),
		       COALESCE(failure_reason, ''), created_at, completed_at
		FROM transactions WHERE idempotency_key = $1
	`, key))
	if err != nil {
		return nil, fmt.Errorf("get transaction by idempotency key: %w", err)
	}
	return txn, nil
}

const selectColumns = `id, user_id, parent_id, type, amount, fee, currency, status,
	source_account_id, COALESCE(source_provider, ''),
	COALESCE(recipient_account, ''), COALESCE(recipient_bank_code, ''),
	COALESCE(recipient_name, ''), COALESCE(provider_ref, ''),
	our_ref, COALESCE(idempotency_key, ''),
	COALESCE(failure_reason, ''), created_at, completed_at`

func (r *TransactionRepo) buildFilterWhere(filter TransactionFilter) (string, []any) {
	var clauses []string
	var args []any
	n := 1

	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, s := range filter.Statuses {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, s)
			n++
		}
		clauses = append(clauses, fmt.Sprintf("status IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(filter.Types) > 0 {
		placeholders := make([]string, len(filter.Types))
		for i, t := range filter.Types {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, t)
			n++
		}
		clauses = append(clauses, fmt.Sprintf("type IN (%s)", strings.Join(placeholders, ",")))
	}
	if !filter.From.IsZero() {
		clauses = append(clauses, fmt.Sprintf("created_at >= $%d", n))
		args = append(args, filter.From)
		n++
	}
	if !filter.To.IsZero() {
		clauses = append(clauses, fmt.Sprintf("created_at <= $%d", n))
		args = append(args, filter.To)
		n++
	}

	where := ""
	if len(clauses) > 0 {
		where = " AND " + strings.Join(clauses, " AND ")
	}
	return where, args
}

func (r *TransactionRepo) ListByUserID(ctx context.Context, userID uuid.UUID, filter TransactionFilter) ([]model.Transaction, error) {
	where, args := r.buildFilterWhere(filter)
	query := fmt.Sprintf(`SELECT %s FROM transactions WHERE user_id = $%d%s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		selectColumns, len(args)+1, where, len(args)+2, len(args)+3)
	allArgs := append(args, userID, filter.Limit, filter.Offset)

	rows, err := getQuerier(ctx, r.pool).Query(ctx, query, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	var txns []model.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		txns = append(txns, *t)
	}
	return txns, nil
}

func (r *TransactionRepo) CountByUserID(ctx context.Context, userID uuid.UUID, filter TransactionFilter) (int, error) {
	where, args := r.buildFilterWhere(filter)
	query := fmt.Sprintf(`SELECT COUNT(*) FROM transactions WHERE user_id = $%d%s`, len(args)+1, where)
	allArgs := append(args, userID)

	var count int
	if err := getQuerier(ctx, r.pool).QueryRow(ctx, query, allArgs...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count transactions: %w", err)
	}
	return count, nil
}
