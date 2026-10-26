package web

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/service"
	"skarb/internal/store"
)

func (s *Server) transactionsPage(w http.ResponseWriter, r *http.Request) {
	actor := s.actor(r)
	f, baseQuery, page := s.transactionFilters(r)
	f.Limit = 50
	f.Offset = (page - 1) * 50
	rows, total, err := s.svc.ListTransactions(r.Context(), actor, f)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Не вдалося завантажити транзакції: "+err.Error())
		return
	}
	categories, _ := s.svc.ListCategories(r.Context(), actor, false)
	users, _ := s.svc.ListUsers(r.Context(), actor, false)
	pages := (total + 49) / 50
	s.render(w, r, "transactions.html", map[string]any{
		"Title":        "Транзакції",
		"Transactions": rows,
		"Total":        total,
		"Page":         page,
		"Pages":        pages,
		"PrevPage":     page - 1,
		"NextPage":     page + 1,
		"BaseQuery":    baseQuery,
		"Categories":   categories,
		"Users":        users,
		"Filter": map[string]string{
			"from":     r.URL.Query().Get("from"),
			"to":       r.URL.Query().Get("to"),
			"user":     r.URL.Query().Get("user"),
			"category": r.URL.Query().Get("category"),
			"kind":     r.URL.Query().Get("kind"),
			"source":   r.URL.Query().Get("source"),
			"q":        r.URL.Query().Get("q"),
			"deleted":  r.URL.Query().Get("deleted"),
		},
	})
}

func (s *Server) transactionFilters(r *http.Request) (store.TransactionFilter, string, int) {
	q := r.URL.Query()
	f := store.TransactionFilter{Search: q.Get("q")}
	if v := q.Get("from"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			f.From = &d
		}
	}
	if v := q.Get("to"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			f.To = &d
		}
	}
	if v := q.Get("user"); v != "" {
		if u, err := s.svc.Store.GetUserByUID(r.Context(), v); err == nil {
			f.OwnerUserID = &u.ID
		}
	}
	if v := q.Get("category"); v != "" {
		if c, err := s.svc.Store.GetCategoryByUID(r.Context(), v); err == nil {
			f.CategoryID = &c.ID
		}
	}
	switch q.Get("kind") {
	case "income":
		k := domain.KindIncome
		f.Kind = &k
	case "expense":
		k := domain.KindExpense
		f.Kind = &k
	}
	switch q.Get("source") {
	case "web":
		src := domain.SourceWeb
		f.Source = &src
	case "telegram":
		src := domain.SourceTelegram
		f.Source = &src
	case "mcp":
		src := domain.SourceMCP
		f.Source = &src
	}
	if u := currentUser(r.Context()); u != nil && u.IsAdmin() && q.Get("deleted") == "1" {
		f.IncludeDeleted = true
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	base := url.Values{}
	for _, key := range []string{"from", "to", "user", "category", "kind", "source", "q", "deleted"} {
		if v := q.Get(key); v != "" {
			base.Set(key, v)
		}
	}
	return f, base.Encode(), page
}

func (s *Server) transactionNewPage(w http.ResponseWriter, r *http.Request) {
	s.renderTransactionForm(w, r, nil, map[string]string{
		"date":     s.svc.Today().String(),
		"currency": "UAH",
		"owner":    fmt.Sprint(currentUser(r.Context()).ID),
	}, "", "/transactions/new")
}

func (s *Server) transactionCreate(w http.ResponseWriter, r *http.Request) {
	form := formStrings(r, "amount", "currency", "date", "category", "description", "note", "owner")
	if form["owner"] == "" {
		form["owner"] = fmt.Sprint(currentUser(r.Context()).ID)
	}
	item := service.TransactionItemInput{
		Amount:      form["amount"],
		Currency:    form["currency"],
		Date:        form["date"],
		Category:    form["category"],
		Description: form["description"],
		Note:        form["note"],
	}
	if form["owner"] != "" {
		if id, err := strconv.ParseInt(form["owner"], 10, 64); err == nil {
			item.OwnerUserID = &id
		}
	}
	_, err := s.svc.CreateTransaction(r.Context(), s.actor(r), item, domain.SourceWeb, form["note"])
	if err != nil {
		s.renderTransactionForm(w, r, nil, form, err.Error(), "/transactions/new")
		return
	}
	s.setFlash(w, "success", "Транзакцію додано.")
	s.redirect(w, r, "/transactions")
}

