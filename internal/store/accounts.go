package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"skarb/internal/domain"
)

const accountColumns = `a.id, a.uid, a.owner_user_id, u.display_name, a.name, a.currency,
	a.kind, a.include_in_net_worth, a.sort_order, a.archived_at, a.created_at, a.updated_at`

const accountJoins = ` FROM accounts a JOIN users u ON u.id = a.owner_user_id`

const snapshotColumns = `s.id, s.uid, s.account_id, s.snapshot_date, s.balance_minor, s.note,
	s.created_by_user_id, s.updated_by_user_id, s.created_at, s.updated_at,
	s.deleted_at, s.deleted_by_user_id`

type CreateAccountInput struct {
	OwnerUserID       int64
	Name              string
	Currency          string
	Kind              domain.AccountKind
	IncludeInNetWorth bool
	SortOrder         int
}

type UpdateAccountInput struct {
	Name              *string
	Currency          *string
	Kind              *domain.AccountKind
	IncludeInNetWorth *bool
	SortOrder         *int
	Archived          *bool
}

type SnapshotInput struct {
	AccountID       int64
	SnapshotDate    domain.Date
	BalanceMinor    int64
	Note            string
	CreatedByUserID *int64
	UpdatedByUserID *int64
}

type AccountSnapshot struct {
	Account  domain.Account
	Snapshot domain.Snapshot
}

func (s *Store) CreateAccount(ctx context.Context, q DBTX, in CreateAccountInput) (*domain.Account, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: "name is required"}
	}
	if in.Kind == "" {
		in.Kind = domain.AccountOther
	}
	switch in.Kind {
	case domain.AccountBank, domain.AccountCash, domain.AccountInvestment, domain.AccountOther:
	default:
		return nil, &domain.ValidationError{Field: "kind", Message: "invalid account kind"}
	}
	now := Now()
	res, err := s.q(q).ExecContext(ctx,
		`INSERT INTO accounts(uid, owner_user_id, name, currency, kind, include_in_net_worth,
		 sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), in.OwnerUserID, in.Name, strings.ToUpper(in.Currency), string(in.Kind),
		boolInt(in.IncludeInNetWorth), in.SortOrder, now, now,
	)
	if err != nil {
		return nil, conflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.getAccountByIDQ(ctx, q, id)
}

func (s *Store) GetAccountByID(ctx context.Context, id int64) (*domain.Account, error) {
	return s.getAccountByIDQ(ctx, nil, id)
}

func (s *Store) getAccountByIDQ(ctx context.Context, q DBTX, id int64) (*domain.Account, error) {
	return s.scanAccountQueryQ(ctx, q, `SELECT `+accountColumns+accountJoins+` WHERE a.id = ?`, id)
}

func (s *Store) GetAccountByUID(ctx context.Context, uid string) (*domain.Account, error) {
	return s.scanAccountQuery(ctx, `SELECT `+accountColumns+accountJoins+` WHERE a.uid = ?`, uid)
}

func (s *Store) scanAccountQuery(ctx context.Context, query string, args ...any) (*domain.Account, error) {
	return s.scanAccountQueryQ(ctx, nil, query, args...)
}

func (s *Store) scanAccountQueryQ(ctx context.Context, q DBTX, query string, args ...any) (*domain.Account, error) {
	row := s.q(q).QueryRowContext(ctx, query, args...)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return a, err
}

func scanAccount(row rowScanner) (*domain.Account, error) {
	var a domain.Account
	var kind string
	var include int
	var archived sql.NullString
	var created, updated string
	if err := row.Scan(&a.ID, &a.UID, &a.OwnerUserID, &a.OwnerName, &a.Name, &a.Currency,
		&kind, &include, &a.SortOrder, &archived, &created, &updated); err != nil {
		return nil, err
	}
	a.Kind = domain.AccountKind(kind)
	a.IncludeInNetWorth = include != 0
	var err error
	if a.ArchivedAt, err = timePtr(archived); err != nil {
		return nil, err
	}
	if a.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if a.UpdatedAt, err = scanTime(updated); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListAccounts(ctx context.Context, includeArchived bool) ([]domain.Account, error) {
	query := `SELECT ` + accountColumns + accountJoins
	if !includeArchived {
		query += ` WHERE a.archived_at IS NULL`
	}
	query += ` ORDER BY a.owner_user_id, a.sort_order, a.name`
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAccount(ctx context.Context, q DBTX, id int64, in UpdateAccountInput) (*domain.Account, error) {
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, &domain.ValidationError{Field: "name", Message: "name is required"}
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}
	if in.Currency != nil {
		sets = append(sets, "currency = ?")
		args = append(args, strings.ToUpper(*in.Currency))
	}
	if in.Kind != nil {
		switch *in.Kind {
		case domain.AccountBank, domain.AccountCash, domain.AccountInvestment, domain.AccountOther:
		default:
			return nil, &domain.ValidationError{Field: "kind", Message: "invalid account kind"}
		}
		sets = append(sets, "kind = ?")
		args = append(args, string(*in.Kind))
	}
	if in.IncludeInNetWorth != nil {
		sets = append(sets, "include_in_net_worth = ?")
		args = append(args, boolInt(*in.IncludeInNetWorth))
	}
	if in.SortOrder != nil {
		sets = append(sets, "sort_order = ?")
		args = append(args, *in.SortOrder)
	}
	if in.Archived != nil {
		if *in.Archived {
			sets = append(sets, "archived_at = ?")
			args = append(args, Now())
		} else {
			sets = append(sets, "archived_at = NULL")
		}
	}
	if len(sets) == 0 {
		return s.getAccountByIDQ(ctx, q, id)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, Now(), id)
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE accounts SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return nil, conflict(err)
	}
	if err := expectOne(res); err != nil {
		return nil, err
	}
	return s.getAccountByIDQ(ctx, q, id)
}

func (s *Store) AccountSnapshotCount(ctx context.Context, q DBTX, accountID int64) (int, error) {
	var n int
	err := s.q(q).QueryRowContext(ctx, `SELECT count(*) FROM account_snapshots WHERE account_id = ?`, accountID).Scan(&n)
	return n, err
}

func (s *Store) DeleteAccount(ctx context.Context, q DBTX, id int64) error {
	n, err := s.AccountSnapshotCount(ctx, q, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: account has snapshots; archive it instead", domain.ErrConflict)
	}
	res, err := s.q(q).ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) UpsertSnapshot(ctx context.Context, q DBTX, in SnapshotInput) (*domain.Snapshot, error) {
	if in.AccountID == 0 {
		return nil, &domain.ValidationError{Field: "account", Message: "account is required"}
	}
	if in.SnapshotDate.IsZero() {
		return nil, &domain.ValidationError{Field: "date", Message: "date is required"}
	}
	if in.BalanceMinor < 0 {
		return nil, &domain.ValidationError{Field: "amount", Message: "balance cannot be negative"}
	}
	runner := s.q(q)
	now := Now()
	res, err := runner.ExecContext(ctx,
		`UPDATE account_snapshots SET balance_minor = ?, note = ?, updated_by_user_id = ?,
		 updated_at = ?, deleted_at = NULL, deleted_by_user_id = NULL
		 WHERE account_id = ? AND snapshot_date = ? AND deleted_at IS NULL`,
		in.BalanceMinor, in.Note, nullInt64(in.UpdatedByUserID), now, in.AccountID, in.SnapshotDate.String(),
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		res, err = runner.ExecContext(ctx,
			`INSERT INTO account_snapshots(uid, account_id, snapshot_date, balance_minor, note,
			 created_by_user_id, updated_by_user_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), in.AccountID, in.SnapshotDate.String(), in.BalanceMinor, in.Note,
			nullInt64(in.CreatedByUserID), nullInt64(in.UpdatedByUserID), now, now,
		)
		if err != nil {
			return nil, conflict(err)
		}
	}
	return s.getSnapshotByAccountDateQ(ctx, q, in.AccountID, in.SnapshotDate)
}

