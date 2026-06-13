package idempotency

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Response struct {
	Status int
	Header map[string]string
	Body   []byte
}

type Store struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewStore(rdb *redis.Client, ttl time.Duration) *Store {
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &Store{rdb: rdb, ttl: ttl}
}

func makeKey(namespace, idempotencyKey string) string {
	return fmt.Sprintf("idempotency:%s:%s", namespace, idempotencyKey)
}

func (s *Store) Get(ctx context.Context, namespace, key string) (*Response, bool, error) {
	data, err := s.rdb.Get(ctx, makeKey(namespace, key)).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("idempotency get: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false, fmt.Errorf("idempotency unmarshal: %w", err)
	}

	return &resp, true, nil
}

func (s *Store) Set(ctx context.Context, namespace, key string, resp *Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("idempotency marshal: %w", err)
	}

	if err := s.rdb.Set(ctx, makeKey(namespace, key), data, s.ttl).Err(); err != nil {
		return fmt.Errorf("idempotency set: %w", err)
	}

	return nil
}

type InMemoryStore struct {
	store map[string]*Response
	ttl   time.Duration
}

func NewInMemoryStore(ttl time.Duration) *InMemoryStore {
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &InMemoryStore{
		store: make(map[string]*Response),
		ttl:   ttl,
	}
}

func (s *InMemoryStore) Get(ctx context.Context, namespace, key string) (*Response, bool, error) {
	resp, ok := s.store[makeKey(namespace, key)]
	if !ok {
		return nil, false, nil
	}
	return resp, true, nil
}

func (s *InMemoryStore) Set(ctx context.Context, namespace, key string, resp *Response) error {
	s.store[makeKey(namespace, key)] = resp
	return nil
}
