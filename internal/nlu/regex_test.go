package nlu

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"skarb/internal/config"
	"skarb/internal/fx"
	"skarb/internal/service"
	"skarb/internal/store"
)

func testParser(t *testing.T) (*Parser, *service.Service, func()) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
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
	fixed := time.Date(2026, 10, 26, 12, 0, 0, 0, loc)
	svc.Now = func() time.Time { return fixed }
	return NewParser(svc, slog.New(slog.NewTextHandler(io.Discard, nil))), svc, func() { st.Close() }
}

func TestRegexParserUkrainianExample(t *testing.T) {
	p, _, cleanup := testParser(t)
	defer cleanup()
	intent, err := p.ParseRegex(context.Background(), "сьогодні 512 грн за їжу, 300 грн за каву")
	if err != nil {
		t.Fatal(err)
	}
	if intent.Operation != "add_transactions" || len(intent.Transactions) != 2 {
		t.Fatalf("unexpected intent: %+v", intent)
	}
	if intent.Transactions[0].Amount != "512" || intent.Transactions[0].Currency != "UAH" {
		t.Fatalf("first transaction: %+v", intent.Transactions[0])
	}
	if intent.Transactions[1].Amount != "300" {
		t.Fatalf("second transaction: %+v", intent.Transactions[1])
	}
	if intent.Transactions[0].Date != "2026-10-26" {
		t.Fatalf("date: %s", intent.Transactions[0].Date)
	}
	if intent.Transactions[0].Category == "" || intent.Transactions[1].Category == "" {
		t.Fatalf("categories not resolved: %+v", intent.Transactions)
	}
}

func TestRegexParserEnglish(t *testing.T) {
	p, _, cleanup := testParser(t)
	defer cleanup()
	intent, err := p.ParseRegex(context.Background(), "yesterday 20 USD coffee")
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.Transactions) != 1 {
		t.Fatalf("unexpected intent: %+v", intent)
	}
	tx := intent.Transactions[0]
	if tx.Amount != "20" || tx.Currency != "USD" || tx.Date != "2026-10-25" {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
}

func TestRegexParserRelativeDateAndCarExpenseAlias(t *testing.T) {
	p, svc, cleanup := testParser(t)
	defer cleanup()
	carExpenses, err := svc.Store.ResolveCategory(context.Background(), "Витрати на машину", nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		text string
		date string
	}{
		{"два дні тому шиномонтаж 880 грн", "2026-10-24"},
		{"2 дні тому шиномонтаж 880 грн", "2026-10-24"},
		{"two days ago tire service 880 грн", "2026-10-24"},
	}
	for _, tc := range cases {
		intent, err := p.ParseRegex(context.Background(), tc.text)
		if err != nil {
			t.Fatalf("%q: %v", tc.text, err)
		}
		if len(intent.Transactions) != 1 {
			t.Fatalf("%q: unexpected transactions: %+v", tc.text, intent.Transactions)
		}
		tx := intent.Transactions[0]
		if tx.Date != tc.date || tx.Amount != "880" || tx.Category != carExpenses.UID {
			t.Fatalf("%q: got date=%s amount=%s category=%s", tc.text, tx.Date, tx.Amount, tx.Category)
		}
	}
}

func TestRegexParserSingleAmountWithDecimals(t *testing.T) {
	p, _, cleanup := testParser(t)
	defer cleanup()
	cases := []struct {
		text   string
		amount string
		date   string
	}{
		{"їжа вчора 5125.5 грн", "5125.5", "2026-10-25"},
		{"їжа вчора 5125,50 грн", "5125.50", "2026-10-25"},
		{"їжа 5125 грн", "5125", "2026-10-26"},
		{"їжа 1 234,56 грн", "1 234.56", "2026-10-26"},
		{"їжа 12 345 грн", "12 345", "2026-10-26"},
	}
	for _, tc := range cases {
		intent, err := p.ParseRegex(context.Background(), tc.text)
		if err != nil {
			t.Fatalf("%q: %v", tc.text, err)
		}
		if len(intent.Transactions) != 1 {
			t.Fatalf("%q: expected 1 transaction, got %+v", tc.text, intent.Transactions)
		}
		tx := intent.Transactions[0]
		if tx.Amount != tc.amount || tx.Date != tc.date {
			t.Fatalf("%q: got amount=%s date=%s", tc.text, tx.Amount, tx.Date)
		}
	}
}

func TestRegexParserMultilineList(t *testing.T) {
	p, svc, cleanup := testParser(t)
	defer cleanup()
	intent, err := p.ParseRegex(context.Background(), "вчора:\nресторан 2100\nінше 300\nкава 75")
	if err != nil {
		t.Fatal(err)
	}
	expected := []struct{ amount, category string }{
		{"2100", "Кафе/ресторани"},
		{"300", "Інше"},
		{"75", "Кафе/ресторани"},
	}
	if len(intent.Transactions) != len(expected) {
		t.Fatalf("expected %d transactions, got %+v", len(expected), intent.Transactions)
	}
	for i, exp := range expected {
		cat, err := svc.Store.ResolveCategory(context.Background(), exp.category, nil)
		if err != nil {
			t.Fatal(err)
		}
		tx := intent.Transactions[i]
		if tx.Amount != exp.amount || tx.Category != cat.UID || tx.Date != "2026-10-25" {
			t.Fatalf("transaction %d: got amount=%s category=%s date=%s", i+1, tx.Amount, tx.Category, tx.Date)
		}
	}
}

func TestRegexParserSingleAmountUsesMessageCategory(t *testing.T) {
	p, svc, cleanup := testParser(t)
	defer cleanup()
	intent, err := p.ParseRegex(context.Background(), "512 грн, кава")
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %+v", intent.Transactions)
	}
	coffee, err := svc.Store.ResolveCategory(context.Background(), "Кафе/ресторани", nil)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Transactions[0].Category != coffee.UID {
		t.Fatalf("expected category Кафе/ресторани, got %+v", intent.Transactions[0])
	}
}

func TestPrefilter(t *testing.T) {
	if !LooksFinancial("сьогодні 512 грн за їжу") {
		t.Fatal("financial message not detected")
	}
	if LooksFinancial("привіт, як справи?") {
		t.Fatal("non-financial message detected")
	}
	if !LooksStats("скільки ми витратили цього місяця?") {
		t.Fatal("stats intent not detected")
	}
}
