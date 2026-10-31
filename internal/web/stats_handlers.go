package web

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/service"
	"skarb/internal/store"
)

type statsSelection struct {
	From, To domain.Date
	Year     int
	Custom   bool
	Kind     domain.Kind
	User     string
	UserID   *int64
	View     string
}

func (s *Server) statsSelection(r *http.Request) (statsSelection, error) {
	q := r.URL.Query()
	today := s.svc.Today()
	v := statsSelection{Year: today.Year(), Kind: domain.KindExpense, User: q.Get("user"), View: "charts"}
	if year, err := strconv.Atoi(q.Get("year")); err == nil && year >= 1 && year <= today.Year() {
		v.Year = year
	}
	v.From = domain.NewDate(v.Year, time.January, 1)
	v.To = domain.NewDate(v.Year, time.December, 31)
	v.Custom = q.Get("period") == "custom" || (q.Get("period") == "" && (q.Get("from") != "" || q.Get("to") != ""))
	if v.Custom {
		var err error
		v.From, err = domain.ParseDate(q.Get("from"))
		if err != nil {
			return v, err
		}
		v.To, err = domain.ParseDate(q.Get("to"))
		if err != nil {
			return v, err
		}
		if v.To.Before(v.From) {
			v.From, v.To = v.To, v.From
		}
	}
	if v.To.After(today) {
		v.To = today
	}
	if v.From.After(v.To) {
		return v, domain.ErrValidation
	}
	if q.Get("kind") == "income" {
		v.Kind = domain.KindIncome
	}
	if q.Get("view") == "table" {
		v.View = "table"
	}
	if v.User != "" {
		u, err := s.svc.UserByUID(r.Context(), v.User)
		if err != nil {
			return v, err
		}
		v.UserID = &u.ID
	}
	return v, nil
}
func (v statsSelection) query() url.Values {
	q := url.Values{"kind": {string(v.Kind)}, "view": {v.View}, "user": {v.User}}
	if v.Custom {
		q.Set("period", "custom")
		q.Set("from", v.From.String())
		q.Set("to", v.To.String())
	} else {
		q.Set("year", strconv.Itoa(v.Year))
	}
	return q
}

type categoryPointView struct {
	service.CategoryMonth
	Date       domain.Date
	Label, URL string
}
type categoryRowView struct {
	Category domain.Category
	Total    service.CategoryMonth
	Points   []categoryPointView
}
type categoryHeaderView struct {
	Label    string
	Partial  bool
	From, To domain.Date
}

func categoryViews(history *service.CategoryHistory, selection statsSelection) ([]categoryHeaderView, []categoryRowView) {
	var headers []categoryHeaderView
	for _, m := range history.Months {
		headers = append(headers, categoryHeaderView{Label: m.From.Format("01.2006"), From: m.From, To: m.To, Partial: m.From.Day() != 1 || !m.To.Equal(service.MonthEnd(m.From))})
	}
	var rows []categoryRowView
	for _, r := range history.Rows {
		row := categoryRowView{Category: r.Category, Total: r.Total}
		for i, p := range r.Months {
			m := history.Months[i]
			q := url.Values{"from": {m.From.String()}, "to": {m.To.String()}, "kind": {string(selection.Kind)}, "category": {r.Category.UID}}
			if selection.User != "" {
				q.Set("user", selection.User)
			}
			row.Points = append(row.Points, categoryPointView{CategoryMonth: p, Date: m.From, Label: m.From.Format("01.2006"), URL: "/transactions?" + q.Encode()})
		}
		rows = append(rows, row)
	}
	return headers, rows
}
func (s *Server) statsPage(w http.ResponseWriter, r *http.Request) {
	v, err := s.statsSelection(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Перевірте період і користувача.")
		return
	}
	actor := s.actor(r)
	f := store.TransactionFilter{From: &v.From, To: &v.To, OwnerUserID: v.UserID}
	summary, err := s.svc.Summary(r.Context(), actor, f)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	history, err := s.svc.CategoryHistory(r.Context(), actor, f, v.Kind)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	users, err := s.svc.UserBreakdown(r.Context(), actor, f)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	trend, err := s.svc.TrendForUser(r.Context(), actor, v.From, v.To, "month", v.UserID)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	days := int(v.To.Time.Sub(v.From.Time).Hours()/24) + 1
	prevTo := v.From.AddDays(-1)
	prevFrom := prevTo.AddDays(-(days - 1))
	comparison, err := s.svc.Compare(r.Context(), actor, domain.DateRange{From: v.From, To: v.To}, domain.DateRange{From: prevFrom, To: prevTo}, v.UserID)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	allUsers, err := s.svc.ListUsers(r.Context(), actor, false)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	headers, rows := categoryViews(history, v)
	q := v.query()
	q.Set("view", "charts")
	chartsURL := "/stats?" + q.Encode()
	q.Set("view", "table")
	tableURL := "/stats?" + q.Encode()
	prev := url.Values{"kind": {string(v.Kind)}, "view": {v.View}, "user": {v.User}}
	prev.Set("year", strconv.Itoa(v.Year-1))
	next := url.Values{"kind": {string(v.Kind)}, "view": {v.View}, "user": {v.User}}
	next.Set("year", strconv.Itoa(v.Year+1))
	s.render(w, r, "stats.html", map[string]any{
		"Title": "Статистика", "From": v.From, "To": v.To, "Selection": v, "Summary": summary, "Users": users, "AllUsers": allUsers, "Trend": trend, "Comparison": comparison,
		"Headers": headers, "CategoryRows": rows, "History": history, "ChartsURL": chartsURL, "TableURL": tableURL, "ExportURL": "/stats/export.csv?" + v.query().Encode(),
		"PrevYear": v.Year - 1, "NextYear": v.Year + 1, "PrevYearURL": "/stats?" + prev.Encode(), "NextYearURL": "/stats?" + next.Encode(), "CanNextYear": v.Year < s.svc.Today().Year(), "CanPrevYear": v.Year > 1,
	})
}
func (s *Server) statsCSV(w http.ResponseWriter, r *http.Request) {
	v, err := s.statsSelection(r)
	if err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Перевірте період і користувача.")
		return
	}
	history, err := s.svc.CategoryHistory(r.Context(), s.actor(r), store.TransactionFilter{From: &v.From, To: &v.To, OwnerUserID: v.UserID}, v.Kind)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="skarb-categories.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	header := []string{"Категорія · " + string(v.Kind) + " · UAH"}
	for _, m := range history.Months {
		header = append(header, m.From.String()+" — "+m.To.String())
	}
	header = append(header, "Разом")
	_ = cw.Write(header)
	amount := func(p service.CategoryMonth) string {
		value := money.PlainAmount(p.AmountUAH, 2)
		if p.MissingRate > 0 {
			value += " (неповно: без курсу " + strconv.Itoa(p.MissingRate) + ")"
		}
		return value
	}
	for _, row := range history.Rows {
		cells := []string{row.Category.Name}
		for _, p := range row.Months {
			cells = append(cells, amount(p))
		}
		cells = append(cells, amount(row.Total))
		_ = cw.Write(cells)
	}
	totals := []string{"Разом"}
	for _, p := range history.Totals {
		totals = append(totals, amount(p))
	}
	totals = append(totals, amount(history.Total))
	_ = cw.Write(totals)
	cw.Flush()
}
