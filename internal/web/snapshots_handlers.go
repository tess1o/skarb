package web

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/service"
)

type accountView struct {
	Account domain.Account
	Latest  *domain.Snapshot
	History []domain.Snapshot
	Balance *domain.AccountBalance
}

type currencyTotalView struct {
	Currency string
	Balance  int64
}

type savingsPointView struct {
	Date        domain.Date
	Total       int64
	Delta       int64
	Pct         float64
	MissingRate int
	HasRate     bool
	HasPrevious bool
}

func (s *Server) snapshotsPage(w http.ResponseWriter, r *http.Request) {
	actor := s.actor(r)
	targetCurrency := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency")))
	if targetCurrency == "" || !money.IsSupported(targetCurrency) {
		targetCurrency = "UAH"
	}
	includeArchived := currentUser(r.Context()).IsAdmin() && r.URL.Query().Get("archived") == "1"
	accounts, err := s.svc.ListAccounts(r.Context(), actor, includeArchived)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	netWorth, err := s.svc.NetWorth(r.Context(), actor, s.svc.Today())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	balances := map[int64]*domain.AccountBalance{}
	byCurrency := map[string]int64{}
	for i := range netWorth.Accounts {
		balances[netWorth.Accounts[i].AccountID] = &netWorth.Accounts[i]
		byCurrency[netWorth.Accounts[i].Currency] += netWorth.Accounts[i].BalanceMinor
	}
	var currencyTotals []currencyTotalView
	for currency, balance := range byCurrency {
		currencyTotals = append(currencyTotals, currencyTotalView{Currency: currency, Balance: balance})
	}
	sort.Slice(currencyTotals, func(i, j int) bool { return currencyTotals[i].Currency < currencyTotals[j].Currency })
	var views []accountView
	var snapshotAccounts []accountView
	for _, a := range accounts {
		history, _ := s.svc.ListSnapshots(r.Context(), actor, a.UID, nil, nil)
		v := accountView{Account: a, Balance: balances[a.ID]}
		if len(history) > 0 {
			v.Latest = &history[0]
			if len(history) > 12 {
				v.History = history[:12]
			} else {
				v.History = history
			}
		}
		views = append(views, v)
		if !a.Archived() {
			snapshotAccounts = append(snapshotAccounts, v)
		}
	}
	users, _ := s.svc.ListUsers(r.Context(), actor, false)
	targetTotal, targetTotalOK, err := s.convertUAH(r, netWorth.TotalUAH, targetCurrency, netWorth.AsOf)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	history, err := s.svc.SnapshotHistory(r.Context(), actor, 12)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	var savings []savingsPointView
	var savingsMax int64
	var previous int64
	hasPrevious := false
	for _, point := range history {
		total, ok, err := s.convertUAH(r, point.TotalUAH, targetCurrency, point.Date)
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
			return
		}
		view := savingsPointView{Date: point.Date, Total: total, MissingRate: point.MissingRate, HasRate: ok}
		if ok && hasPrevious {
			view.HasPrevious = true
			view.Delta = total - previous
			if previous != 0 {
				view.Pct = float64(view.Delta) / float64(previous) * 100
			}
		}
		if ok {
			previous = total
			hasPrevious = true
			if total > savingsMax {
				savingsMax = total
			}
		} else {
			hasPrevious = false
		}
		savings = append(savings, view)
	}
	s.render(w, r, "snapshots.html", map[string]any{
		"Title":            "Заощадження",
		"Accounts":         views,
		"NetWorth":         netWorth,
		"CurrencyTotals":   currencyTotals,
		"TargetCurrency":   targetCurrency,
		"TargetTotal":      targetTotal,
		"TargetTotalOK":    targetTotalOK,
		"Savings":          savings,
		"SavingsMax":       savingsMax,
		"SnapshotAccounts": snapshotAccounts,
		"Users":            users,
		"Currencies":       money.SupportedCurrencies(),
		"IncludeArchived":  includeArchived,
		"Today":            s.svc.Today(),
	})
}

func (s *Server) convertUAH(r *http.Request, amount int64, currency string, date domain.Date) (int64, bool, error) {
	if currency == "UAH" || amount == 0 {
		return amount, true, nil
	}
	rate, _, ok, err := s.svc.FX.RateFor(r.Context(), currency, date)
	if err != nil || !ok {
		return 0, ok, err
	}
	converted, err := money.ConvertFromUAH(amount, currency, rate)
	return converted, err == nil, err
}

func (s *Server) accountCreate(w http.ResponseWriter, r *http.Request) {
	ownerID, _ := strconv.ParseInt(r.FormValue("owner"), 10, 64)
	in := service.CreateAccountInput{
		OwnerUserID:       ownerID,
		Name:              strings.TrimSpace(r.FormValue("name")),
		Currency:          strings.ToUpper(strings.TrimSpace(r.FormValue("currency"))),
		Kind:              domain.AccountKind(r.FormValue("kind")),
		IncludeInNetWorth: r.FormValue("include") == "1",
		SortOrder:         atoiDefault(r.FormValue("sort"), 0),
	}
	if _, err := s.svc.CreateAccount(r.Context(), s.actor(r), in); err != nil {
		s.setFlash(w, "error", "Не вдалося створити рахунок: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Рахунок створено.")
	}
	s.redirect(w, r, "/snapshots")
}

func (s *Server) accountUpdate(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	name := strings.TrimSpace(r.FormValue("name"))
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	kind := domain.AccountKind(r.FormValue("kind"))
	include := r.FormValue("include") == "1"
	archived := r.FormValue("archived") == "1"
	sort := atoiDefault(r.FormValue("sort"), 0)
	in := service.UpdateAccountInput{
		Name:              &name,
		Currency:          &currency,
		Kind:              &kind,
		IncludeInNetWorth: &include,
		SortOrder:         &sort,
		Archived:          &archived,
	}
	if _, err := s.svc.UpdateAccount(r.Context(), s.actor(r), uid, in); err != nil {
		s.setFlash(w, "error", "Не вдалося оновити рахунок: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Рахунок оновлено.")
	}
	s.redirect(w, r, "/snapshots")
}

func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteAccount(r.Context(), s.actor(r), r.PathValue("uid")); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити рахунок: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Рахунок видалено.")
	}
	s.redirect(w, r, "/snapshots")
}

func (s *Server) snapshotUpsert(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	accounts, amounts := r.Form["account"], r.Form["amount"]
	in := service.SaveSnapshotInput{Date: r.FormValue("date")}
	if len(accounts) == len(amounts) {
		for i := range accounts {
			in.Balances = append(in.Balances, service.SnapshotBalanceInput{AccountUID: accounts[i], Amount: amounts[i]})
		}
	}
	if len(accounts) != len(amounts) {
		s.setFlash(w, "error", "Не вдалося зберегти знімок: неповні дані рахунків")
	} else if err := s.svc.SaveSnapshot(r.Context(), s.actor(r), in); err != nil {
		s.setFlash(w, "error", "Не вдалося зберегти знімок: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Знімок капіталу збережено.")
	}
	query := url.Values{}
	if currency := strings.TrimSpace(r.FormValue("currency")); currency != "" && money.IsSupported(currency) {
		query.Set("currency", strings.ToUpper(currency))
	}
	path := "/snapshots"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	s.redirect(w, r, path)
}

func (s *Server) snapshotDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteSnapshot(r.Context(), s.actor(r), r.PathValue("uid")); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити залишок: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Залишок видалено.")
	}
	s.redirect(w, r, "/snapshots")
}

func atoiDefault(s string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return v
}
