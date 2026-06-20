package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdulsalamcodes/weave-server/internal/model"
)

type ChatMessageRepo struct {
	pool *pgxpool.Pool
}

func NewChatMessageRepo(pool *pgxpool.Pool) *ChatMessageRepo {
	return &ChatMessageRepo{pool: pool}
}

func (r *ChatMessageRepo) Create(ctx context.Context, msg *model.ChatMessage) error {
	return getQuerier(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO chat_messages (user_id, role, content, is_error)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`, msg.UserID, msg.Role, msg.Content, msg.IsError,
	).Scan(&msg.ID, &msg.CreatedAt)
}

func (r *ChatMessageRepo) ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.ChatMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := getQuerier(ctx, r.pool).Query(ctx, `
		SELECT id, user_id, role, content, is_error, created_at
		FROM chat_messages
		WHERE user_id = $1
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	defer rows.Close()

	var msgs []model.ChatMessage
	for rows.Next() {
		var m model.ChatMessage
		if err := rows.Scan(&m.ID, &m.UserID, &m.Role, &m.Content, &m.IsError, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chat message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (r *ChatMessageRepo) DeleteByUserID(ctx context.Context, userID uuid.UUID) error {
	_, err := getQuerier(ctx, r.pool).Exec(ctx,
		`DELETE FROM chat_messages WHERE user_id = $1`, userID,
	)
	return err
}

// RecentAsLLMMessages returns the last n messages formatted for the LLM context window.
func (r *ChatMessageRepo) RecentAsLLMMessages(ctx context.Context, userID uuid.UUID, n int) ([]model.ChatMessage, error) {
	rows, err := getQuerier(ctx, r.pool).Query(ctx, `
		SELECT id, user_id, role, content, is_error, created_at
		FROM (
			SELECT * FROM chat_messages
			WHERE user_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		) recent
		ORDER BY created_at ASC
	`, userID, n)
	if err != nil {
		return nil, fmt.Errorf("recent llm messages: %w", err)
	}
	defer rows.Close()

	var msgs []model.ChatMessage
	for rows.Next() {
		var m model.ChatMessage
		if err := rows.Scan(&m.ID, &m.UserID, &m.Role, &m.Content, &m.IsError, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
