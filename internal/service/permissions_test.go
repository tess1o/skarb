package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"skarb/internal/domain"
	"skarb/internal/service"
	"skarb/internal/store"
)

// newMember creates a regular (non-admin) user with a permanent password and
// returns an actor for them.
func newMember(t *testing.T, svc *service.Service, admin domain.Actor) domain.Actor {
	t.Helper()
	ctx := context.Background()
	user, temp, err := svc.CreateUser(ctx, admin, service.CreateUserInput{Username: "member", DisplayName: "Member"})
	if err != nil {
		t.Fatal(err)
	}
	actor := svc.ActorForUser(user, domain.SourceWeb)
	if err := svc.ChangePassword(ctx, actor, temp, "memberpassword123"); err != nil {
		t.Fatal(err)
	}
	return actor
}

func addExpense(t *testing.T, svc *service.Service, actor domain.Actor, date domain.Date, amount, desc string) *domain.Transaction {
	t.Helper()
	ctx := context.Background()
	food, err := svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := svc.CreateTransaction(ctx, actor, service.TransactionItemInput{
		Amount: amount, Currency: "UAH", Date: date.String(), Category: food.UID, Description: desc,
	}, domain.SourceWeb, desc)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestMemberCannotUseAdminActions(t *testing.T) {
	ctx := context.Background()
	svc, admin, cleanup := setupService(t)
	defer cleanup()
	member := newMember(t, svc, admin)

	checks := map[string]error{}
	_, checks["create category"] = svc.CreateCategory(ctx, member, store.CreateCategoryInput{
		Name: "Nope", Kind: domain.KindExpense, Color: "#112233",
	})
	_, _, checks["create user"] = svc.CreateUser(ctx, member, service.CreateUserInput{Username: "third", DisplayName: "Third"})
	_, _, checks["list deleted transactions"] = svc.ListTransactions(ctx, member, store.TransactionFilter{IncludeDeleted: true})
	_, checks["list inactive users"] = svc.ListUsers(ctx, member, true)
	for name, err := range checks {
		if !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", name, err)
		}
	}

	// Both household members may edit and delete any transaction.
	tx := addExpense(t, svc, admin, svc.Today(), "10", "admin's lunch")
	if err := svc.DeleteTransaction(ctx, member, tx.UID, "member delete"); err != nil {
		t.Fatalf("member deleting admin's transaction: %v", err)
	}
}

