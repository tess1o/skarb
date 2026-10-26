package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/store"
)

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

type UpsertSnapshotInput struct {
	AccountUID string
	Date       string
	Amount     string
	Note       string
}

func (s *Service) ListAccounts(ctx context.Context, actor domain.Actor, includeArchived bool) ([]domain.Account, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if includeArchived && actor.Role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	return s.Store.ListAccounts(ctx, includeArchived)
}

func (s *Service) AccountByUID(ctx context.Context, actor domain.Actor, uid string) (*domain.Account, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	return s.Store.GetAccountByUID(ctx, uid)
}

func (s *Service) CreateAccount(ctx context.Context, actor domain.Actor, in CreateAccountInput) (*domain.Account, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	owner, err := s.Store.GetUserByID(ctx, in.OwnerUserID)
	if err != nil || !owner.IsActive {
		return nil, validation("owner", "owner not found or inactive")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, validation("name", "name is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if _, err := money.CurrencyFor(currency); err != nil {
		return nil, validation("currency", err.Error())
	}
	var account *domain.Account
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		account, err = s.Store.CreateAccount(ctx, tx, store.CreateAccountInput{
			OwnerUserID:       in.OwnerUserID,
			Name:              in.Name,
			Currency:          currency,
			Kind:              in.Kind,
			IncludeInNetWorth: in.IncludeInNetWorth,
			SortOrder:         in.SortOrder,
		})
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "account.created", "account", account.UID, nil, safeAccount(account))
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (s *Service) UpdateAccount(ctx context.Context, actor domain.Actor, uid string, in UpdateAccountInput) (*domain.Account, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	before, err := s.Store.GetAccountByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, validation("name", "name is required")
	}
	if in.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*in.Currency))
		if _, err := money.CurrencyFor(c); err != nil {
			return nil, validation("currency", err.Error())
		}
		in.Currency = &c
	}
	var account *domain.Account
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		account, err = s.Store.UpdateAccount(ctx, tx, before.ID, store.UpdateAccountInput{
			Name:              in.Name,
			Currency:          in.Currency,
			Kind:              in.Kind,
			IncludeInNetWorth: in.IncludeInNetWorth,
			SortOrder:         in.SortOrder,
			Archived:          in.Archived,
		})
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "account.updated", "account", account.UID, safeAccount(before), safeAccount(account))
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (s *Service) DeleteAccount(ctx context.Context, actor domain.Actor, uid string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	account, err := s.Store.GetAccountByUID(ctx, uid)
	if err != nil {
		return err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.DeleteAccount(ctx, tx, account.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "account.deleted", "account", account.UID, safeAccount(account), nil)
	})
	return err
}

func (s *Service) UpsertSnapshot(ctx context.Context, actor domain.Actor, in UpsertSnapshotInput) (*domain.Snapshot, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	account, err := s.Store.GetAccountByUID(ctx, in.AccountUID)
	if err != nil {
		return nil, err
	}
	if account.Archived() {
		return nil, validation("account", "account is archived")
	}
	date, err := parseFlexibleDate(in.Date)
	if err != nil {
		return nil, validation("date", err.Error())
	}
	if date.IsZero() {
		date = s.Today()
	}
	if date.Before(domain.NewDate(2000, 1, 1)) || date.After(s.Today().AddDays(1)) {
		return nil, validation("date", "date is outside the allowed range")
	}
	balance, err := money.ParseBalance(in.Amount, account.Currency)
	if err != nil {
		return nil, validation("amount", err.Error())
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 500 {
		return nil, validation("note", "note is too long")
	}
	var snap *domain.Snapshot
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		snap, err = s.Store.UpsertSnapshot(ctx, tx, store.SnapshotInput{
			AccountID:       account.ID,
			SnapshotDate:    date,
			BalanceMinor:    balance,
			Note:            note,
			CreatedByUserID: actorPtr(actor.UserID),
			UpdatedByUserID: actorPtr(actor.UserID),
		})
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "snapshot.upserted", "snapshot", snap.UID,
			nil, map[string]any{"account": account.Name, "date": date.String(), "balance_minor": balance, "currency": account.Currency})
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *Service) ListSnapshots(ctx context.Context, actor domain.Actor, accountUID string, from, to *domain.Date) ([]domain.Snapshot, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	account, err := s.Store.GetAccountByUID(ctx, accountUID)
	if err != nil {
		return nil, err
	}
	return s.Store.ListSnapshots(ctx, account.ID, from, to, false)
}

func (s *Service) DeleteSnapshot(ctx context.Context, actor domain.Actor, uid string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	snap, err := s.Store.GetSnapshotByUID(ctx, uid)
	if err != nil {
		return err
	}
	if snap.DeletedAt != nil {
		return domain.ErrNotFound
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.SoftDeleteSnapshot(ctx, tx, snap.ID, actor.UserID); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "snapshot.deleted", "snapshot", snap.UID, nil, nil)
	})
	return err
}

// ResolveAccount finds an account by UID or name, optionally narrowed by an owner hint.
func (s *Service) ResolveAccount(ctx context.Context, ref, ownerHint string) (*domain.Account, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, validation("account", "account is required")
	}
	if a, err := s.Store.GetAccountByUID(ctx, ref); err == nil {
		if a.Archived() {
			return nil, domain.ErrNotFound
		}
		return a, nil
	}
	accounts, err := s.Store.ListAccounts(ctx, false)
	if err != nil {
		return nil, err
	}
	refLower := strings.ToLower(ref)
	ownerLower := strings.ToLower(strings.TrimSpace(ownerHint))
	var matches []domain.Account
	for _, a := range accounts {
		if strings.ToLower(a.Name) != refLower {
			continue
		}
		if ownerLower != "" {
			ownerName := strings.ToLower(a.OwnerName)
			if !strings.Contains(ownerName, ownerLower) && !strings.Contains(ownerLower, ownerName) {
				continue
			}
		}
		matches = append(matches, a)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w: account %q", domain.ErrNotFound, ref)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: account %q matches several owners; specify the owner", domain.ErrConflict, ref)
	}
	return &matches[0], nil
}

func safeAccount(a *domain.Account) map[string]any {
	if a == nil {
		return nil
	}
	return map[string]any{
		"uid":          a.UID,
		"owner":        a.OwnerName,
		"name":         a.Name,
		"currency":     a.Currency,
		"kind":         a.Kind,
		"in_net_worth": a.IncludeInNetWorth,
		"archived":     a.Archived(),
	}
}
