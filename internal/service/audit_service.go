package service

import (
	"context"

	"skarb/internal/domain"
	"skarb/internal/store"
)

func (s *Service) ListAudit(ctx context.Context, actor domain.Actor, f store.AuditFilter) ([]store.AuditRow, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	return s.Store.ListAudit(ctx, f)
}
