package web

import (
	"html"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStatsPrimaryChartAndComparisonLabels(t *testing.T) {
	app := newTestApp(t)
	body := app.do(&app.admin, "GET", "/stats?period=custom&from=2026-10-01&to=2026-10-26", nil).Body.String()
	overall := strings.Index(body, `class="card overall-trend"`)
	categories := strings.Index(body, `class="category-history"`)
	comparison := strings.Index(body, `class="card period-comparison"`)
	users := strings.Index(body, `class="card user-breakdown"`)
	if overall < 0 || overall >= categories || categories >= comparison || comparison >= users {
		t.Fatal("wrong section order")
	}
	for _, label := range []string{"Обраний період:", "01.10.2026 — 26.10.2026", "Попередній період такої самої тривалості:", "05.09.2026 — 30.09.2026"} {
		if !strings.Contains(body, label) {
			t.Fatalf("missing %s", label)
		}
	}
	if strings.Contains(body, `<details class="card supplementary"`) {
		t.Fatal("primary sections should be visible")
	}
}

func TestStatsYearUsesApplicationClock(t *testing.T) {
	app := newTestApp(t)
	app.svc.Now = func() time.Time { return time.Date(2027, 1, 1, 0, 30, 0, 0, app.svc.Loc) }
	admin := app.login("admin", "adminpassword123")
	rec := app.do(&admin, "GET", "/stats", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="year" min="1" max="2027" value="2027"`) {
		t.Fatal("year control must use the same clock as the selected period")
	}
}

func TestCategorySeriesSelectionSurvivesViewAndPeriodLinks(t *testing.T) {
	app := newTestApp(t)
	for _, value := range []string{"", "one,two"} {
		rec := app.do(&app.admin, "GET", "/stats?year=2026&series="+url.QueryEscape(value), nil)
		body := html.UnescapeString(rec.Body.String())
		if rec.Code != 200 || !strings.Contains(body, `name="series" value="`+value+`"`) {
			t.Fatal("series field not retained")
		}
		if !strings.Contains(body, "series="+url.QueryEscape(value)+"&") {
			t.Fatal("series selection not retained in view and year links")
		}
	}
}

func TestStatsYearQueryKeepsFiltersWithoutCustomDates(t *testing.T) {
	selection := statsSelection{Year: 2026, Custom: true, Kind: "income", User: "member", View: "table", Series: "a,b", HasSeries: true}
	query := selection.yearQuery(2025)
	want := url.Values{"year": {"2025"}, "kind": {"income"}, "user": {"member"}, "view": {"table"}, "series": {"a,b"}}
	if query.Encode() != want.Encode() {
		t.Fatalf("year query: got %s, want %s", query.Encode(), want.Encode())
	}
	if !selection.Custom || selection.Year != 2026 {
		t.Fatal("year navigation mutated current selection")
	}
}
