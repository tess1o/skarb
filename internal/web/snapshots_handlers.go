package web

import (
	"net/http"
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

func (s *Server) snapshotsPage(w http.ResponseWriter, r *http.Request) {
	actor := s.actor(r)
	includeArchived := currentUser(r.Context()).IsAdmin() && r.URL.Query().Get("archived") == "1"
	accounts, err := s.svc.ListAccounts(r.Context(), actor, includeArchived)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	netWorth, err := s.svc.NetWorth(r.Context(), actor, s.svc.Today())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	balances := map[int64]*domain.AccountBalance{}
	for i := range netWorth.Accounts {
		balances[netWorth.Accounts[i].AccountID] = &netWorth.Accounts[i]
	}
	var views []accountView
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
	}
	users, _ := s.svc.ListUsers(r.Context(), actor, false)
	today := s.svc.Today()
	historyFrom := domain.NewDate(today.Year(), today.Month(), 1)
	for i := 0; i < 11; i++ {
		historyFrom = domain.DateOf(historyFrom.Time.AddDate(0, -1, 0))
	}
	savings, _ := s.svc.SavingsHistory(r.Context(), actor, historyFrom, today, "month")
	var savingsMax int64
	for _, point := range savings {
		if point.TotalUAH > savingsMax {
			savingsMax = point.TotalUAH
		}
	}
	s.render(w, r, "snapshots.html", map[string]any{
		"Title":           "Заощадження",
		"Accounts":        views,
		"NetWorth":        netWorth,
		"Savings":         savings,
		"SavingsMax":      savingsMax,
		"Users":           users,
		"Currencies":      money.SupportedCurrencies(),
		"IncludeArchived": includeArchived,
		"Today":           today,
	})
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
		s.setFlash(w, "error", "Не вдалося створити рахунок: "+err.Error())
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
		s.setFlash(w, "error", "Не вдалося оновити рахунок: "+err.Error())
	} else {
		s.setFlash(w, "success", "Рахунок оновлено.")
	}
	s.redirect(w, r, "/snapshots")
}

func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteAccount(r.Context(), s.actor(r), r.PathValue("uid")); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити рахунок: "+err.Error())
	} else {
		s.setFlash(w, "success", "Рахунок видалено.")
	}
	s.redirect(w, r, "/snapshots")
}

func (s *Server) snapshotUpsert(w http.ResponseWriter, r *http.Request) {
	in := service.UpsertSnapshotInput{
		AccountUID: r.FormValue("account"),
		Date:       r.FormValue("date"),
		Amount:     r.FormValue("amount"),
		Note:       r.FormValue("note"),
	}
	if _, err := s.svc.UpsertSnapshot(r.Context(), s.actor(r), in); err != nil {
		s.setFlash(w, "error", "Не вдалося зберегти залишок: "+err.Error())
	} else {
		s.setFlash(w, "success", "Залишок збережено.")
	}
	s.redirect(w, r, "/snapshots")
}

func (s *Server) snapshotDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteSnapshot(r.Context(), s.actor(r), r.PathValue("uid")); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити залишок: "+err.Error())
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
