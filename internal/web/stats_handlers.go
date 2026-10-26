package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/service"
	"skarb/internal/store"
)

func (s *Server) statsPage(w http.ResponseWriter, r *http.Request) {
	actor := s.actor(r)
	today := s.svc.Today()
	from, to := currentMonthRange(today)
	if v := r.URL.Query().Get("from"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			from = d
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			to = d
		}
	}
	if to.Before(from) {
		from, to = to, from
	}
	kind := domain.KindExpense
	if r.URL.Query().Get("kind") == "income" {
		kind = domain.KindIncome
	}
	var userID *int64
	if u := r.URL.Query().Get("user"); u != "" {
		if user, err := s.svc.Store.GetUserByUID(r.Context(), u); err == nil {
			userID = &user.ID
		}
	}
	f := store.TransactionFilter{From: &from, To: &to, OwnerUserID: userID}
	summary, err := s.svc.Summary(r.Context(), actor, f)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	categories, err := s.svc.CategoryBreakdown(r.Context(), actor, f, kind, 0)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	users, err := s.svc.UserBreakdown(r.Context(), actor, f)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	days := int(to.Time.Sub(from.Time).Hours()/24) + 1
	granularity := "day"
	if days > 62 {
		granularity = "month"
	}
	trend, err := s.svc.Trend(r.Context(), actor, from, to, granularity)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	prevTo := from.AddDays(-1)
	prevFrom := prevTo.AddDays(-(days - 1))
	comparison, err := s.svc.Compare(r.Context(), actor, domain.DateRange{From: from, To: to}, domain.DateRange{From: prevFrom, To: prevTo}, userID)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	allUsers, _ := s.svc.ListUsers(r.Context(), actor, false)
	s.render(w, r, "stats.html", map[string]any{
		"Title":       "Статистика",
		"From":        from,
		"To":          to,
		"FromValue":   from.String(),
		"ToValue":     to.String(),
		"Kind":        kind,
		"Summary":     summary,
		"Categories":  categories,
		"Users":       users,
		"AllUsers":    allUsers,
		"Trend":       trend,
		"Comparison":  comparison,
		"Granularity": granularity,
		"UserFilter":  r.URL.Query().Get("user"),
	})
}

func (s *Server) statsCSV(w http.ResponseWriter, r *http.Request) {
	actor := s.actor(r)
	today := s.svc.Today()
	from, to := currentMonthRange(today)
	if v := r.URL.Query().Get("from"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			from = d
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			to = d
		}
	}
	kind := domain.KindExpense
	if r.URL.Query().Get("kind") == "income" {
		kind = domain.KindIncome
	}
	rows, err := s.svc.CategoryBreakdown(r.Context(), actor, store.TransactionFilter{From: &from, To: &to}, kind, 0)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="skarb-stats.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Період", "Тип", "Категорія", "Сума UAH", "Кількість", "Відсоток"})
	for _, row := range rows {
		_ = cw.Write([]string{
			from.String() + "—" + to.String(), string(kindLabel(kind)), row.Name,
			money.PlainAmount(row.AmountUAH, 2), strconv.Itoa(row.Count),
			fmt.Sprintf("%.1f", row.Percent),
		})
	}
	cw.Flush()
}

func currentMonthRange(today domain.Date) (domain.Date, domain.Date) {
	return domain.NewDate(today.Year(), today.Month(), 1), service.MonthEnd(today)
}

func kindLabel(k domain.Kind) string {
	if k == domain.KindIncome {
		return "income"
	}
	return "expense"
}