func (s *Store) getSnapshotByAccountDateQ(ctx context.Context, q DBTX, accountID int64, date domain.Date) (*domain.Snapshot, error) {
	row := s.q(q).QueryRowContext(ctx,
		`SELECT `+snapshotColumns+` FROM account_snapshots s
		 WHERE s.account_id = ? AND s.snapshot_date = ? AND s.deleted_at IS NULL`, accountID, date.String())
	sn, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return sn, err
}

func (s *Store) GetSnapshotByUID(ctx context.Context, uid string) (*domain.Snapshot, error) {
	return s.scanSnapshotQuery(ctx, `SELECT `+snapshotColumns+` FROM account_snapshots s WHERE s.uid = ?`, uid)
}

func (s *Store) scanSnapshotQuery(ctx context.Context, query string, args ...any) (*domain.Snapshot, error) {
	row := s.DB.QueryRowContext(ctx, query, args...)
	sn, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return sn, err
}

func scanSnapshot(row rowScanner) (*domain.Snapshot, error) {
	var sn domain.Snapshot
	var date, created, updated string
	var createdBy, updatedBy, deletedBy sql.NullInt64
	var deleted sql.NullString
	if err := row.Scan(&sn.ID, &sn.UID, &sn.AccountID, &date, &sn.BalanceMinor, &sn.Note,
		&createdBy, &updatedBy, &created, &updated, &deleted, &deletedBy); err != nil {
		return nil, err
	}
	var err error
	if sn.SnapshotDate, err = domain.ParseDate(date); err != nil {
		return nil, err
	}
	sn.CreatedByUserID = int64Ptr(createdBy)
	sn.UpdatedByUserID = int64Ptr(updatedBy)
	sn.DeletedByUserID = int64Ptr(deletedBy)
	if sn.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if sn.UpdatedAt, err = scanTime(updated); err != nil {
		return nil, err
	}
	if sn.DeletedAt, err = timePtr(deleted); err != nil {
		return nil, err
	}
	return &sn, nil
}

