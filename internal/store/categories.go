package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"skarb/internal/domain"
)

const categoryColumns = `id, uid, name, kind, color, emoji, sort_order, archived_at, created_at, updated_at`

type CreateCategoryInput struct {
	Name      string
	Kind      domain.Kind
	Color     string
	Emoji     string
	SortOrder int
	Aliases   []string
}

type UpdateCategoryInput struct {
	Name      *string
	Color     *string
	Emoji     *string
	SortOrder *int
	Aliases   *[]string
}

func (s *Store) ListCategories(ctx context.Context, includeArchived bool) ([]domain.Category, error) {
	query := `SELECT ` + categoryColumns + ` FROM categories`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
	}
	query += ` ORDER BY kind, sort_order, name`
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	aliases, err := s.allAliases(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Aliases = aliases[out[i].ID]
	}
	return out, nil
}

func (s *Store) GetCategoryByID(ctx context.Context, id int64) (*domain.Category, error) {
	return s.getCategoryByIDQ(ctx, nil, id)
}

func (s *Store) getCategoryByIDQ(ctx context.Context, q DBTX, id int64) (*domain.Category, error) {
	c, err := s.scanCategoryQueryQ(ctx, q, `SELECT `+categoryColumns+` FROM categories WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	c.Aliases, err = s.aliasesForQ(ctx, q, id)
	return c, err
}

func (s *Store) GetCategoryByUID(ctx context.Context, uid string) (*domain.Category, error) {
	c, err := s.scanCategoryQuery(ctx, `SELECT `+categoryColumns+` FROM categories WHERE uid = ?`, uid)
	if err != nil {
		return nil, err
	}
	c.Aliases, err = s.aliasesFor(ctx, c.ID)
	return c, err
}

func (s *Store) scanCategoryQuery(ctx context.Context, query string, args ...any) (*domain.Category, error) {
	return s.scanCategoryQueryQ(ctx, nil, query, args...)
}

func (s *Store) scanCategoryQueryQ(ctx context.Context, q DBTX, query string, args ...any) (*domain.Category, error) {
	row := s.q(q).QueryRowContext(ctx, query, args...)
	c, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func scanCategory(row rowScanner) (*domain.Category, error) {
	var c domain.Category
	var kind string
	var archived sql.NullString
	var created, updated string
	if err := row.Scan(&c.ID, &c.UID, &c.Name, &kind, &c.Color, &c.Emoji,
		&c.SortOrder, &archived, &created, &updated); err != nil {
		return nil, err
	}
	c.Kind = domain.Kind(kind)
	var err error
	if c.ArchivedAt, err = timePtr(archived); err != nil {
		return nil, err
	}
	if c.CreatedAt, err = scanTime(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = scanTime(updated); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) allAliases(ctx context.Context) (map[int64][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT category_id, alias_text FROM category_aliases ORDER BY alias_text`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var alias string
		if err := rows.Scan(&id, &alias); err != nil {
			return nil, err
		}
		out[id] = append(out[id], alias)
	}
	return out, rows.Err()
}

func (s *Store) aliasesFor(ctx context.Context, categoryID int64) ([]string, error) {
	return s.aliasesForQ(ctx, nil, categoryID)
}

func (s *Store) aliasesForQ(ctx context.Context, q DBTX, categoryID int64) ([]string, error) {
	rows, err := s.q(q).QueryContext(ctx, `SELECT alias_text FROM category_aliases WHERE category_id = ? ORDER BY alias_text`, categoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		out = append(out, alias)
	}
	return out, rows.Err()
}

// ResolveCategory resolves an exact UID, name, or alias to an active category.
func (s *Store) ResolveCategory(ctx context.Context, ref string, kind *domain.Kind) (*domain.Category, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, &domain.ValidationError{Field: "category", Message: "category is required"}
	}
	// UID first.
	if c, err := s.GetCategoryByUID(ctx, ref); err == nil {
		if c.Archived() {
			return nil, domain.ErrNotFound
		}
		if kind != nil && c.Kind != *kind {
			return nil, domain.ErrNotFound
		}
		return c, nil
	}
	// Numeric database IDs are accepted for server-rendered forms.
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		if c, err := s.GetCategoryByID(ctx, id); err == nil {
			if c.Archived() {
				return nil, domain.ErrNotFound
			}
			if kind != nil && c.Kind != *kind {
				return nil, domain.ErrNotFound
			}
			return c, nil
		}
	}
	query := `SELECT ` + categoryColumns + ` FROM categories WHERE name = ? COLLATE NOCASE AND archived_at IS NULL`
	args := []any{ref}
	if kind != nil {
		query += ` AND kind = ?`
		args = append(args, string(*kind))
	}
	query += ` LIMIT 1`
	c, err := s.scanCategoryQuery(ctx, query, args...)
	if err == nil {
		c.Aliases, _ = s.aliasesFor(ctx, c.ID)
		return c, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	norm := NormalizeAlias(ref)
	query = `SELECT ` + categoryColumns + ` FROM categories c
		JOIN category_aliases ca ON ca.category_id = c.id
		WHERE ca.alias_norm = ? AND c.archived_at IS NULL`
	args = []any{norm}
	if kind != nil {
		query += ` AND c.kind = ?`
		args = append(args, string(*kind))
	}
	query += ` LIMIT 1`
	c, err = s.scanCategoryQuery(ctx, query, args...)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("%w: category %q", domain.ErrNotFound, ref)
	}
	if err != nil {
		return nil, err
	}
	c.Aliases, _ = s.aliasesFor(ctx, c.ID)
	return c, nil
}

