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

const transactionColumns = `t.id, t.uid, t.kind, t.amount_minor, t.currency, t.occurred_on,
	t.category_id, c.name, c.emoji, c.color,
	t.owner_user_id, u.display_name,
	t.description, t.note, t.original_text, t.source,
	t.created_by_user_id, t.updated_by_user_id, t.created_at, t.updated_at,
	t.deleted_at, t.deleted_by_user_id, t.delete_reason, t.replaces_id`

const transactionJoins = ` FROM transactions t
	JOIN categories c ON c.id = t.category_id
	JOIN users u ON u.id = t.owner_user_id`

type TransactionFilter struct {
	From           *domain.Date
	To             *domain.Date
	OwnerUserID    *int64
	CategoryID     *int64
	Kind           *domain.Kind
	Source         *domain.Source
	Search         string
	IncludeDeleted bool
	Limit          int
	Offset         int
	OrderBy        string
}

type CreateTransactionInput struct {
	Kind            domain.Kind
	AmountMinor     int64
	Currency        string
	OccurredOn      domain.Date
	CategoryID      int64
	OwnerUserID     int64
	Description     string
	Note            string
	OriginalText    string
	Source          domain.Source
	CreatedByUserID *int64
	UpdatedByUserID *int64
	ReplacesID      *int64
}

type UpdateTransactionInput struct {
	AmountMinor     *int64
	Currency        *string
	OccurredOn      *domain.Date
	CategoryID      *int64
	Kind            *domain.Kind
	OwnerUserID     *int64
	Description     *string
	Note            *string
	OriginalText    *string
	UpdatedByUserID *int64
}

