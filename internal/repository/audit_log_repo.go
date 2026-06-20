package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type AuditLogRepo struct {
	pool *pgxpool.Pool
}

func NewAuditLogRepo(pool *pgxpool.Pool) *AuditLogRepo {
	return &AuditLogRepo{pool: pool}
}

func (r *AuditLogRepo) Write(ctx context.Context, entry *model.AuditLog) error {
	meta, _ := json.Marshal(entry.Metadata)
	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, status, metadata, ip_address)
		VALUES ($1, $2, $3, $4, $5)
	`, entry.UserID, entry.Action, entry.Status, meta, nullStr(entry.IPAddress))
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

func (r *AuditLogRepo) ListByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]model.AuditLog, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, action, status, metadata, ip_address, created_at
		FROM audit_logs
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []model.AuditLog
	for rows.Next() {
		var l model.AuditLog
		var meta []byte
		var ip *string
		if err := rows.Scan(&l.ID, &l.UserID, &l.Action, &l.Status, &meta, &ip, &l.CreatedAt); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &l.Metadata)
		}
		if ip != nil {
			l.IPAddress = *ip
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
