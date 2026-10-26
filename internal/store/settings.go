package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"skarb/internal/domain"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.DB.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SetSetting(ctx context.Context, q DBTX, key, valueJSON string, updatedBy *int64) error {
	_, err := s.q(q).ExecContext(ctx,
		`INSERT INTO settings(key, value_json, updated_by_user_id, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		   value_json = excluded.value_json,
		   updated_by_user_id = excluded.updated_by_user_id,
		   updated_at = excluded.updated_at`,
		key, valueJSON, nullInt64(updatedBy), Now(),
	)
	return err
}

func (s *Store) GetSettings(ctx context.Context, prefix string) (map[string]string, error) {
	query := `SELECT key, value_json FROM settings`
	args := []any{}
	if prefix != "" {
		query += ` WHERE key LIKE ?`
		args = append(args, strings.ReplaceAll(prefix, "%", "\\%")+"%")
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) RequireSetting(ctx context.Context, key string) (string, error) {
	v, ok, err := s.GetSetting(ctx, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}
