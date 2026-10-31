package web

import (
	"context"
	"encoding/csv"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"skarb/internal/domain"
	"skarb/internal/service"
)

func TestCategoryStatsViewsCSVAndDrilldown(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	app.addExpense(app.admin.actor, "admin expense")
	app.addExpense(app.member.actor, "member expense")
	food, err := app.svc.Store.ResolveCategory(ctx, "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.svc.ArchiveCategory(ctx, app.admin.actor, food.UID, true); err != nil {
		t.Fatal(err)
	}
	params := url.Values{"year": {"2026"}, "user": {app.member.actor.UserUID}, "kind": {"expense"}}
	for _, view := range []string{"charts", "table"} {
		params.Set("view", view)
		rec := app.do(&app.member, http.MethodGet, "/stats?"+params.Encode(), nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", view, rec.Code, rec.Body.String())
		}
		body := html.UnescapeString(rec.Body.String())
		if view == "charts" && (strings.Count(body, `data-chart="categories"`) != 1 || strings.Contains(body, `data-chart="category"`) || !strings.Contains(body, `data-series-toggle="`+food.UID+`"`)) {
			t.Fatal("expected one category chart with a category legend")
		}

		if !strings.Contains(body, "Динаміка категорій") || !strings.Contains(body, food.Name) || !strings.Contains(body, "2026-01-01") {
			t.Fatal("missing category or year")
		}
		if strings.Contains(body, "12.2026") {
			t.Fatal("future month")
		}
		want := url.Values{"from": {"2026-10-01"}, "to": {"2026-10-26"}, "category": {food.UID}, "kind": {"expense"}, "user": {app.member.actor.UserUID}}
		if !strings.Contains(body, "/transactions?"+want.Encode()) {
			t.Fatal("drilldown filters lost")
		}
	}
	rec := app.do(&app.member, http.MethodGet, "/stats/export.csv?"+params.Encode(), nil)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(rows) != 3 || len(rows[0]) != 12 || rows[1][10] != "25.00" || rows[1][11] != "25.00" || rows[2][11] != "25.00" {
		t.Fatalf("wrong filtered CSV: %v", rows)
	}
	txURL := "/transactions?" + url.Values{"from": {"2026-10-01"}, "to": {"2026-10-26"}, "category": {food.UID}, "kind": {"expense"}, "user": {app.member.actor.UserUID}}.Encode()
	body := app.do(&app.member, http.MethodGet, txURL, nil).Body.String()
	if !strings.Contains(body, "member expense") || strings.Contains(body, "admin expense") || !strings.Contains(body, `value="`+food.UID+`" selected`) {
		t.Fatal("archived category drilldown failed")
	}
}
func TestStatsPeriodAndDashboardLinks(t *testing.T) {
	app := newTestApp(t)
	for _, path := range []string{"/stats", "/stats?year=2025", "/stats?period=custom&from=2026-03-10&to=2026-01-15&view=table"} {
		rec := app.do(&app.admin, "GET", path, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	rec := app.do(&app.admin, "GET", "/stats?period=custom&from=bad&to=bad", nil)
	if rec.Code != 400 {
		t.Fatal("invalid dates accepted")
	}
	food, err := app.svc.Store.ResolveCategory(context.Background(), "Їжа", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.svc.CreateTransaction(context.Background(), app.admin.actor, service.TransactionItemInput{Amount: "10", Currency: "UAH", Date: "2026-09-10", Category: food.UID, Description: "September only"}, domain.SourceWeb, "")
	if err != nil {
		t.Fatal(err)
	}
	app.addExpense(app.admin.actor, "October only")
	for _, tc := range []struct{ path, from, to, want, absent, year string }{{"/", "2026-10-01", "2026-10-26", "October only", "September only", "2026"}, {"/?month=2026-09", "2026-09-01", "2026-09-30", "September only", "October only", "2026"}} {
		rec := app.do(&app.admin, "GET", tc.path, nil)
		body := html.UnescapeString(rec.Body.String())
		if rec.Code != 200 || strings.Contains(body, "Динаміка по днях") || !strings.Contains(body, `href="/transactions?from=`+tc.from+`&to=`+tc.to+`"`) || !strings.Contains(body, `href="/stats?year=`+tc.year+`"`) || !strings.Contains(body, tc.want) || strings.Contains(body, tc.absent) {
			t.Fatalf("wrong dashboard scope: %s", tc.path)
		}
	}
}
