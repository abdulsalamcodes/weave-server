package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

func (r *UserRepo) Create(ctx context.Context, input model.CreateUserInput) (*model.User, error) {
	user := &model.User{}
	err := 	getQuerier(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO users (phone, full_name, pin_hash)
		VALUES ($1, $2, $3)
		RETURNING id, phone, full_name, kyc_level, biometric_enabled, created_at, updated_at
	`, input.Phone, input.FullName, input.PIN).Scan(
		&user.ID, &user.Phone, &user.FullName,
		&user.KYCLevel, &user.BiometricEnabled,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	user := &model.User{}
	err := 	getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT id, phone, COALESCE(email, ''), full_name,
		       pin_hash, kyc_level, biometric_enabled,
		       COALESCE(biometric_public_key, ''),
		       created_at, updated_at
		FROM users WHERE id = $1
	`, id).Scan(
		&user.ID, &user.Phone, &user.Email, &user.FullName,
		&user.PINHash, &user.KYCLevel, &user.BiometricEnabled,
		&user.BiometricPublicKey,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return user, nil
}

func (r *UserRepo) GetByPhone(ctx context.Context, phone string) (*model.User, error) {
	user := &model.User{}
	err := 	getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT id, phone, COALESCE(email, ''), full_name,
		       pin_hash, kyc_level, biometric_enabled,
		       COALESCE(biometric_public_key, ''),
		       created_at, updated_at
		FROM users WHERE phone = $1
	`, phone).Scan(
		&user.ID, &user.Phone, &user.Email, &user.FullName,
		&user.PINHash, &user.KYCLevel, &user.BiometricEnabled,
		&user.BiometricPublicKey,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by phone: %w", err)
	}
	return user, nil
}

func (r *UserRepo) UpdateKYC(ctx context.Context, userID uuid.UUID, level model.KYCLevel, bvnHash, ninHash string) error {
	_, err := 	getQuerier(ctx, r.pool).Exec(ctx, `
		UPDATE users SET kyc_level = $2, bvn_hash = $3, nin_hash = $4, updated_at = NOW()
		WHERE id = $1
	`, userID, level, bvnHash, ninHash)
	if err != nil {
		return fmt.Errorf("update kyc: %w", err)
	}
	return nil
}

func (r *UserRepo) RecordPINAttempt(ctx context.Context, userID uuid.UUID, success bool, ip string) error {
	var ipVal interface{}
	if ip != "" {
		ipVal = ip
	}
	_, err := getQuerier(ctx, r.pool).Exec(ctx, `
		INSERT INTO pin_attempts (user_id, success, ip_address)
		VALUES ($1, $2, $3)
	`, userID, success, ipVal)
	return err
}

func (r *UserRepo) RecentFailedPINAttempts(ctx context.Context, userID uuid.UUID, within time.Duration) (int, error) {
	var count int
	err := 	getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT COUNT(*) FROM pin_attempts
		WHERE user_id = $1 AND success = false AND created_at > NOW() - $2::interval
	`, userID, fmt.Sprintf("%d minutes", int(within.Minutes()))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pin attempts: %w", err)
	}
	return count, nil
}

func (r *UserRepo) RecentFailedPINAttemptsByIP(ctx context.Context, ip string, within time.Duration) (int, error) {
	if ip == "" {
		return 0, nil
	}
	var count int
	err := getQuerier(ctx, r.pool).QueryRow(ctx, `
		SELECT COUNT(*) FROM pin_attempts
		WHERE ip_address = $1 AND success = false AND created_at > NOW() - $2::interval
	`, ip, fmt.Sprintf("%d minutes", int(within.Minutes()))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count ip pin attempts: %w", err)
	}
	return count, nil
}

func (r *UserRepo) UpdatePIN(ctx context.Context, userID uuid.UUID, pinHash string) error {
	_, err := 	getQuerier(ctx, r.pool).Exec(ctx, `
		UPDATE users SET pin_hash = $2, updated_at = NOW() WHERE id = $1
	`, userID, pinHash)
	return err
}
