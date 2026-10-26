package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"skarb/internal/domain"
)

type IdempotencyRecord struct {
	Scope        string
	Key          string
	RequestHash  string
	ResponseJSON string
	ExpiresAt    *time.Time
}

func (s *Store) GetIdempotency(ctx context.Context, scope, key string) (*IdempotencyRecord, error) {
	var r IdempotencyRecord
	var expires sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT scope, key, request_hash, response_json, expires_at FROM idempotency_keys
		 WHERE scope = ? AND key = ?`, scope, key,
	).Scan(&r.Scope, &r.Key, &r.RequestHash, &r.ResponseJSON, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.ExpiresAt, err = timePtr(expires); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) PutIdempotency(ctx context.Context, q DBTX, scope, key string, actorID int64, requestHash, responseJSON string) error {
	_, err := s.q(q).ExecContext(ctx,
		`INSERT INTO idempotency_keys(scope, key, actor_user_id, request_hash, response_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(scope, key) DO NOTHING`,
		scope, key, nullableActorID(actorID), requestHash, responseJSON, Now(),
	)
	return err
}
