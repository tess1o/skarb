package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"skarb/internal/domain"
)

// DBTX is satisfied by *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) q(q DBTX) DBTX {
	if q == nil {
		return s.DB
	}
	return q
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

func timePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := scanTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func int64Ptr(ni sql.NullInt64) *int64 {
	if !ni.Valid {
		return nil
	}
	v := ni.Int64
	return &v
}

func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func isUniqueError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

func conflict(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueError(err) {
		return fmt.Errorf("%w: %v", domain.ErrConflict, err)
	}
	return err
}