func (s *Store) ListSnapshots(ctx context.Context, accountID int64, from, to *domain.Date, includeDeleted bool) ([]domain.Snapshot, error) {
	where := []string{"s.account_id = ?"}
	args := []any{accountID}
	if !includeDeleted {
		where = append(where, "s.deleted_at IS NULL")
	}
	if from != nil {
		where = append(where, "s.snapshot_date >= ?")
		args = append(args, from.String())
	}
	if to != nil {
		where = append(where, "s.snapshot_date <= ?")
		args = append(args, to.String())
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+snapshotColumns+` FROM account_snapshots s WHERE `+strings.Join(where, " AND ")+
			` ORDER BY s.snapshot_date DESC, s.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Snapshot
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sn)
	}
	return out, rows.Err()
}

func (s *Store) SoftDeleteSnapshot(ctx context.Context, q DBTX, id, byUserID int64) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE account_snapshots SET deleted_at = ?, deleted_by_user_id = ?, updated_at = ?
		 WHERE id = ? AND deleted_at IS NULL`,
		Now(), nullableActorID(byUserID), Now(), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) LatestSnapshots(ctx context.Context, asOf domain.Date) ([]AccountSnapshot, error) {
	query := `SELECT ` + accountColumns + `, ` + snapshotColumns + `
		FROM accounts a
		JOIN users u ON u.id = a.owner_user_id
		JOIN account_snapshots s ON s.id = (
			SELECT s2.id FROM account_snapshots s2
			WHERE s2.account_id = a.id AND s2.deleted_at IS NULL AND s2.snapshot_date <= ?
			ORDER BY s2.snapshot_date DESC, s2.id DESC LIMIT 1
		)
		WHERE a.archived_at IS NULL AND a.include_in_net_worth = 1
		ORDER BY a.owner_user_id, a.sort_order, a.name`
	rows, err := s.DB.QueryContext(ctx, query, asOf.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountSnapshot
	for rows.Next() {
		a, sn, err := scanAccountSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, AccountSnapshot{Account: *a, Snapshot: *sn})
	}
	return out, rows.Err()
}

func scanAccountSnapshot(row rowScanner) (*domain.Account, *domain.Snapshot, error) {
	var a domain.Account
	var kind string
	var include int
	var accountArchived sql.NullString
	var accountCreated, accountUpdated string
	var sn domain.Snapshot
	var date, snapCreated, snapUpdated string
	var createdBy, updatedBy, deletedBy sql.NullInt64
	var deleted sql.NullString
	if err := row.Scan(&a.ID, &a.UID, &a.OwnerUserID, &a.OwnerName, &a.Name, &a.Currency,
		&kind, &include, &a.SortOrder, &accountArchived, &accountCreated, &accountUpdated,
		&sn.ID, &sn.UID, &sn.AccountID, &date, &sn.BalanceMinor, &sn.Note,
		&createdBy, &updatedBy, &snapCreated, &snapUpdated, &deleted, &deletedBy); err != nil {
		return nil, nil, err
	}
	a.Kind = domain.AccountKind(kind)
	a.IncludeInNetWorth = include != 0
	var err error
	if a.ArchivedAt, err = timePtr(accountArchived); err != nil {
		return nil, nil, err
	}
	if a.CreatedAt, err = scanTime(accountCreated); err != nil {
		return nil, nil, err
	}
	if a.UpdatedAt, err = scanTime(accountUpdated); err != nil {
		return nil, nil, err
	}
	if sn.SnapshotDate, err = domain.ParseDate(date); err != nil {
		return nil, nil, err
	}
	sn.CreatedByUserID = int64Ptr(createdBy)
	sn.UpdatedByUserID = int64Ptr(updatedBy)
	sn.DeletedByUserID = int64Ptr(deletedBy)
	if sn.CreatedAt, err = scanTime(snapCreated); err != nil {
		return nil, nil, err
	}
	if sn.UpdatedAt, err = scanTime(snapUpdated); err != nil {
		return nil, nil, err
	}
	if sn.DeletedAt, err = timePtr(deleted); err != nil {
		return nil, nil, err
	}
	return &a, &sn, nil
}