func (s *Store) CreateCategory(ctx context.Context, q DBTX, in CreateCategoryInput) (*domain.Category, error) {
	return s.createCategory(ctx, s.q(q), in)
}

func (s *Store) createCategory(ctx context.Context, q DBTX, in CreateCategoryInput) (*domain.Category, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: "name is required"}
	}
	if in.Kind != domain.KindExpense && in.Kind != domain.KindIncome {
		return nil, &domain.ValidationError{Field: "kind", Message: "kind must be income or expense"}
	}
	if in.Color == "" {
		in.Color = "#64748b"
	}
	now := Now()
	res, err := q.ExecContext(ctx,
		`INSERT INTO categories(uid, name, kind, color, emoji, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), in.Name, string(in.Kind), in.Color, in.Emoji, in.SortOrder, now, now,
	)
	if err != nil {
		return nil, conflict(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := s.replaceAliases(ctx, q, id, in.Aliases); err != nil {
		return nil, err
	}
	return s.getCategoryByIDQ(ctx, q, id)
}

func (s *Store) UpdateCategory(ctx context.Context, q DBTX, id int64, in UpdateCategoryInput) (*domain.Category, error) {
	runner := s.q(q)
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
	if in.Color != nil {
		sets = append(sets, "color = ?")
		args = append(args, *in.Color)
	}
	if in.Emoji != nil {
		sets = append(sets, "emoji = ?")
		args = append(args, *in.Emoji)
	}
	if in.SortOrder != nil {
		sets = append(sets, "sort_order = ?")
		args = append(args, *in.SortOrder)
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, Now(), id)
		res, err := runner.ExecContext(ctx,
			`UPDATE categories SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
		if err != nil {
			return nil, conflict(err)
		}
		if err := expectOne(res); err != nil {
			return nil, err
		}
	}
	if in.Aliases != nil {
		if err := s.replaceAliases(ctx, runner, id, *in.Aliases); err != nil {
			return nil, err
		}
	}
	return s.getCategoryByIDQ(ctx, q, id)
}

func (s *Store) replaceAliases(ctx context.Context, q DBTX, categoryID int64, aliases []string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM category_aliases WHERE category_id = ?`, categoryID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, alias := range aliases {
		norm := NormalizeAlias(alias)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		if _, err := q.ExecContext(ctx,
			`INSERT INTO category_aliases(alias_norm, alias_text, category_id) VALUES (?, ?, ?)`,
			norm, strings.TrimSpace(alias), categoryID,
		); err != nil {
			return conflict(err)
		}
	}
	return nil
}

func (s *Store) ArchiveCategory(ctx context.Context, q DBTX, id int64, archived bool) error {
	var value any
	if archived {
		value = Now()
	}
	res, err := s.q(q).ExecContext(ctx,
		`UPDATE categories SET archived_at = ?, updated_at = ? WHERE id = ?`,
		value, Now(), id,
	)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) CategoryUsage(ctx context.Context, q DBTX, id int64) (int, error) {
	var n int
	err := s.q(q).QueryRowContext(ctx, `SELECT count(*) FROM transactions WHERE category_id = ?`, id).Scan(&n)
	return n, err
}

// DeleteCategory deletes a category. If it is used, reassignTo must be a category
// of the same kind; otherwise ErrConflict is returned.
func (s *Store) DeleteCategory(ctx context.Context, q DBTX, id int64, reassignTo *int64) error {
	runner := s.q(q)
	cat, err := s.getCategoryByIDQ(ctx, q, id)
	if err != nil {
		return err
	}
	usage, err := s.CategoryUsage(ctx, runner, id)
	if err != nil {
		return err
	}
	if usage > 0 {
		if reassignTo == nil {
			return fmt.Errorf("%w: category is used by %d transactions", domain.ErrConflict, usage)
		}
		if *reassignTo == id {
			return &domain.ValidationError{Field: "reassign_to", Message: "cannot reassign to the same category"}
		}
		target, err := s.getCategoryByIDQ(ctx, q, *reassignTo)
		if err != nil {
			return err
		}
		if target.Kind != cat.Kind {
			return &domain.ValidationError{Field: "reassign_to", Message: "target category must have the same kind"}
		}
		if _, err := runner.ExecContext(ctx,
			`UPDATE transactions SET category_id = ?, updated_at = ? WHERE category_id = ?`,
			target.ID, Now(), id,
		); err != nil {
			return err
		}
	}
	res, err := runner.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOne(res)
}

func (s *Store) CategoryCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM categories`).Scan(&n)
	return n, err
}