func TestRestoreTransactionsPermissionsAndVersions(t *testing.T) {
	ctx := context.Background()
	svc, admin, cleanup := setupService(t)
	defer cleanup()
	member := newMember(t, svc, admin)
	today := svc.Today()

	// A member can undo their own delete.
	own := addExpense(t, svc, admin, today, "10", "own")
	if err := svc.DeleteTransaction(ctx, member, own.UID, ""); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.RestoreTransactions(ctx, member, []string{own.UID}); err != nil || n != 1 {
		t.Fatalf("member restoring own delete: n=%d err=%v", n, err)
	}

	// ...but not a delete made by someone else; the admin can.
	other := addExpense(t, svc, admin, today, "20", "other")
	if err := svc.DeleteTransaction(ctx, admin, other.UID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreTransactions(ctx, member, []string{other.UID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member restoring admin's delete: err = %v, want ErrForbidden", err)
	}
	if n, err := svc.RestoreTransactions(ctx, admin, []string{other.UID}); err != nil || n != 1 {
		t.Fatalf("admin restore: n=%d err=%v", n, err)
	}

	// Rows that are not deleted are skipped.
	if n, err := svc.RestoreTransactions(ctx, admin, []string{other.UID}); err != nil || n != 0 {
		t.Fatalf("restoring an active row: n=%d err=%v", n, err)
	}

	// Restoring a superseded version would duplicate the entry.
	original := addExpense(t, svc, admin, today, "30", "original")
	if _, err := svc.EditTransactionNote(ctx, admin, original.UID, "edited"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreTransactions(ctx, admin, []string{original.UID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("restoring a replaced version: err = %v, want ErrConflict", err)
	}
}

func TestBulkLimits(t *testing.T) {
	ctx := context.Background()
	svc, admin, cleanup := setupService(t)
	defer cleanup()
	uids := make([]string, service.MaxBulkDelete+1)
	for i := range uids {
		uids[i] = fmt.Sprintf("uid-%d", i)
	}
	if _, err := svc.DeleteTransactions(ctx, admin, uids, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("delete over limit: err = %v, want validation error", err)
	}
	if _, err := svc.RestoreTransactions(ctx, admin, uids); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("restore over limit: err = %v, want validation error", err)
	}
}

func TestDashboardMonth(t *testing.T) {
	ctx := context.Background()
	svc, admin, cleanup := setupService(t) // today is 2026-10-26
	defer cleanup()
	addExpense(t, svc, admin, domain.NewDate(2026, 9, 15), "100", "september")
	addExpense(t, svc, admin, domain.NewDate(2026, 10, 5), "50", "october")

	sep, err := svc.DashboardMonth(ctx, admin, "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if sep.IsCurrent || sep.Month != "2026-09" || sep.PrevMonth != "2026-08" || sep.NextMonth != "2026-10" {
		t.Fatalf("september navigation: %+v", sep)
	}
	if sep.Period.From.String() != "2026-09-01" || sep.Period.To.String() != "2026-09-30" {
		t.Fatalf("september period: %v", sep.Period)
	}
	if sep.Summary.ExpenseUAH != 10000 {
		t.Fatalf("september expenses = %d, want 10000", sep.Summary.ExpenseUAH)
	}
	if len(sep.Recent) != 1 || sep.Recent[0].Description != "september" {
		t.Fatalf("september recent = %+v", sep.Recent)
	}

	// Empty, invalid and future months all show the current month to date.
	for _, month := range []string{"", "garbage", "2026-10", "2027-01"} {
		cur, err := svc.DashboardMonth(ctx, admin, month)
		if err != nil {
			t.Fatal(err)
		}
		if !cur.IsCurrent || cur.NextMonth != "" || cur.Period.To.String() != "2026-10-26" {
			t.Fatalf("month %q: current=%v next=%q period=%v", month, cur.IsCurrent, cur.NextMonth, cur.Period)
		}
		if cur.Summary.ExpenseUAH != 5000 {
			t.Fatalf("month %q: expenses = %d, want 5000", month, cur.Summary.ExpenseUAH)
		}
	}
}

func TestPurgeStale(t *testing.T) {
	ctx := context.Background()
	svc, _, cleanup := setupService(t)
	defer cleanup()
	db := svc.Store.DB
	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour).Format(time.RFC3339Nano)
	recent := now.Add(-time.Hour).Format(time.RFC3339Nano)

	keep, _, err := svc.Login(ctx, "admin", "adminpassword123", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	expired, _, err := svc.Login(ctx, "admin", "adminpassword123", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	stmts := []struct {
		q    string
		args []any
	}{
		{`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, []any{old, expired.TokenHash}},
		{`INSERT INTO telegram_updates(bot_id, update_id, received_at) VALUES (1, 1, ?), (1, 2, ?)`, []any{old, recent}},
		{`INSERT INTO idempotency_keys(scope, key, request_hash, response_json, created_at) VALUES ('s', 'old', 'h', '{}', ?), ('s', 'new', 'h', '{}', ?)`, []any{old, recent}},
	}
	for _, st := range stmts {
		if _, err := db.ExecContext(ctx, st.q, st.args...); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := svc.Store.PurgeStale(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int64{"sessions": 1, "telegram_updates": 1, "idempotency_keys": 1} {
		if removed[table] != want {
			t.Errorf("%s: removed %d, want %d", table, removed[table], want)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, keep.TokenHash).Scan(&n); err != nil || n != 1 {
		t.Fatalf("active session was purged: n=%d err=%v", n, err)
	}
}
