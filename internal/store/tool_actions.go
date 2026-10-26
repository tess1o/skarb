package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"skarb/internal/domain"
)

// CreateToolActionInput describes a pending MCP tool invocation or a paginated
// tool result shown in Telegram.
type CreateToolActionInput struct {
	Token               string
	ChatID              int64
	MessageID           int64
	ActorTelegramUserID int64
	Action              string
	ToolName            string
	ArgumentsJSON       string
	ResultJSON          string
	NextCursor          string
	PageSize            int
	PageNumber          int
	ExpiresAt           time.Time
}

func (s *Store) CreateToolAction(ctx context.Context, q DBTX, in CreateToolActionInput) (*domain.TelegramToolAction, error) {
	if in.PageSize <= 0 {
		in.PageSize = 20
	}
	var mid any
	if in.MessageID != 0 {
		mid = in.MessageID
	}
	_, err := s.q(q).ExecContext(ctx,
		`INSERT INTO telegram_tool_actions(
			token, chat_id, message_id, actor_telegram_user_id, action, tool_name,
			arguments_json, result_json, next_cursor, page_size, page_number,
			status, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		in.Token, in.ChatID, mid, in.ActorTelegramUserID, in.Action, in.ToolName,
		in.ArgumentsJSON, in.ResultJSON, in.NextCursor, in.PageSize, in.PageNumber,
		in.ExpiresAt.UTC().Format(time.RFC3339Nano), Now(),
	)
	if err != nil {
		return nil, err
	}
	return s.GetToolAction(ctx, in.Token)
}

func (s *Store) GetToolAction(ctx context.Context, token string) (*domain.TelegramToolAction, error) {
	var a domain.TelegramToolAction
	var messageID sql.NullInt64
	var expires, created string
	var used sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT token, chat_id, message_id, actor_telegram_user_id, action, tool_name,
		        arguments_json, result_json, next_cursor, page_size, page_number,
		        status, expires_at, used_at, created_at
		 FROM telegram_tool_actions WHERE token = ?`, token,
	).Scan(
		&a.Token, &a.ChatID, &messageID, &a.ActorTelegramUserID, &a.Action, &a.ToolName,
		&a.ArgumentsJSON, &a.ResultJSON, &a.NextCursor, &a.PageSize, &a.PageNumber,
		&a.Status, &expires, &used, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if messageID.Valid {
		a.MessageID = messageID.Int64
	}
	if a.ExpiresAt, err = scanTime(expires); err != nil {
		return nil, err
	}
	if a.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if a.UsedAt, err = timePtr(used); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) BindToolActionMessage(ctx context.Context, token string, messageID int64) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_tool_actions SET message_id = ? WHERE token = ?`, messageID, token)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// UseToolAction atomically consumes a pending confirm action.
func (s *Store) UseToolAction(ctx context.Context, token string, now time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_tool_actions
		 SET status = 'used', used_at = ?
		 WHERE token = ? AND status = 'pending'
		   AND julianday(expires_at) > julianday(?)`,
		now.UTC().Format(time.RFC3339Nano), token, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CancelToolAction atomically cancels a pending confirm action.
func (s *Store) CancelToolAction(ctx context.Context, token string, now time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_tool_actions
		 SET status = 'cancelled', used_at = ?
		 WHERE token = ? AND status = 'pending'
		   AND julianday(expires_at) > julianday(?)`,
		now.UTC().Format(time.RFC3339Nano), token, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AdvanceToolPage moves a page action forward using an optimistic compare-and-set
// so a double click cannot skip a page.
func (s *Store) AdvanceToolPage(ctx context.Context, token string, expectedPage int, nextCursor string, now time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_tool_actions
		 SET page_number = page_number + 1, next_cursor = ?, used_at = ?
		 WHERE token = ? AND action = 'page' AND status = 'pending'
		   AND page_number = ? AND julianday(expires_at) > julianday(?)`,
		nextCursor, now.UTC().Format(time.RFC3339Nano), token, expectedPage, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PurgeToolActions removes expired pending rows and old completed rows.
func (s *Store) PurgeToolActions(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM telegram_tool_actions
		 WHERE (status != 'pending' AND julianday(created_at) < julianday(?))
		    OR (status = 'pending' AND julianday(expires_at) < julianday(?))`,
		olderThan.UTC().Format(time.RFC3339Nano), Now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
