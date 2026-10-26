package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"skarb/internal/domain"
)

func (s *Store) IsTelegramChatAllowed(ctx context.Context, chatID int64) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM telegram_chats WHERE chat_id = ?`, chatID).Scan(&n)
	return n > 0, err
}

func (s *Store) ListTelegramChats(ctx context.Context) ([]domain.TelegramChat, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT chat_id, title, chat_type, added_by_user_id, created_at
		FROM telegram_chats ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TelegramChat
	for rows.Next() {
		var c domain.TelegramChat
		var added sql.NullInt64
		var created string
		if err := rows.Scan(&c.ChatID, &c.Title, &c.ChatType, &added, &created); err != nil {
			return nil, err
		}
		c.AddedByUserID = int64Ptr(added)
		c.CreatedAt, err = scanTime(created)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) AddTelegramChat(ctx context.Context, chatID int64, title, chatType string, addedBy *int64) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO telegram_chats(chat_id, title, chat_type, added_by_user_id, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET title = excluded.title, chat_type = excluded.chat_type`,
		chatID, title, chatType, nullInt64(addedBy), Now(),
	)
	return err
}

func (s *Store) RemoveTelegramChat(ctx context.Context, chatID int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM telegram_chats WHERE chat_id = ?`, chatID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

// MarkUpdateReceived inserts an update id and reports whether it was new.
func (s *Store) MarkUpdateReceived(ctx context.Context, botID, updateID int64, kind string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO telegram_updates(bot_id, update_id, kind, status, received_at)
		 VALUES (?, ?, ?, 'received', ?)`,
		botID, updateID, kind, Now(),
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) MarkUpdateProcessed(ctx context.Context, botID, updateID int64, errMsg string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_updates SET status = ?, processed_at = ?, error = ?
		 WHERE bot_id = ? AND update_id = ?`,
		map[bool]string{true: "failed", false: "processed"}[errMsg != ""], Now(), nullString(errMsg), botID, updateID,
	)
	return err
}

func (s *Store) CreateConfirmation(ctx context.Context, q DBTX, chatID, messageID, actorTG int64, itemsJSON string) (*domain.TelegramConfirmation, error) {
	now := Now()
	var mid any
	if messageID != 0 {
		mid = messageID
	}
	res, err := s.q(q).ExecContext(ctx,
		`INSERT INTO telegram_confirmations(chat_id, message_id, actor_telegram_user_id, items_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		chatID, mid, actorTG, itemsJSON, now, now,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.getConfirmationQ(ctx, q, id)
}

func (s *Store) GetConfirmation(ctx context.Context, id int64) (*domain.TelegramConfirmation, error) {
	return s.getConfirmationQ(ctx, nil, id)
}

func (s *Store) getConfirmationQ(ctx context.Context, q DBTX, id int64) (*domain.TelegramConfirmation, error) {
	var c domain.TelegramConfirmation
	var created, updated string
	var messageID sql.NullInt64
	err := s.q(q).QueryRowContext(ctx,
		`SELECT id, chat_id, message_id, actor_telegram_user_id, items_json, created_at, updated_at
		 FROM telegram_confirmations WHERE id = ?`, id,
	).Scan(&c.ID, &c.ChatID, &messageID, &c.ActorTelegramUserID, &c.ItemsJSON, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if messageID.Valid {
		c.MessageID = messageID.Int64
	}
	if c.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = scanTime(updated); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) GetConfirmationByMessage(ctx context.Context, chatID, messageID int64) (*domain.TelegramConfirmation, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM telegram_confirmations WHERE chat_id = ? AND message_id = ?`, chatID, messageID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetConfirmation(ctx, id)
}

func (s *Store) UpdateConfirmation(ctx context.Context, q DBTX, id, messageID int64, itemsJSON string) error {
	var mid any
	if messageID != 0 {
		mid = messageID
	}
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE telegram_confirmations SET message_id = COALESCE(?, message_id), items_json = ?, updated_at = ? WHERE id = ?`,
		mid, itemsJSON, Now(), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) CreateAction(ctx context.Context, q DBTX, token string, confirmationID, transactionID int64, action string, actorTG int64, expires time.Time) error {
	_, err := s.q(q).ExecContext(ctx,
		`INSERT INTO telegram_actions(token, confirmation_id, transaction_id, action, actor_telegram_user_id, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		token, confirmationID, transactionID, action, actorTG, expires.UTC().Format(time.RFC3339Nano), Now(),
	)
	return err
}

func (s *Store) GetAction(ctx context.Context, token string) (*domain.TelegramAction, error) {
	var a domain.TelegramAction
	var expires, created string
	var used sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT token, confirmation_id, transaction_id, action, actor_telegram_user_id, expires_at, used_at, created_at
		 FROM telegram_actions WHERE token = ?`, token,
	).Scan(&a.Token, &a.ConfirmationID, &a.TransactionID, &a.Action, &a.ActorTelegramUserID, &expires, &used, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
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

func (s *Store) UseAction(ctx context.Context, token string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_actions SET used_at = ? WHERE token = ? AND used_at IS NULL`, Now(), token)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) ClearOpenActions(ctx context.Context, confirmationID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM telegram_actions WHERE confirmation_id = ? AND used_at IS NULL`, confirmationID)
	return err
}

func (s *Store) CreatePendingEdit(ctx context.Context, chatID, promptMessageID, confirmationID, transactionID, actorTG int64, expires time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO telegram_pending_edits(chat_id, prompt_message_id, confirmation_id, transaction_id, actor_telegram_user_id, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(chat_id, prompt_message_id) DO UPDATE SET
		 confirmation_id = excluded.confirmation_id,
		 transaction_id = excluded.transaction_id,
		 actor_telegram_user_id = excluded.actor_telegram_user_id,
		 expires_at = excluded.expires_at,
		 created_at = excluded.created_at`,
		chatID, promptMessageID, confirmationID, transactionID, actorTG, expires.UTC().Format(time.RFC3339Nano), Now(),
	)
	return err
}

func (s *Store) GetPendingEdit(ctx context.Context, chatID, promptMessageID int64) (*domain.TelegramPendingEdit, error) {
	var p domain.TelegramPendingEdit
	var expires, created string
	err := s.DB.QueryRowContext(ctx,
		`SELECT chat_id, prompt_message_id, confirmation_id, transaction_id, actor_telegram_user_id, expires_at, created_at
		 FROM telegram_pending_edits WHERE chat_id = ? AND prompt_message_id = ?`, chatID, promptMessageID,
	).Scan(&p.ChatID, &p.PromptMessageID, &p.ConfirmationID, &p.TransactionID, &p.ActorTelegramUserID, &expires, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.ExpiresAt, err = scanTime(expires); err != nil {
		return nil, err
	}
	if p.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) DeletePendingEdit(ctx context.Context, chatID, promptMessageID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM telegram_pending_edits WHERE chat_id = ? AND prompt_message_id = ?`, chatID, promptMessageID)
	return err
}

type OutboxItem struct {
	ID          int64
	Method      string
	PayloadJSON string
	Attempts    int
}

func (s *Store) EnqueueOutbox(ctx context.Context, q DBTX, method string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.q(q).ExecContext(ctx,
		`INSERT INTO telegram_outbox(method, payload_json, status, attempts, next_attempt_at, created_at)
		 VALUES (?, ?, 'pending', 0, ?, ?)`,
		method, string(b), Now(), Now(),
	)
	return err
}

func (s *Store) ListPendingOutbox(ctx context.Context, limit int) ([]OutboxItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, method, payload_json, attempts FROM telegram_outbox
		 WHERE status = 'pending' AND next_attempt_at <= ? ORDER BY id LIMIT ?`, Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var item OutboxItem
		if err := rows.Scan(&item.ID, &item.Method, &item.PayloadJSON, &item.Attempts); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_outbox SET status = 'sent', sent_at = ? WHERE id = ?`, Now(), id)
	return err
}

func (s *Store) MarkOutboxFailed(ctx context.Context, id int64, errMsg string, next time.Time) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_outbox SET attempts = attempts + 1, last_error = ?, next_attempt_at = ? WHERE id = ?`,
		errMsg, next.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) MarkOutboxDead(ctx context.Context, id int64, errMsg string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE telegram_outbox SET status = 'failed', last_error = ? WHERE id = ?`, errMsg, id)
	return err
}
