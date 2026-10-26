package service

import (
	"context"
	"database/sql"
	"regexp"
	"strings"

	"skarb/internal/domain"
	"skarb/internal/store"
)

var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Service) ListCategories(ctx context.Context, actor domain.Actor, includeArchived bool) ([]domain.Category, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if includeArchived && actor.Role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListCategories(ctx, includeArchived)
}

func (s *Service) CategoryByUID(ctx context.Context, actor domain.Actor, uid string) (*domain.Category, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	return s.Store.GetCategoryByUID(ctx, uid)
}

func (s *Service) CreateCategory(ctx context.Context, actor domain.Actor, in store.CreateCategoryInput) (*domain.Category, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if err := validateCategoryInput(in.Name, in.Kind, in.Color, in.Emoji, in.Aliases); err != nil {
		return nil, err
	}
	var cat *domain.Category
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		cat, err = s.Store.CreateCategory(ctx, tx, in)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "category.created", "category", cat.UID, nil, safeCategory(cat))
	})
	if err != nil {
		return nil, err
	}
	return cat, nil
}

func (s *Service) UpdateCategory(ctx context.Context, actor domain.Actor, uid string, in store.UpdateCategoryInput) (*domain.Category, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	before, err := s.Store.GetCategoryByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		if err := validateCategoryInput(*in.Name, before.Kind, "", "", nil); err != nil {
			return nil, err
		}
	}
	if in.Color != nil && *in.Color != "" && !colorRE.MatchString(*in.Color) {
		return nil, validation("color", "color must be #RRGGBB")
	}
	if in.Aliases != nil {
		if err := validateAliases(*in.Aliases); err != nil {
			return nil, err
		}
	}
	var cat *domain.Category
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		cat, err = s.Store.UpdateCategory(ctx, tx, before.ID, in)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "category.updated", "category", cat.UID, safeCategory(before), safeCategory(cat))
	})
	if err != nil {
		return nil, err
	}
	return cat, nil
}

func (s *Service) ArchiveCategory(ctx context.Context, actor domain.Actor, uid string, archived bool) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	cat, err := s.Store.GetCategoryByUID(ctx, uid)
	if err != nil {
		return err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.ArchiveCategory(ctx, tx, cat.ID, archived); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "category.archived", "category", cat.UID,
			safeCategory(cat), map[string]any{"archived": archived})
	})
	return err
}

func (s *Service) DeleteCategory(ctx context.Context, actor domain.Actor, uid, reassignToUID string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	cat, err := s.Store.GetCategoryByUID(ctx, uid)
	if err != nil {
		return err
	}
	var reassignTo *int64
	if strings.TrimSpace(reassignToUID) != "" {
		target, err := s.Store.GetCategoryByUID(ctx, reassignToUID)
		if err != nil {
			return err
		}
		reassignTo = &target.ID
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.DeleteCategory(ctx, tx, cat.ID, reassignTo); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "category.deleted", "category", cat.UID,
			safeCategory(cat), map[string]any{"reassigned_to": reassignToUID})
	})
	return err
}

func (s *Service) resolveCategory(ctx context.Context, ref string, kind *domain.Kind) (*domain.Category, error) {
	return s.Store.ResolveCategory(ctx, ref, kind)
}

func validateCategoryInput(name string, kind domain.Kind, color, emoji string, aliases []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return validation("name", "name is required")
	}
	if len([]rune(name)) > 80 {
		return validation("name", "name is too long")
	}
	if kind != domain.KindExpense && kind != domain.KindIncome {
		return validation("kind", "kind must be income or expense")
	}
	if color != "" && !colorRE.MatchString(color) {
		return validation("color", "color must be #RRGGBB")
	}
	if len([]rune(emoji)) > 16 {
		return validation("emoji", "emoji is too long")
	}
	return validateAliases(aliases)
}

func validateAliases(aliases []string) error {
	if len(aliases) > 50 {
		return validation("aliases", "too many aliases")
	}
	for _, alias := range aliases {
		if len([]rune(strings.TrimSpace(alias))) > 80 {
			return validation("aliases", "alias is too long")
		}
	}
	return nil
}

func safeCategory(c *domain.Category) map[string]any {
	if c == nil {
		return nil
	}
	return map[string]any{
		"uid":     c.UID,
		"name":    c.Name,
		"kind":    c.Kind,
		"color":   c.Color,
		"emoji":   c.Emoji,
		"sort":    c.SortOrder,
		"aliases": c.Aliases,
	}
}
