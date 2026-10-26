package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/store"
)

type TransactionItemInput struct {
	Amount      string
	Currency    string
	Date        string
	Category    string
	Description string
	Note        string
	OwnerUserID *int64
}

type CreateTransactionsInput struct {
	Items        []TransactionItemInput
	Source       domain.Source
	OriginalText string
}

func (s *Service) CreateTransactions(ctx context.Context, actor domain.Actor, in CreateTransactionsInput) ([]domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if len(in.Items) == 0 {
		return nil, validation("transactions", "no transactions supplied")
	}
	if len(in.Items) > 10 {
		return nil, validation("transactions", "at most 10 transactions per message")
	}
	if len(in.OriginalText) > 4000 {
		return nil, validation("text", "message is too long")
	}
	source := in.Source
	if source == "" {
		switch actor.Kind {
		case domain.SourceTelegram, domain.SourceMCP, domain.SourceWeb:
			source = actor.Kind
		default:
			source = domain.SourceWeb
		}
	}
	if source != domain.SourceWeb && source != domain.SourceTelegram && source != domain.SourceMCP {
		return nil, validation("source", "invalid transaction source")
	}
	today := s.Today()

	preparedItems := make([]store.CreateTransactionInput, 0, len(in.Items))
	for idx, item := range in.Items {
		currency := strings.ToUpper(strings.TrimSpace(item.Currency))
		if currency == "" {
			currency = "UAH"
		}
		if _, err := money.CurrencyFor(currency); err != nil {
			return nil, validation(fmt.Sprintf("transactions[%d].currency", idx), err.Error())
		}
		amountMinor, err := money.ParseAmount(item.Amount, currency)
		if err != nil {
			return nil, validation(fmt.Sprintf("transactions[%d].amount", idx), err.Error())
		}
		occurred, err := parseFlexibleDate(item.Date)
		if err != nil {
			return nil, validation(fmt.Sprintf("transactions[%d].date", idx), err.Error())
		}
		if occurred.IsZero() {
			occurred = today
		}
		if occurred.Before(domain.NewDate(2000, 1, 1)) || occurred.After(today.AddDays(1)) {
			return nil, validation(fmt.Sprintf("transactions[%d].date", idx), "date is outside the allowed range")
		}
		cat, err := s.resolveCategory(ctx, item.Category, nil)
		if err != nil {
			return nil, validation(fmt.Sprintf("transactions[%d].category", idx), "unknown category")
		}
		ownerID, err := s.resolveOwner(ctx, actor, item.OwnerUserID)
		if err != nil {
			return nil, err
		}
		owner, err := s.Store.GetUserByID(ctx, ownerID)
		if err != nil || !owner.IsActive {
			return nil, validation(fmt.Sprintf("transactions[%d].owner", idx), "owner not found or inactive")
		}
		description := strings.TrimSpace(item.Description)
		if description == "" {
			description = cat.Name
		}
		if len([]rune(description)) > 200 {
			return nil, validation(fmt.Sprintf("transactions[%d].description", idx), "description is too long")
		}
		note := strings.TrimSpace(item.Note)
		if len([]rune(note)) > 500 {
			return nil, validation(fmt.Sprintf("transactions[%d].note", idx), "note is too long")
		}
		preparedItems = append(preparedItems, store.CreateTransactionInput{
			Kind:            cat.Kind,
			AmountMinor:     amountMinor,
			Currency:        currency,
			OccurredOn:      occurred,
			CategoryID:      cat.ID,
			OwnerUserID:     ownerID,
			Description:     description,
			Note:            note,
			OriginalText:    in.OriginalText,
			Source:          source,
			CreatedByUserID: actorPtr(actor.UserID),
			UpdatedByUserID: actorPtr(actor.UserID),
		})
	}

	var created []domain.Transaction
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		for _, p := range preparedItems {
			t, err := s.Store.CreateTransaction(ctx, tx, p)
			if err != nil {
				return err
			}
			if err := s.audit(ctx, tx, actor, "transaction.created", "transaction", t.UID, nil, safeTransaction(t)); err != nil {
				return err
			}
			created = append(created, *t)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Service) CreateTransaction(ctx context.Context, actor domain.Actor, item TransactionItemInput, source domain.Source, originalText string) (*domain.Transaction, error) {
	items, err := s.CreateTransactions(ctx, actor, CreateTransactionsInput{Items: []TransactionItemInput{item}, Source: source, OriginalText: originalText})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

type UpdateTransactionInput struct {
	Amount      *string
	Currency    *string
	Date        *string
	Category    *string
	Description *string
	Note        *string
	OwnerUserID *int64
}

func (s *Service) UpdateTransaction(ctx context.Context, actor domain.Actor, uid string, in UpdateTransactionInput) (*domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	before, err := s.Store.GetTransactionByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if before.Deleted() {
		return nil, domain.ErrNotFound
	}
	upd := store.UpdateTransactionInput{UpdatedByUserID: actorPtr(actor.UserID)}
	currency := before.Currency
	if in.Currency != nil {
		currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
		if _, err := money.CurrencyFor(currency); err != nil {
			return nil, validation("currency", err.Error())
		}
		upd.Currency = &currency
	}
	if in.Amount != nil {
		amountMinor, err := money.ParseAmount(*in.Amount, currency)
		if err != nil {
			return nil, validation("amount", err.Error())
		}
		upd.AmountMinor = &amountMinor
	}
	if in.Date != nil {
		occurred, err := parseFlexibleDate(*in.Date)
		if err != nil {
			return nil, validation("date", err.Error())
		}
		today := s.Today()
		if occurred.IsZero() || occurred.Before(domain.NewDate(2000, 1, 1)) || occurred.After(today.AddDays(1)) {
			return nil, validation("date", "date is outside the allowed range")
		}
		upd.OccurredOn = &occurred
	}
	if in.Category != nil {
		cat, err := s.resolveCategory(ctx, *in.Category, nil)
		if err != nil {
			return nil, validation("category", "unknown category")
		}
		upd.CategoryID = &cat.ID
		upd.Kind = &cat.Kind
	}
	if in.OwnerUserID != nil {
		if actor.Role != domain.RoleAdmin {
			return nil, domain.ErrForbidden
		}
		owner, err := s.Store.GetUserByID(ctx, *in.OwnerUserID)
		if err != nil || !owner.IsActive {
			return nil, validation("owner", "owner not found or inactive")
		}
		upd.OwnerUserID = in.OwnerUserID
	}
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if len([]rune(d)) > 200 {
			return nil, validation("description", "description is too long")
		}
		upd.Description = &d
	}
	if in.Note != nil {
		n := strings.TrimSpace(*in.Note)
		if len([]rune(n)) > 500 {
			return nil, validation("note", "note is too long")
		}
		upd.Note = &n
	}
	var updated *domain.Transaction
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		updated, err = s.Store.UpdateTransaction(ctx, tx, before.ID, upd)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "transaction.updated", "transaction", updated.UID,
			safeTransaction(before), safeTransaction(updated))
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) DeleteTransaction(ctx context.Context, actor domain.Actor, uid, reason string) error {
	if err := s.RequireAuth(actor); err != nil {
		return err
	}
	tx, err := s.Store.GetTransactionByUID(ctx, uid)
	if err != nil {
		return err
	}
	if tx.Deleted() {
		return domain.ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 200 {
		return validation("reason", "reason is too long")
	}
	err = s.withTx(ctx, func(sqlTx *sql.Tx) error {
		if err := s.Store.SoftDeleteTransaction(ctx, sqlTx, tx.ID, actor.UserID, reason); err != nil {
			return err
		}
		return s.audit(ctx, sqlTx, actor, "transaction.deleted", "transaction", tx.UID,
			safeTransaction(tx), map[string]any{"reason": reason})
	})
	return err
}

// MaxBulkDelete caps how many transactions one DeleteTransactions call may touch.
const MaxBulkDelete = 200

// DeleteTransactions soft-deletes several transactions in one database
// transaction: either all of them are deleted or none are. Rows that are
// already deleted are skipped, so a stale page can be resubmitted safely.
// It returns the number of rows actually deleted.
func (s *Service) DeleteTransactions(ctx context.Context, actor domain.Actor, uids []string, reason string) (int, error) {
	if err := s.RequireAuth(actor); err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 200 {
		return 0, validation("reason", "reason is too long")
	}
	seen := map[string]bool{}
	var unique []string
	for _, uid := range uids {
		uid = strings.TrimSpace(uid)
		if uid != "" && !seen[uid] {
			seen[uid] = true
			unique = append(unique, uid)
		}
	}
	if len(unique) == 0 {
		return 0, validation("uids", "no transactions selected")
	}
	if len(unique) > MaxBulkDelete {
		return 0, validation("uids", fmt.Sprintf("at most %d transactions at once", MaxBulkDelete))
	}
	var targets []*domain.Transaction
	for _, uid := range unique {
		tx, err := s.Store.GetTransactionByUID(ctx, uid)
		if err != nil {
			return 0, err
		}
		if !tx.Deleted() {
			targets = append(targets, tx)
		}
	}
	err := s.withTx(ctx, func(sqlTx *sql.Tx) error {
		for _, tx := range targets {
			if err := s.Store.SoftDeleteTransaction(ctx, sqlTx, tx.ID, actor.UserID, reason); err != nil {
				return err
			}
			if err := s.audit(ctx, sqlTx, actor, "transaction.deleted", "transaction", tx.UID,
				safeTransaction(tx), map[string]any{"reason": reason, "bulk": true}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(targets), nil
}

// EditTransactionNote implements the Telegram comment flow: the old row is
// soft-deleted and a replacement row with the new note is linked to it.
func (s *Service) EditTransactionNote(ctx context.Context, actor domain.Actor, uid, note string) (*domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	before, err := s.Store.GetTransactionByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if before.Deleted() {
		return nil, domain.ErrNotFound
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return nil, validation("note", "note is too long")
	}
	createdBy := actorPtr(actor.UserID)
	var updated *domain.Transaction
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		updated, err = s.Store.CreateTransaction(ctx, tx, store.CreateTransactionInput{
			Kind:            before.Kind,
			AmountMinor:     before.AmountMinor,
			Currency:        before.Currency,
			OccurredOn:      before.OccurredOn,
			CategoryID:      before.CategoryID,
			OwnerUserID:     before.OwnerUserID,
			Description:     before.Description,
			Note:            note,
			OriginalText:    before.OriginalText,
			Source:          before.Source,
			CreatedByUserID: createdBy,
			UpdatedByUserID: createdBy,
			ReplacesID:      &before.ID,
		})
		if err != nil {
			return err
		}
		if err := s.Store.SoftDeleteTransaction(ctx, tx, before.ID, actor.UserID, "edited"); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, "transaction.edited_note", "transaction", updated.UID,
			safeTransaction(before), safeTransaction(updated)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// UndoTransaction reverses an add or an edit. If the transaction replaced a
// previous version, the immediate parent is restored and returned.
func (s *Service) UndoTransaction(ctx context.Context, actor domain.Actor, uid string) (*domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	cur, err := s.Store.GetTransactionByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if cur.Deleted() {
		return nil, domain.ErrNotFound
	}
	var restored *domain.Transaction
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.Store.SoftDeleteTransaction(ctx, tx, cur.ID, actor.UserID, "undone"); err != nil {
			return err
		}
		if cur.ReplacesID != nil {
			parent, err := s.Store.GetTransactionByID(ctx, *cur.ReplacesID)
			if err != nil {
				return err
			}
			if err := s.Store.RestoreTransaction(ctx, tx, parent.ID, actor.UserID); err != nil {
				return err
			}
			restored = parent
		}
		return s.audit(ctx, tx, actor, "transaction.undone", "transaction", cur.UID,
			safeTransaction(cur), safeTransaction(restored))
	})
	if err != nil {
		return nil, err
	}
	if restored != nil {
		restored.DeletedAt = nil
	}
	return restored, nil
}

func (s *Service) LatestTransactionByOwner(ctx context.Context, actor domain.Actor) (*domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	rows, err := s.Store.ListTransactions(ctx, store.TransactionFilter{OwnerUserID: &actor.UserID, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, domain.ErrNotFound
	}
	return &rows[0], nil
}

func (s *Service) RestoreTransaction(ctx context.Context, actor domain.Actor, uid string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	tx, err := s.Store.GetTransactionByUID(ctx, uid)
	if err != nil {
		return err
	}
	if !tx.Deleted() {
		return domain.ErrConflict
	}
	err = s.withTx(ctx, func(sqlTx *sql.Tx) error {
		if err := s.Store.RestoreTransaction(ctx, sqlTx, tx.ID, actor.UserID); err != nil {
			return err
		}
		return s.audit(ctx, sqlTx, actor, "transaction.restored", "transaction", tx.UID, nil, nil)
	})
	return err
}

func (s *Service) ListTransactions(ctx context.Context, actor domain.Actor, f store.TransactionFilter) ([]domain.Transaction, int, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, 0, err
	}
	if f.IncludeDeleted && actor.Role != domain.RoleAdmin {
		return nil, 0, domain.ErrForbidden
	}
	total, err := s.Store.CountTransactions(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.Store.ListTransactions(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *Service) GetTransaction(ctx context.Context, actor domain.Actor, uid string) (*domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	return s.Store.GetTransactionByUID(ctx, uid)
}

func (s *Service) TransactionVersions(ctx context.Context, actor domain.Actor, uid string) ([]domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	return s.Store.ListTransactionVersions(ctx, uid)
}

func (s *Service) RecentTransactions(ctx context.Context, actor domain.Actor, limit int) ([]domain.Transaction, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	return s.Store.ListTransactions(ctx, store.TransactionFilter{Limit: limit})
}

func parseFlexibleDate(raw string) (domain.Date, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.Date{}, nil
	}
	layouts := []string{"2006-01-02", "02.01.2006", "02/01/2006", "2.1.2006", "2/1/2006"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return domain.DateOf(t), nil
		}
	}
	return domain.Date{}, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", raw)
}

func actorPtr(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func safeTransaction(t *domain.Transaction) map[string]any {
	if t == nil {
		return nil
	}
	return map[string]any{
		"uid":          t.UID,
		"kind":         t.Kind,
		"amount_minor": t.AmountMinor,
		"currency":     t.Currency,
		"occurred_on":  t.OccurredOn.String(),
		"category":     t.CategoryName,
		"owner":        t.OwnerName,
		"description":  t.Description,
		"note":         t.Note,
		"source":       t.Source,
		"deleted":      t.Deleted(),
	}
}
