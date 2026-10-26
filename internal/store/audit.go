package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"skarb/internal/domain"
)

type AuditEntry struct {
	Actor      domain.Actor
	Action     string
	EntityType string
	EntityUID  string
	BeforeJSON string
	AfterJSON  string
}

func (s *Store) InsertAudit(ctx context.Context, q DBTX, e AuditEntry) error {
	_, err := s.q(q).ExecContext(ctx,
		`INSERT INTO audit_log(created_at, actor_kind, actor_user_id, actor_token_uid,
		 actor_telegram_user_id, actor_chat_id, action, entity_type, entity_uid,
		 before_json, after_json, request_id, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		Now(), string(e.Actor.Kind), nullableActorID(e.Actor.UserID), nullString(e.Actor.TokenUID),
		nullableActorID(e.Actor.TelegramUserID), nullableActorID(e.Actor.ChatID),
		e.Action, e.EntityType, nullString(e.EntityUID),
		nullString(e.BeforeJSON), nullString(e.AfterJSON),
		nullString(e.Actor.RequestID), nullString(e.Actor.IP), nullString(e.Actor.UserAgent),
	)
	return err
}

func nullableActorID(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type AuditFilter struct {
	ActorUserID *int64
	Action      string
	EntityType  string
	EntityUID   string
	From        *time.Time
	To          *time.Time
	Limit       int
	Offset      int
}

type AuditRow struct {
	ID          int64
	CreatedAt   time.Time
	ActorKind   domain.Source
	ActorUserID *int64
	ActorName   string
	Action      string
	EntityType  string
	EntityUID   string
	BeforeJSON  string
	AfterJSON   string
	RequestID   string
	IP          string
}

func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.ActorUserID != nil {
		where = append(where, "a.actor_user_id = ?")
		args = append(args, *f.ActorUserID)
	}
	if f.Action != "" {
		where = append(where, "a.action = ?")
		args = append(args, f.Action)
	}
	if f.EntityType != "" {
		where = append(where, "a.entity_type = ?")
		args = append(args, f.EntityType)
	}
	if f.EntityUID != "" {
		where = append(where, "a.entity_uid = ?")
		args = append(args, f.EntityUID)
	}
	if f.From != nil {
		where = append(where, "a.created_at >= ?")
		args = append(args, f.From.UTC().Format(time.RFC3339Nano))
	}
	if f.To != nil {
		where = append(where, "a.created_at <= ?")
		args = append(args, f.To.UTC().Format(time.RFC3339Nano))
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := `SELECT a.id, a.created_at, a.actor_kind, a.actor_user_id,
		COALESCE(u.display_name, ''), a.action, a.entity_type, COALESCE(a.entity_uid, ''),
		COALESCE(a.before_json, ''), COALESCE(a.after_json, ''),
		COALESCE(a.request_id, ''), COALESCE(a.ip, '')
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY a.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var kind, created string
		var actor sql.NullInt64
		if err := rows.Scan(&r.ID, &created, &kind, &actor, &r.ActorName, &r.Action,
			&r.EntityType, &r.EntityUID, &r.BeforeJSON, &r.AfterJSON, &r.RequestID, &r.IP); err != nil {
			return nil, err
		}
		r.ActorKind = domain.Source(kind)
		r.ActorUserID = int64Ptr(actor)
		r.CreatedAt, err = scanTime(created)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
