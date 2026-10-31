package service_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/service"
	"skarb/internal/store"
)

func setupService(t *testing.T) (*service.Service, domain.Actor, func()) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/Kyiv")
	cfg := config.Config{
		DataDir: dir, PublicBaseURL: "http://localhost", AdminUsername: "admin",
		AdminPassword: "adminpassword123", MaxActiveUsers: 2, Timezone: "Europe/Kyiv",
		SessionSecret: []byte("test-secret-012345678901234567890"),
	}
	fxSvc := fx.New(st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), loc)
	svc, err := service.New(st, fxSvc, cfg)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := svc.BootstrapAdmin(ctx); err != nil {
		st.Close()
		t.Fatal(err)
	}
	admin, err := st.AdminUser(ctx)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	fixed := time.Date(2026, 10, 26, 12, 0, 0, 0, loc)
	svc.Now = func() time.Time { return fixed }
	return svc, svc.ActorForUser(admin, domain.SourceWeb), func() { st.Close() }
}

func TestSummaryAndBreakdownWithTwoTransactions(t *testing.T) {
	ctx := context.Background()
	svc, actor, cleanup := setupService(t)
	defer cleanup()
	today := svc.Today()

	food, err := svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Store.ResolveCategory(ctx, "Інше", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateTransaction(ctx, actor, service.TransactionItemInput{
		Amount: "500", Currency: "UAH", Date: today.String(), Category: food.UID, Description: "food",
	}, domain.SourceWeb, "food"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateTransaction(ctx, actor, service.TransactionItemInput{
		Amount: "880", Currency: "UAH", Date: today.String(), Category: other.UID, Description: "tires",
	}, domain.SourceWeb, "tires"); err != nil {
		t.Fatal(err)
	}

	sum, err := svc.Summary(ctx, actor, store.TransactionFilter{From: &today, To: &today})
	if err != nil {
		t.Fatal(err)
	}
	if sum.ExpenseUAH != 138000 || sum.Count != 2 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	rows, err := svc.CategoryBreakdown(ctx, actor, store.TransactionFilter{From: &today, To: &today}, domain.KindExpense, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 categories, got %+v", rows)
	}
	total := int64(0)
	amounts := map[string]int64{}
	for _, row := range rows {
		total += row.AmountUAH
		amounts[row.Name] = row.AmountUAH
	}
	if total != 138000 {
		t.Fatalf("category total = %d, rows=%+v", total, rows)
	}
	if amounts["Їжа"] != 50000 || amounts["Інше"] != 88000 {
		t.Fatalf("category amounts = %+v", amounts)
	}
}

func TestTransactionEditAndUndoVersioning(t *testing.T) {
	ctx := context.Background()
	svc, actor, cleanup := setupService(t)
	defer cleanup()
	today := svc.Today()

	cat, err := svc.CreateCategory(ctx, actor, store.CreateCategoryInput{
		Name: "Test category", Kind: domain.KindExpense, Color: "#112233", Emoji: "🧪",
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := svc.CreateTransaction(ctx, actor, service.TransactionItemInput{
		Amount: "10", Currency: "UAH", Date: today.String(), Category: cat.UID, Description: "first",
	}, domain.SourceWeb, "first")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := svc.EditTransactionNote(ctx, actor, original.UID, "with a friend")
	if err != nil {
		t.Fatal(err)
	}
	if edited.ReplacesID == nil || *edited.ReplacesID != original.ID {
		t.Fatalf("edit is not linked to the original: %+v", edited)
	}
	if edited.Note != "with a friend" {
		t.Fatalf("note = %q", edited.Note)
	}
	old, err := svc.GetTransaction(ctx, actor, original.UID)
	if err != nil {
		t.Fatal(err)
	}
	if !old.Deleted() {
		t.Fatal("original row should be soft-deleted after edit")
	}

	restored, err := svc.UndoTransaction(ctx, actor, edited.UID)
	if err != nil {
		t.Fatal(err)
	}
	if restored == nil || restored.UID != original.UID {
		t.Fatalf("undo did not restore the parent: %+v", restored)
	}
	newRow, err := svc.GetTransaction(ctx, actor, edited.UID)
	if err != nil {
		t.Fatal(err)
	}
	if !newRow.Deleted() {
		t.Fatal("edited row should be deleted after undo")
	}
	old, err = svc.GetTransaction(ctx, actor, original.UID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Deleted() {
		t.Fatal("original row should be restored after undo")
	}

	sum, err := svc.Summary(ctx, actor, store.TransactionFilter{From: &today, To: &today})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Count != 1 || sum.ExpenseUAH != 1000 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	latest, err := svc.LatestTransactionByOwner(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if latest.UID != original.UID {
		t.Fatalf("latest = %s, want %s", latest.UID, original.UID)
	}
}

func TestDeleteTransactionsBulk(t *testing.T) {
	ctx := context.Background()
	svc, actor, cleanup := setupService(t)
	defer cleanup()
	today := svc.Today()

	food, err := svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	var uids []string
	for _, desc := range []string{"a", "b", "c"} {
		tx, err := svc.CreateTransaction(ctx, actor, service.TransactionItemInput{
			Amount: "10", Currency: "UAH", Date: today.String(), Category: food.UID, Description: desc,
		}, domain.SourceWeb, desc)
		if err != nil {
			t.Fatal(err)
		}
		uids = append(uids, tx.UID)
	}

	// An unknown UID fails the whole batch and deletes nothing.
	if _, err := svc.DeleteTransactions(ctx, actor, []string{uids[0], "missing"}, "bulk"); err == nil {
		t.Fatal("expected error for unknown uid")
	}
	if tx, _ := svc.GetTransaction(ctx, actor, uids[0]); tx.Deleted() {
		t.Fatal("batch with an unknown uid must not delete anything")
	}

	if err := svc.DeleteTransaction(ctx, actor, uids[2], "single"); err != nil {
		t.Fatal(err)
	}
	// Duplicates are ignored and the already-deleted row is skipped.
	deleted, err := svc.DeleteTransactions(ctx, actor, []string{uids[0], uids[1], uids[1], uids[2]}, "bulk")
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || deleted[0] != uids[0] || deleted[1] != uids[1] {
		t.Fatalf("deleted = %v, want %v", deleted, uids[:2])
	}
	for _, uid := range uids {
		if tx, _ := svc.GetTransaction(ctx, actor, uid); !tx.Deleted() {
			t.Fatalf("%s should be deleted", uid)
		}
	}

	if _, err := svc.DeleteTransactions(ctx, actor, nil, ""); err == nil {
		t.Fatal("expected error for empty selection")
	}
}

func TestTrendForUserMatchesSummary(t *testing.T) {
	ctx := context.Background()
	svc, admin, cleanup := setupService(t)
	defer cleanup()
	member := newMember(t, svc, admin)
	day := svc.Today()
	addExpense(t, svc, admin, day, "500", "admin")
	addExpense(t, svc, member, day, "200", "member")
	for _, granularity := range []string{"day", "month"} {
		id := member.UserID
		points, err := svc.TrendForUser(ctx, admin, day.AddDays(-1), day, granularity, &id)
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, p := range points {
			total += p.ExpenseUAH
		}
		if total != 20000 {
			t.Fatalf("%s: got %d, want member's 20000", granularity, total)
		}
	}
}
