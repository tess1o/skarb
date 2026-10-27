package store

import (
	"context"
	"time"
)

// StaleDataRetention is how long rows that only matter briefly are kept:
// Telegram update IDs (used to drop duplicate deliveries) and idempotency keys
// (used to replay a retried MCP call).
const StaleDataRetention = 30 * 24 * time.Hour

// PurgeStale deletes rows that are no longer useful: expired or revoked web
// sessions, expired pending Telegram comment edits, and Telegram updates and
// idempotency keys older than StaleDataRetention. It returns the number of
// rows removed per table.
func (s *Store) PurgeStale(ctx context.Context, now time.Time) (map[string]int64, error) {
	cutoff := now.Add(-StaleDataRetention).UTC().Format(time.RFC3339Nano)
	nowStr := now.UTC().Format(time.RFC3339Nano)
	jobs := []struct {
		table string
		query string
		arg   string
	}{
		{"sessions", `DELETE FROM sessions WHERE julianday(expires_at) < julianday(?) OR revoked_at IS NOT NULL`, nowStr},
		{"telegram_pending_edits", `DELETE FROM telegram_pending_edits WHERE julianday(expires_at) < julianday(?)`, nowStr},
		{"telegram_updates", `DELETE FROM telegram_updates WHERE julianday(received_at) < julianday(?)`, cutoff},
		{"idempotency_keys", `DELETE FROM idempotency_keys WHERE julianday(created_at) < julianday(?)`, cutoff},
	}
	removed := map[string]int64{}
	for _, j := range jobs {
		res, err := s.DB.ExecContext(ctx, j.query, j.arg)
		if err != nil {
			return removed, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return removed, err
		}
		removed[j.table] = n
	}
	return removed, nil
}
