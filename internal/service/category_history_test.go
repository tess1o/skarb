package service_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/service"
	"skarb/internal/store"
)

type failedRatesTransport struct{}

func (failedRatesTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
}

func TestCategoryHistoryFiltersAndMonthTotals(t *testing.T) {
	ctx := context.Background()
	svc, admin, cleanup := setupService(t)
	defer cleanup()
	member := newMember(t, svc, admin)
	from := domain.NewDate(2025, 12, 15)
	to := domain.NewDate(2026, 3, 10)
	addExpense(t, svc, admin, domain.NewDate(2025, 12, 20), "100", "December")
	addExpense(t, svc, admin, domain.NewDate(2026, 1, 10), "200", "January")
	addExpense(t, svc, member, domain.NewDate(2026, 1, 12), "50", "Member")
	addExpense(t, svc, admin, domain.NewDate(2025, 12, 1), "999", "outside")
	removed := addExpense(t, svc, admin, domain.NewDate(2026, 2, 1), "999", "deleted")
	if err := svc.DeleteTransaction(ctx, admin, removed.UID, ""); err != nil {
		t.Fatal(err)
	}
	salary, err := svc.Store.ResolveCategory(ctx, "Зарплата", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateTransaction(ctx, admin, service.TransactionItemInput{Amount: "900", Currency: "UAH", Date: "2026-01-10", Category: salary.UID, Description: "Salary"}, domain.SourceWeb, "")
	if err != nil {
		t.Fatal(err)
	}
	food, err := svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveCategory(ctx, admin, food.UID, true); err != nil {
		t.Fatal(err)
	}
	h, err := svc.CategoryHistory(ctx, member, store.TransactionFilter{From: &from, To: &to}, domain.KindExpense)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Months) != 4 || len(h.Rows) != 1 || h.Total.AmountUAH != 35000 || h.Rows[0].Category.UID != food.UID {
		t.Fatalf("unexpected history: %+v", h)
	}
	for i, want := range []int64{10000, 25000, 0, 0} {
		if h.Rows[0].Months[i].AmountUAH != want || h.Totals[i].AmountUAH != want {
			t.Fatalf("month %d mismatch", i)
		}
	}
	if !h.Months[0].From.Equal(from) || !h.Months[3].To.Equal(to) {
		t.Fatal("partial months not clipped")
	}
	id := member.UserID
	h, err = svc.CategoryHistory(ctx, admin, store.TransactionFilter{From: &from, To: &to, OwnerUserID: &id}, domain.KindExpense)
	if err != nil || h.Total.AmountUAH != 5000 {
		t.Fatalf("member: %+v %v", h, err)
	}
	h, err = svc.CategoryHistory(ctx, admin, store.TransactionFilter{From: &from, To: &to}, domain.KindIncome)
	if err != nil || h.Total.AmountUAH != 90000 {
		t.Fatalf("income: %+v %v", h, err)
	}
	to = domain.NewDate(2026, 12, 31)
	h, err = svc.CategoryHistory(ctx, admin, store.TransactionFilter{From: &from, To: &to}, domain.KindExpense)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Months[len(h.Months)-1].To.Equal(svc.Today()) {
		t.Fatal("future months must be excluded")
	}
}
func TestCategoryHistoryMissingRates(t *testing.T) {
	ctx := context.Background()
	svc, actor, cleanup := setupService(t)
	defer cleanup()
	svc.FX = fx.New(svc.Store, &http.Client{Transport: failedRatesTransport{}}, slog.New(slog.NewTextHandler(io.Discard, nil)), svc.Loc)
	food, err := svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	day := svc.Today()
	_, err = svc.CreateTransaction(ctx, actor, service.TransactionItemInput{Amount: "50", Currency: "USD", Date: day.String(), Category: food.UID, Description: "no rate"}, domain.SourceWeb, "")
	if err != nil {
		t.Fatal(err)
	}
	addExpense(t, svc, actor, day, "123", "converted")
	if err := svc.Store.UpsertRate(ctx, nil, "EUR", day, 4500000000, "manual", nil); err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateTransaction(ctx, actor, service.TransactionItemInput{Amount: "2", Currency: "EUR", Date: day.String(), Category: food.UID, Description: "converted EUR"}, domain.SourceWeb, "")
	if err != nil {
		t.Fatal(err)
	}

	h, err := svc.CategoryHistory(ctx, actor, store.TransactionFilter{From: &day, To: &day}, domain.KindExpense)
	if err != nil {
		t.Fatal(err)
	}
	if h.Total.MissingRate != 1 || h.Total.AmountUAH != 21300 || h.Rows[0].Months[0].MissingRate != 1 || h.Totals[0].MissingRate != 1 {
		t.Fatalf("missing rate lost: %+v", h)
	}
	dashboard, err := svc.DashboardMonth(ctx, actor, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Categories) != 1 || dashboard.Categories[0].MissingRate != 1 || !dashboard.CategoryComparisonIncomplete {
		t.Fatal("dashboard must mark incomplete categories")
	}

}
func TestDashboardCategoriesAndMonthScope(t *testing.T) {
	ctx := context.Background()
	svc, actor, cleanup := setupService(t)
	defer cleanup()
	cats, err := svc.Store.ListCategories(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cats {
		if c.Kind != domain.KindExpense {
			continue
		}
		_, err := svc.CreateTransaction(ctx, actor, service.TransactionItemInput{Amount: "100", Currency: "UAH", Date: "2026-10-10", Category: c.UID, Description: c.Name}, domain.SourceWeb, "")
		if err != nil {
			t.Fatal(err)
		}
		if i >= 8 {
			break
		}
	}
	addExpense(t, svc, actor, domain.NewDate(2026, 9, 1), "30", "previous")
	current, err := svc.DashboardMonth(ctx, actor, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Categories) != 9 {
		t.Fatalf("categories truncated: %d", len(current.Categories))
	}
	for _, tx := range current.Recent {
		if tx.OccurredOn.Month() != 10 {
			t.Fatal("outside current month")
		}
	}
	food, err := svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	if current.CategoryChanges[food.ID] != 7000 {
		t.Fatalf("wrong comparison: %d", current.CategoryChanges[food.ID])
	}
	past, err := svc.DashboardMonth(ctx, actor, "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	if len(past.Recent) != 1 || past.Recent[0].Description != "previous" {
		t.Fatal("historical month scope")
	}
}
