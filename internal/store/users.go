package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"skarb/internal/domain"
)

const userColumns = `id, uid, username, display_name, password_hash, role, is_active,
	telegram_user_id, must_change_password, created_at, updated_at`

const userColumnsAliased = `u.id, u.uid, u.username, u.display_name, u.password_hash, u.role,
	u.is_active, u.telegram_user_id, u.must_change_password, u.created_at, u.updated_at`

type CreateUserInput struct {
	Username           string
	DisplayName        string
	PasswordHash       string
	Role               domain.Role
	TelegramUserID     *int64
	MustChangePassword bool
}

func (s *Store) CreateUser(ctx context.Context, q DBTX, in CreateUserInput) (*domain.User, error) {
	if in.Role == "" {
		in.Role = domain.RoleMember
	}
	if in.Role != domain.RoleAdmin && in.Role != domain.RoleMember {
		return nil, domain.ErrValidation
	}
	now := Now()
	res, err := s.q(q).ExecContext(ctx,
		`INSERT INTO users(uid, username, display_name, password_hash, role, is_active,
		 telegram_user_id, must_change_password, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		uuid.NewString(), in.Username, in.DisplayName, in.PasswordHash, string(in.Role),
		nullInt64(in.TelegramUserID), boolInt(in.MustChangePassword), now, now,
	)
	if err != nil {
		return nil, conflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.getUserByIDQ(ctx, q, id)
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*domain.User, error) {
	return s.getUserByIDQ(ctx, nil, id)
}

func (s *Store) getUserByIDQ(ctx context.Context, q DBTX, id int64) (*domain.User, error) {
	return s.scanUserQueryQ(ctx, q, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
}

func (s *Store) GetUserByUID(ctx context.Context, uid string) (*domain.User, error) {
	return s.scanUserQuery(ctx, `SELECT `+userColumns+` FROM users WHERE uid = ?`, uid)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	return s.scanUserQuery(ctx, `SELECT `+userColumns+` FROM users WHERE username = ? COLLATE NOCASE`, username)
}

func (s *Store) GetUserByTelegramID(ctx context.Context, tgID int64) (*domain.User, error) {
	return s.scanUserQuery(ctx, `SELECT `+userColumns+` FROM users WHERE telegram_user_id = ?`, tgID)
}

func (s *Store) scanUserQuery(ctx context.Context, query string, args ...any) (*domain.User, error) {
	return s.scanUserQueryQ(ctx, nil, query, args...)
}

func (s *Store) scanUserQueryQ(ctx context.Context, q DBTX, query string, args ...any) (*domain.User, error) {
	row := s.q(q).QueryRowContext(ctx, query, args...)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return u, err
}

func scanUser(row rowScanner) (*domain.User, error) {
	var u domain.User
	var role string
	var active, mustChange int
	var tg sql.NullInt64
	var created, updated string
	if err := row.Scan(&u.ID, &u.UID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&role, &active, &tg, &mustChange, &created, &updated); err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	u.IsActive = active != 0
	u.MustChangePassword = mustChange != 0
	u.TelegramUserID = int64Ptr(tg)
	var err error
	if u.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if u.UpdatedAt, err = scanTime(updated); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context, includeInactive bool) ([]domain.User, error) {
	query := `SELECT ` + userColumns + ` FROM users`
	if !includeInactive {
		query += ` WHERE is_active = 1`
	}
	query += ` ORDER BY CASE role WHEN 'admin' THEN 0 ELSE 1 END, display_name`
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) CountActiveUsers(ctx context.Context, q DBTX, excludeID int64) (int, error) {
	var n int
	err := s.q(q).QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE is_active = 1 AND id <> ?`, excludeID).Scan(&n)
	return n, err
}

func (s *Store) CountAdmins(ctx context.Context, q DBTX, excludeID int64) (int, error) {
	var n int
	err := s.q(q).QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin' AND is_active = 1 AND id <> ?`, excludeID).Scan(&n)
	return n, err
}

func (s *Store) SetUserActive(ctx context.Context, q DBTX, id int64, active bool) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE users SET is_active = ?, updated_at = ? WHERE id = ?`,
		boolInt(active), Now(), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) SetUserPassword(ctx context.Context, q DBTX, id int64, hash string, mustChange bool) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		hash, boolInt(mustChange), Now(), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) SetUserDisplayName(ctx context.Context, q DBTX, id int64, name string) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`,
		name, Now(), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) SetUserTelegramID(ctx context.Context, q DBTX, id int64, tgID *int64) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE users SET telegram_user_id = ?, updated_at = ? WHERE id = ?`,
		nullInt64(tgID), Now(), id,
	)
	if err != nil {
		return conflict(err)
	}
	return expectOne(res)
}

func expectOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) TouchUser(ctx context.Context, q DBTX, id int64) error {
	_, err := s.q(q).ExecContext(ctx, `UPDATE users SET updated_at = ? WHERE id = ?`, Now(), id)
	return err
}

func (s *Store) AdminUser(ctx context.Context) (*domain.User, error) {
	return s.scanUserQuery(ctx, `SELECT `+userColumns+` FROM users WHERE role = 'admin' AND is_active = 1 ORDER BY id LIMIT 1`)
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}
