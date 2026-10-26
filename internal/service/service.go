package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/store"
)

type Service struct {
	Store *store.Store
	FX    *fx.Service
	Cfg   config.Config
	Loc   *time.Location
	Now   func() time.Time
}

func New(st *store.Store, fxSvc *fx.Service, cfg config.Config) (*Service, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone: %w", err)
	}
	return &Service{Store: st, FX: fxSvc, Cfg: cfg, Loc: loc, Now: time.Now}, nil
}

func (s *Service) Today() domain.Date {
	return domain.TodayIn(s.Loc, s.Now())
}

func (s *Service) RequireAuth(actor domain.Actor) error {
	if actor.Kind == domain.SourceSystem {
		return nil
	}
	if actor.UserID == 0 {
		return domain.ErrUnauthorized
	}
	return nil
}

func (s *Service) RequireAdmin(actor domain.Actor) error {
	if actor.Kind == domain.SourceSystem {
		return nil
	}
	if actor.UserID == 0 {
		return domain.ErrUnauthorized
	}
	if actor.Role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Service) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) audit(ctx context.Context, q store.DBTX, actor domain.Actor, action, entityType, entityUID string, before, after any) error {
	kind := actor.Kind
	if kind == "" {
		kind = domain.SourceSystem
	}
	return s.Store.InsertAudit(ctx, q, store.AuditEntry{
		Actor: domain.Actor{
			Kind:           kind,
			UserID:         actor.UserID,
			TokenUID:       actor.TokenUID,
			TelegramUserID: actor.TelegramUserID,
			ChatID:         actor.ChatID,
			RequestID:      actor.RequestID,
			IP:             actor.IP,
			UserAgent:      actor.UserAgent,
		},
		Action:     action,
		EntityType: entityType,
		EntityUID:  entityUID,
		BeforeJSON: auditJSON(before),
		AfterJSON:  auditJSON(after),
	})
}

func auditJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func validation(field, message string) error {
	return &domain.ValidationError{Field: field, Message: message}
}

func wrapNotFound(entity string, err error) error {
	if err == nil {
		return nil
	}
	if err == domain.ErrNotFound {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, entity)
	}
	return err
}

func (s *Service) SystemActor() domain.Actor {
	return domain.Actor{Kind: domain.SourceSystem, Role: domain.RoleAdmin}
}

func (s *Service) ActorForUser(u *domain.User, kind domain.Source) domain.Actor {
	return domain.Actor{
		Kind:    kind,
		UserID:  u.ID,
		UserUID: u.UID,
		Role:    u.Role,
	}
}

func (s *Service) userByID(ctx context.Context, id int64) (*domain.User, error) {
	return s.Store.GetUserByID(ctx, id)
}

func (s *Service) userByUID(ctx context.Context, uid string) (*domain.User, error) {
	return s.Store.GetUserByUID(ctx, uid)
}

func (s *Service) resolveOwner(ctx context.Context, actor domain.Actor, requested *int64) (int64, error) {
	if requested == nil || *requested == 0 {
		if actor.UserID == 0 {
			return 0, validation("owner", "owner is required")
		}
		return actor.UserID, nil
	}
	if actor.Role != domain.RoleAdmin && *requested != actor.UserID {
		return 0, domain.ErrForbidden
	}
	if _, err := s.Store.GetUserByID(ctx, *requested); err != nil {
		return 0, validation("owner", "owner not found")
	}
	return *requested, nil
}

func (s *Service) activeUser(ctx context.Context, id int64) (*domain.User, error) {
	u, err := s.Store.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.IsActive {
		return nil, domain.ErrForbidden
	}
	return u, nil
}
