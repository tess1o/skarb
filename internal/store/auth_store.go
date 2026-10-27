package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"skarb/internal/domain"
)

func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, csrfSecret string, expires time.Time, ip, ua string) error {
	now := Now()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions(token_hash, user_id, csrf_secret, created_at, last_seen_at, expires_at, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, csrfSecret, now, now, expires.UTC().Format(time.RFC3339Nano), ip, ua,
	)
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (*domain.Session, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT s.token_hash, s.user_id, s.csrf_secret, s.created_at, s.last_seen_at, s.expires_at,
		 s.revoked_at, s.ip, s.user_agent, `+userColumnsAliased+`
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ?`, tokenHash)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return sess, err
}

func scanSession(row rowScanner) (*domain.Session, error) {
	var s domain.Session
	var created, lastSeen, expires string
	var revoked sql.NullString
	var u domain.User
	var role string
	var active, must int
	var tg sql.NullInt64
	var uCreated, uUpdated string
	if err := row.Scan(&s.TokenHash, &s.UserID, &s.CSRFSecret, &created, &lastSeen, &expires,
		&revoked, &s.IP, &s.UserAgent,
		&u.ID, &u.UID, &u.Username, &u.DisplayName, &u.PasswordHash, &role, &active, &tg, &must,
		&uCreated, &uUpdated); err != nil {
		return nil, err
	}
	var err error
	if s.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if s.LastSeenAt, err = scanTime(lastSeen); err != nil {
		return nil, err
	}
	if s.ExpiresAt, err = scanTime(expires); err != nil {
		return nil, err
	}
	if s.RevokedAt, err = timePtr(revoked); err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	u.IsActive = active != 0
	u.MustChangePassword = must != 0
	u.TelegramUserID = int64Ptr(tg)
	if u.CreatedAt, err = scanTime(uCreated); err != nil {
		return nil, err
	}
	if u.UpdatedAt, err = scanTime(uUpdated); err != nil {
		return nil, err
	}
	s.User = u
	return &s, nil
}

func (s *Store) TouchSession(ctx context.Context, tokenHash string, expires time.Time) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		Now(), expires.UTC().Format(time.RFC3339Nano), tokenHash,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, Now(), tokenHash)
	return err
}

func (s *Store) RevokeUserSessions(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, Now(), userID)
	return err
}

type CreateAPITokenInput struct {
	TokenHash   string
	TokenPrefix string
	Last4       string
	UserID      int64
	Name        string
	ExpiresAt   *time.Time
}

func (s *Store) CreateAPIToken(ctx context.Context, in CreateAPITokenInput) (*domain.APIToken, error) {
	var expires any
	if in.ExpiresAt != nil {
		expires = in.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	uid := uuid.NewString()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO api_tokens(uid, token_hash, token_prefix, last4, user_id, name, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, in.TokenHash, in.TokenPrefix, in.Last4, in.UserID, in.Name, expires, Now(),
	)
	if err != nil {
		return nil, err
	}
	return s.GetAPITokenByUID(ctx, uid)
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*domain.APIToken, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT t.id, t.uid, t.token_prefix, t.last4, t.user_id, t.name, t.last_used_at,
		 t.expires_at, t.revoked_at, t.created_at, `+userColumnsAliased+`
		 FROM api_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = ?`, tokenHash)
	tok, err := scanAPIToken(row, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return tok, err
}

func (s *Store) GetAPITokenByUID(ctx context.Context, uid string) (*domain.APIToken, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT t.id, t.uid, t.token_prefix, t.last4, t.user_id, t.name, t.last_used_at,
		 t.expires_at, t.revoked_at, t.created_at, `+userColumnsAliased+`
		 FROM api_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.uid = ?`, uid)
	tok, err := scanAPIToken(row, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return tok, err
}

func (s *Store) ListAPITokens(ctx context.Context, userID *int64) ([]domain.APIToken, error) {
	query := `SELECT t.id, t.uid, t.token_prefix, t.last4, t.user_id, t.name, t.last_used_at,
		t.expires_at, t.revoked_at, t.created_at, ` + userColumnsAliased + `
		FROM api_tokens t JOIN users u ON u.id = t.user_id`
	args := []any{}
	if userID != nil {
		query += ` WHERE t.user_id = ?`
		args = append(args, *userID)
	}
	query += ` ORDER BY t.created_at DESC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.APIToken
	for rows.Next() {
		tok, err := scanAPIToken(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, *tok)
	}
	return out, rows.Err()
}

func scanAPIToken(row rowScanner, withUser bool) (*domain.APIToken, error) {
	var t domain.APIToken
	var lastUsed, expires, revoked sql.NullString
	var created string
	dest := []any{&t.ID, &t.UID, &t.TokenPrefix, &t.Last4, &t.UserID, &t.Name,
		&lastUsed, &expires, &revoked, &created}
	var u domain.User
	var role string
	var active, must int
	var tg sql.NullInt64
	var uCreated, uUpdated string
	if withUser {
		dest = append(dest, &u.ID, &u.UID, &u.Username, &u.DisplayName, &u.PasswordHash,
			&role, &active, &tg, &must, &uCreated, &uUpdated)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	var err error
	if t.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if t.LastUsedAt, err = timePtr(lastUsed); err != nil {
		return nil, err
	}
	if t.ExpiresAt, err = timePtr(expires); err != nil {
		return nil, err
	}
	if t.RevokedAt, err = timePtr(revoked); err != nil {
		return nil, err
	}
	if withUser {
		u.Role = domain.Role(role)
		u.IsActive = active != 0
		u.MustChangePassword = must != 0
		u.TelegramUserID = int64Ptr(tg)
		if u.CreatedAt, err = scanTime(uCreated); err != nil {
			return nil, err
		}
		if u.UpdatedAt, err = scanTime(uUpdated); err != nil {
			return nil, err
		}
		t.User = u
	}
	return &t, nil
}

func (s *Store) TouchAPIToken(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, Now(), id)
	return err
}

func (s *Store) RevokeAPIToken(ctx context.Context, uid string, userID int64) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE uid = ? AND user_id = ? AND revoked_at IS NULL`,
		Now(), uid, userID)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) RevokeAllUserAPITokens(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, Now(), userID)
	return err
}
