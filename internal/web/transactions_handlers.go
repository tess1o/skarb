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
		s.renderError(w, r, http.StatusInternalServerError, "Не вдалося завантажити транзакції: "+s.errText(r, err))
		return
	}
	categories, _ := s.svc.ListCategories(r.Context(), actor, false)
	// Keep an archived category visible when arriving from historical statistics.
	if uid := r.URL.Query().Get("category"); uid != "" {
		if selected, err := s.svc.CategoryByUID(r.Context(), actor, uid); err == nil && selected.Archived() {
			categories = append(categories, *selected)
		}
	}

	users, _ := s.svc.ListUsers(r.Context(), actor, false)
	activeFilters := 0
	for _, key := range []string{"from", "to", "user", "category", "kind", "source", "q", "deleted"} {
		if r.URL.Query().Get(key) != "" {
			activeFilters++
		}
	}
	pages := (total + 49) / 50
	if pages > 0 && page > pages {
		// E.g. after deleting every row on the last page: show the new last page.
		page = pages
		f.Offset = (page - 1) * 50
		if rows, total, err = s.svc.ListTransactions(r.Context(), actor, f); err != nil {
			s.renderError(w, r, http.StatusInternalServerError, "Не вдалося завантажити транзакції: "+s.errText(r, err))
			return
		}
	}
	s.render(w, r, "transactions.html", map[string]any{
		"Title":         "Транзакції",
		"Transactions":  rows,
		"Total":         total,
		"Page":          page,
		"Pages":         pages,
		"PrevPage":      page - 1,
		"NextPage":      page + 1,
		"BaseQuery":     baseQuery,
		"Categories":    categories,
		"Users":         users,
		"ActiveFilters": activeFilters,
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
	q := r.URL.Query()
	form := map[string]string{
		"date":     s.svc.Today().String(),
		"currency": "UAH",
		"owner":    fmt.Sprint(currentUser(r.Context()).ID),
		"category": q.Get("category"),
	}
	// "Зберегти й додати ще" carries the date and currency over to the next entry.
	if d, err := domain.ParseDate(q.Get("date")); err == nil {
		form["date"] = d.String()
	}
	if c := q.Get("currency"); c != "" {
		form["currency"] = c
	}
	if form["category"] == "" {
		// Default to the category of the user's latest expense: usually the next one too.
		actor := s.actor(r)
		expense := domain.KindExpense
		rows, _, err := s.svc.ListTransactions(r.Context(), actor, store.TransactionFilter{
			OwnerUserID: &actor.UserID, Kind: &expense, Limit: 1,
		})
		if err == nil && len(rows) > 0 {
			form["category"] = fmt.Sprint(rows[0].CategoryID)
		}
	}
	s.renderTransactionForm(w, r, nil, form, "", "/transactions/new")
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
		s.renderTransactionForm(w, r, nil, form, s.errText(r, err), "/transactions/new")
		return
	}
	if r.FormValue("again") != "" {
		s.setFlash(w, "success", "Транзакцію додано. Додайте наступну.")
		next := url.Values{"category": {form["category"]}, "date": {form["date"]}, "currency": {form["currency"]}}
		s.redirect(w, r, "/transactions/new?"+next.Encode())
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
		s.renderTransactionForm(w, r, tx, form, s.errText(r, err), "/transactions/"+uid+"/edit")
		return
	}
	s.setFlash(w, "success", "Транзакцію оновлено.")
	s.redirect(w, r, "/transactions/"+uid)
}

func (s *Server) transactionDelete(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	if err := s.svc.DeleteTransaction(r.Context(), s.actor(r), uid, r.FormValue("reason")); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити транзакцію: "+s.errText(r, err))
	} else {
		s.setFlashUndo(w, "Транзакцію видалено.", []string{uid})
	}
	s.redirect(w, r, nextOr(r, "/transactions"))
}

func (s *Server) transactionsBulkDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Некоректна форма.")
		return
	}
	deleted, err := s.svc.DeleteTransactions(r.Context(), s.actor(r), r.PostForm["uid"], r.PostFormValue("reason"))
	switch n := len(deleted); {
	case err != nil:
		s.setFlash(w, "error", "Не вдалося видалити транзакції: "+s.errText(r, err))
	case n == 0:
		s.setFlash(w, "warning", "Вибрані транзакції вже видалено.")
	default:
		s.setFlashUndo(w, fmt.Sprintf("Видалено %d %s.", n, pluralUK(n, "транзакцію", "транзакції", "транзакцій")), deleted)
	}
	s.redirect(w, r, nextOr(r, "/transactions"))
}

// transactionsRestore undeletes the posted uids: the flash's undo button and
// the "Відновити" buttons on deleted rows both post here.
func (s *Server) transactionsRestore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Некоректна форма.")
		return
	}
	n, err := s.svc.RestoreTransactions(r.Context(), s.actor(r), r.PostForm["uid"])
	switch {
	case err != nil:
		s.setFlash(w, "error", "Не вдалося відновити: "+s.errText(r, err))
	case n == 0:
		s.setFlash(w, "warning", "Нічого відновлювати: транзакції не видалені.")
	default:
		s.setFlash(w, "success", fmt.Sprintf("Відновлено %d %s.", n, pluralUK(n, "транзакцію", "транзакції", "транзакцій")))
	}
	s.redirect(w, r, nextOr(r, "/transactions"))
}

// nextOr returns the form's local "next" path, or fallback when it is unset or unsafe.
func nextOr(r *http.Request, fallback string) string {
	if next := safeNext(r.FormValue("next")); next != "" {
		return next
	}
	return fallback
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
	// The type toggle follows the selected category; new entries default to expense.
	kind := domain.KindExpense
	var expense, income []domain.Category
	for _, c := range categories {
		if fmt.Sprint(c.ID) == form["category"] {
			kind = c.Kind
		}
		if c.Kind == domain.KindIncome {
			income = append(income, c)
		} else {
			expense = append(expense, c)
		}
	}
	s.render(w, r, "transaction_form.html", map[string]any{
		"Title":       title,
		"Kind":        string(kind),
		"Expense":     expense,
		"Income":      income,
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
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
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