func (s *Store) CreateTransaction(ctx context.Context, q DBTX, in CreateTransactionInput) (*domain.Transaction, error) {
	now := Now()
	res, err := s.q(q).ExecContext(ctx,
		`INSERT INTO transactions(uid, kind, amount_minor, currency, occurred_on, category_id,
		 owner_user_id, description, note, original_text, source, created_by_user_id,
		 updated_by_user_id, created_at, updated_at, replaces_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), string(in.Kind), in.AmountMinor, in.Currency, in.OccurredOn.String(),
		in.CategoryID, in.OwnerUserID, in.Description, in.Note, in.OriginalText, string(in.Source),
		nullInt64(in.CreatedByUserID), nullInt64(in.UpdatedByUserID), now, now, nullInt64(in.ReplacesID),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.getTransactionByIDQ(ctx, q, id)
}

func (s *Store) GetTransactionByID(ctx context.Context, id int64) (*domain.Transaction, error) {
	return s.getTransactionByIDQ(ctx, nil, id)
}

func (s *Store) getTransactionByIDQ(ctx context.Context, q DBTX, id int64) (*domain.Transaction, error) {
	return s.scanTransactionQueryQ(ctx, q, `SELECT `+transactionColumns+transactionJoins+` WHERE t.id = ?`, id)
}

func (s *Store) GetTransactionByUID(ctx context.Context, uid string) (*domain.Transaction, error) {
	return s.scanTransactionQuery(ctx, `SELECT `+transactionColumns+transactionJoins+` WHERE t.uid = ?`, uid)
}

func (s *Store) scanTransactionQuery(ctx context.Context, query string, args ...any) (*domain.Transaction, error) {
	return s.scanTransactionQueryQ(ctx, nil, query, args...)
}

func (s *Store) scanTransactionQueryQ(ctx context.Context, q DBTX, query string, args ...any) (*domain.Transaction, error) {
	row := s.q(q).QueryRowContext(ctx, query, args...)
	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

func scanTransaction(row rowScanner) (*domain.Transaction, error) {
	var t domain.Transaction
	var kind, occurred, source, created, updated string
	var createdBy, updatedBy, deletedBy, replaces sql.NullInt64
	var deleted sql.NullString
	if err := row.Scan(&t.ID, &t.UID, &kind, &t.AmountMinor, &t.Currency, &occurred,
		&t.CategoryID, &t.CategoryName, &t.CategoryEmoji, &t.CategoryColor,
		&t.OwnerUserID, &t.OwnerName, &t.Description, &t.Note, &t.OriginalText, &source,
		&createdBy, &updatedBy, &created, &updated, &deleted, &deletedBy,
		&t.DeleteReason, &replaces); err != nil {
		return nil, err
	}
	t.Kind = domain.Kind(kind)
	t.Source = domain.Source(source)
	t.CreatedByUserID = int64Ptr(createdBy)
	t.UpdatedByUserID = int64Ptr(updatedBy)
	t.DeletedByUserID = int64Ptr(deletedBy)
	t.ReplacesID = int64Ptr(replaces)
	var err error
	if t.OccurredOn, err = domain.ParseDate(occurred); err != nil {
		return nil, err
	}
	if t.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if t.UpdatedAt, err = scanTime(updated); err != nil {
		return nil, err
	}
	if t.DeletedAt, err = timePtr(deleted); err != nil {
		return nil, err
	}
	return &t, nil
}

func transactionWhere(f TransactionFilter) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if !f.IncludeDeleted {
		where = append(where, "t.deleted_at IS NULL")
	}
	if f.From != nil {
		where = append(where, "t.occurred_on >= ?")
		args = append(args, f.From.String())
	}
	if f.To != nil {
		where = append(where, "t.occurred_on <= ?")
		args = append(args, f.To.String())
	}
	if f.OwnerUserID != nil {
		where = append(where, "t.owner_user_id = ?")
		args = append(args, *f.OwnerUserID)
	}
	if f.CategoryID != nil {
		where = append(where, "t.category_id = ?")
		args = append(args, *f.CategoryID)
	}
	if f.Kind != nil {
		where = append(where, "t.kind = ?")
		args = append(args, string(*f.Kind))
	}
	if f.Source != nil {
		where = append(where, "t.source = ?")
		args = append(args, string(*f.Source))
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		where = append(where, `(t.description LIKE ? OR t.note LIKE ? OR t.original_text LIKE ? OR c.name LIKE ?)`)
		like := "%" + term + "%"
		args = append(args, like, like, like, like)
	}
	return strings.Join(where, " AND "), args
}

func transactionOrder(order string) string {
	switch order {
	case "date_asc":
		return "t.occurred_on ASC, t.id ASC"
	case "amount_desc":
		return "t.amount_minor DESC, t.id DESC"
	case "amount_asc":
		return "t.amount_minor ASC, t.id ASC"
	default:
		return "t.occurred_on DESC, t.id DESC"
	}
}

func (s *Store) ListTransactions(ctx context.Context, f TransactionFilter) ([]domain.Transaction, error) {
	where, args := transactionWhere(f)
	query := `SELECT ` + transactionColumns + transactionJoins + ` WHERE ` + where +
		` ORDER BY ` + transactionOrder(f.OrderBy)
	if f.Limit != -1 {
		limit := f.Limit
		if limit <= 0 {
			limit = 100
		}
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, f.Offset)
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) ListTransactionsForStats(ctx context.Context, f TransactionFilter) ([]domain.Transaction, error) {
	f.Limit = -1
	return s.ListTransactions(ctx, f)
}

func (s *Store) CountTransactions(ctx context.Context, f TransactionFilter) (int, error) {
	where, args := transactionWhere(f)
	query := `SELECT count(*)` + transactionJoins + ` WHERE ` + where
	var n int
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func (s *Store) UpdateTransaction(ctx context.Context, q DBTX, id int64, in UpdateTransactionInput) (*domain.Transaction, error) {
	sets := []string{}
	args := []any{}
	if in.AmountMinor != nil {
		sets = append(sets, "amount_minor = ?")
		args = append(args, *in.AmountMinor)
	}
	if in.Currency != nil {
		sets = append(sets, "currency = ?")
		args = append(args, *in.Currency)
	}
	if in.OccurredOn != nil {
		sets = append(sets, "occurred_on = ?")
		args = append(args, in.OccurredOn.String())
	}
	if in.CategoryID != nil {
		sets = append(sets, "category_id = ?")
		args = append(args, *in.CategoryID)
	}
	if in.Kind != nil {
		sets = append(sets, "kind = ?")
		args = append(args, string(*in.Kind))
	}
	if in.OwnerUserID != nil {
		sets = append(sets, "owner_user_id = ?")
		args = append(args, *in.OwnerUserID)
	}
	if in.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *in.Description)
	}
	if in.Note != nil {
		sets = append(sets, "note = ?")
		args = append(args, *in.Note)
	}
	if in.OriginalText != nil {
		sets = append(sets, "original_text = ?")
		args = append(args, *in.OriginalText)
	}
	if len(sets) == 0 {
		return s.getTransactionByIDQ(ctx, q, id)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, Now())
	if in.UpdatedByUserID != nil {
		sets = append(sets, "updated_by_user_id = ?")
		args = append(args, *in.UpdatedByUserID)
	}
	args = append(args, id)
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE transactions SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return nil, err
	}
	if err := expectOne(res); err != nil {
		return nil, err
	}
	return s.getTransactionByIDQ(ctx, q, id)
}

func (s *Store) SoftDeleteTransaction(ctx context.Context, q DBTX, id, byUserID int64, reason string) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE transactions SET deleted_at = ?, deleted_by_user_id = ?, delete_reason = ?, updated_at = ?
		 WHERE id = ? AND deleted_at IS NULL`,
		Now(), nullableActorID(byUserID), reason, Now(), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) RestoreTransaction(ctx context.Context, q DBTX, id, byUserID int64) error {
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE transactions SET deleted_at = NULL, deleted_by_user_id = NULL, delete_reason = '',
		 updated_at = ?, updated_by_user_id = ? WHERE id = ? AND deleted_at IS NOT NULL`,
		Now(), nullableActorID(byUserID), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) CountTransactionsInRange(ctx context.Context, from, to domain.Date) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM transactions
		 WHERE deleted_at IS NULL AND occurred_on >= ? AND occurred_on <= ?`,
		from.String(), to.String(),
	).Scan(&n)
	return n, err
}

func (s *Store) TransactionRoot(ctx context.Context, id int64) (int64, error) {
	seen := map[int64]bool{}
	cur := id
	for i := 0; i < 100; i++ {
		if seen[cur] {
			return 0, fmt.Errorf("transaction replacement cycle at %d", cur)
		}
		seen[cur] = true
		t, err := s.GetTransactionByID(ctx, cur)
		if err != nil {
			return 0, err
		}
		if t.ReplacesID == nil {
			return t.ID, nil
		}
		cur = *t.ReplacesID
	}
	return 0, fmt.Errorf("transaction replacement chain too long")
}

func (s *Store) ListTransactionVersions(ctx context.Context, uid string) ([]domain.Transaction, error) {
	current, err := s.GetTransactionByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	root, err := s.TransactionRoot(ctx, current.ID)
	if err != nil {
		return nil, err
	}
	query := `WITH RECURSIVE versions AS (
		SELECT t.* FROM transactions t WHERE t.id = ?
		UNION ALL
		SELECT t.* FROM transactions t JOIN versions v ON t.replaces_id = v.id
	)
	SELECT ` + transactionColumns + ` FROM versions t
	JOIN categories c ON c.id = t.category_id
	JOIN users u ON u.id = t.owner_user_id
	ORDER BY t.id`
	rows, err := s.DB.QueryContext(ctx, query, root)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}