func (s *Server) transactionEditPage(w http.ResponseWriter, r *http.Request) {
	tx, err := s.svc.GetTransaction(r.Context(), s.actor(r), r.PathValue("uid"))
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Транзакцію не знайдено.")
		return
	}
	form := map[string]string{
		"amount":      money.FormatScaled(tx.AmountMinor, money.MinorUnits(tx.Currency)),
		"currency":    tx.Currency,
		"date":        tx.OccurredOn.String(),
		"category":    fmt.Sprint(tx.CategoryID),
		"description": tx.Description,
		"note":        tx.Note,
		"owner":       fmt.Sprint(tx.OwnerUserID),
	}
	s.renderTransactionForm(w, r, tx, form, "", "/transactions/"+tx.UID+"/edit")
}

func (s *Server) transactionUpdate(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	tx, err := s.svc.GetTransaction(r.Context(), s.actor(r), uid)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Транзакцію не знайдено.")
		return
	}
	form := formStrings(r, "amount", "currency", "date", "category", "description", "note", "owner")
	in := service.UpdateTransactionInput{
		Amount:      strPtr(form["amount"]),
		Currency:    strPtr(form["currency"]),
		Date:        strPtr(form["date"]),
		Category:    strPtr(form["category"]),
		Description: strPtr(form["description"]),
		Note:        strPtr(form["note"]),
	}
	if currentUser(r.Context()).IsAdmin() && form["owner"] != "" {
		if id, err := strconv.ParseInt(form["owner"], 10, 64); err == nil {
			in.OwnerUserID = &id
		}
	}
	if _, err := s.svc.UpdateTransaction(r.Context(), s.actor(r), uid, in); err != nil {
		s.renderTransactionForm(w, r, tx, form, err.Error(), "/transactions/"+uid+"/edit")
		return
	}
	s.setFlash(w, "success", "Транзакцію оновлено.")
	s.redirect(w, r, "/transactions/"+uid)
}

func (s *Server) transactionDelete(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if err := s.svc.DeleteTransaction(r.Context(), s.actor(r), uid, r.FormValue("reason")); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити транзакцію: "+err.Error())
	} else {
		s.setFlash(w, "success", "Транзакцію видалено.")
	}
	s.redirect(w, r, "/transactions")
}

func (s *Server) transactionDetailPage(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	tx, err := s.svc.GetTransaction(r.Context(), s.actor(r), uid)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Транзакцію не знайдено.")
		return
	}
	versions, _ := s.svc.TransactionVersions(r.Context(), s.actor(r), uid)
	s.render(w, r, "transaction_detail.html", map[string]any{
		"Title":       "Транзакція",
		"Transaction": tx,
		"Versions":    versions,
	})
}

func (s *Server) renderTransactionForm(w http.ResponseWriter, r *http.Request, tx *domain.Transaction, form map[string]string, errMsg, action string) {
	categories, _ := s.svc.ListCategories(r.Context(), s.actor(r), false)
	users, _ := s.svc.ListUsers(r.Context(), s.actor(r), false)
	title := "Нова транзакція"
	if tx != nil {
		title = "Редагування транзакції"
	}
	s.render(w, r, "transaction_form.html", map[string]any{
		"Title":       title,
		"Transaction": tx,
		"Form":        form,
		"Error":       errMsg,
		"Action":      action,
		"Categories":  categories,
		"Users":       users,
		"Currencies":  money.SupportedCurrencies(),
		"IsAdmin":     currentUser(r.Context()).IsAdmin(),
	})
}

func (s *Server) transactionsCSV(w http.ResponseWriter, r *http.Request) {
	f, _, _ := s.transactionFilters(r)
	f.Limit = -1
	f.Offset = 0
	rows, _, err := s.svc.ListTransactions(r.Context(), s.actor(r), f)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="skarb-transactions.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Дата", "Тип", "Категорія", "Сума", "Валюта", "Опис", "Нотатка", "Користувач", "Джерело"})
	for _, t := range rows {
		_ = cw.Write([]string{
			t.OccurredOn.String(), string(t.Kind), t.CategoryName,
			money.PlainAmount(t.AmountMinor, money.MinorUnits(t.Currency)), t.Currency,
			t.Description, t.Note, t.OwnerName, string(t.Source),
		})
	}
	cw.Flush()
}

func formStrings(r *http.Request, keys ...string) map[string]string {
	out := map[string]string{}
	for _, key := range keys {
		out[key] = strings.TrimSpace(r.FormValue(key))
	}
	return out
}

func strPtr(s string) *string { return &s }

func parseID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("empty id")
	}
	return strconv.ParseInt(raw, 10, 64)
}
