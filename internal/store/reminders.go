package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"skarb/internal/domain"
)

func scanReminderConfig(row rowScanner) (*domain.ReminderConfig, error) {
	var c domain.ReminderConfig
	var enabled int
	var chat, user sql.NullInt64
	var lastRun, lastSent sql.NullString
	var updated string
	if err := row.Scan(&c.Kind, &c.CronExpr, &enabled, &c.Timezone, &c.WindowDays,
		&chat, &user, &lastRun, &lastSent, &updated); err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	c.TargetChatID = int64Ptr(chat)
	c.TargetUserID = int64Ptr(user)
	var err error
	if c.LastRunAt, err = timePtr(lastRun); err != nil {
		return nil, err
	}
	if c.LastSentAt, err = timePtr(lastSent); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = scanTime(updated); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) GetReminderConfig(ctx context.Context, kind string) (*domain.ReminderConfig, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT kind, cron_expr, enabled, timezone, window_days, target_chat_id, target_user_id,
		 last_run_at, last_sent_at, updated_at FROM reminder_configs WHERE kind = ?`, kind)
	c, err := scanReminderConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (s *Store) ListReminderConfigs(ctx context.Context) ([]domain.ReminderConfig, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT kind, cron_expr, enabled, timezone, window_days, target_chat_id, target_user_id,
		 last_run_at, last_sent_at, updated_at FROM reminder_configs ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ReminderConfig
	for rows.Next() {
		c, err := scanReminderConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateReminderConfig(ctx context.Context, q DBTX, c domain.ReminderConfig) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE reminder_configs SET cron_expr = ?, enabled = ?, timezone = ?, window_days = ?,
		 target_chat_id = ?, target_user_id = ?, updated_at = ? WHERE kind = ?`,
		c.CronExpr, boolInt(c.Enabled), c.Timezone, c.WindowDays,
		nullInt64(c.TargetChatID), nullInt64(c.TargetUserID), Now(), c.Kind,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) SetReminderLastRun(ctx context.Context, kind string, sent bool) error {
	now := Now()
	var err error
	if sent {
		_, err = s.DB.ExecContext(ctx,
			`UPDATE reminder_configs SET last_run_at = ?, last_sent_at = ? WHERE kind = ?`, now, now, kind)
	} else {
		_, err = s.DB.ExecContext(ctx,
			`UPDATE reminder_configs SET last_run_at = ? WHERE kind = ?`, now, kind)
	}
	return err
}

func (s *Store) RecordReminderRun(ctx context.Context, kind string, scheduledFor time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO reminder_runs(kind, scheduled_for, status, created_at)
		 VALUES (?, ?, 'started', ?)`,
		kind, scheduledFor.UTC().Format(time.RFC3339Nano), Now(),
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) FinishReminderRun(ctx context.Context, kind string, scheduledFor time.Time, status, errMsg string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE reminder_runs SET status = ?, error = ? WHERE kind = ? AND scheduled_for = ?`,
		status, nullString(errMsg), kind, scheduledFor.UTC().Format(time.RFC3339Nano),
	)
	return err
}
