package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/nlu"
	"skarb/internal/service"
	"skarb/internal/store"
)

func (s *Server) adminSettingsPage(w http.ResponseWriter, r *http.Request) {
	actor := s.actor(r)
	llm, err := s.svc.GetLLMConfig(r.Context())
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	chats, _ := s.svc.ListTelegramChats(r.Context(), actor)
	reminders, _ := s.svc.ListReminderConfigs(r.Context(), actor)
	users, _ := s.svc.ListUsers(r.Context(), actor, true)
	tokens, _ := s.svc.ListAllAPITokens(r.Context(), actor)
	from := s.svc.Today().AddDays(-30)
	rates, _ := s.svc.FX.List(r.Context(), "", &from, nil)
	seen := map[string]bool{}
	var latestRates []domain.ExchangeRate
	for _, rate := range rates {
		if !seen[rate.Currency] {
			seen[rate.Currency] = true
			latestRates = append(latestRates, rate)
		}
	}
	s.render(w, r, "settings.html", map[string]any{
		"Title":         "Налаштування",
		"LLM":           llm,
		"LLMConfigured": s.svc.LLMConfigured(llm),
		"LLMKeySet":     s.svc.Cfg.LLMAPIKey != "",
		"Chats":         chats,
		"Reminders":     reminders,
		"Users":         users,
		"Tokens":        tokens,
		"Rates":         latestRates,
		"Currencies":    money.SupportedCurrencies(),
		"Today":         s.svc.Today(),
		"PublicBaseURL": s.svc.Cfg.PublicBaseURL,
	})
}

func (s *Server) adminLLMUpdate(w http.ResponseWriter, r *http.Request) {
	temp, _ := strconv.ParseFloat(strings.ReplaceAll(r.FormValue("temperature"), ",", "."), 64)
	cfg := service.LLMConfig{
		BaseURL:         r.FormValue("base_url"),
		Model:           r.FormValue("model"),
		SchemaMode:      r.FormValue("schema_mode"),
		TimeoutSeconds:  atoiDefault(r.FormValue("timeout_seconds"), 15),
		MaxOutputTokens: atoiDefault(r.FormValue("max_output_tokens"), 800),
		MaxTransactions: atoiDefault(r.FormValue("max_transactions"), 10),
		MaxInputChars:   atoiDefault(r.FormValue("max_input_chars"), 2000),
		Temperature:     temp,
	}
	if err := s.svc.SetLLMConfig(r.Context(), s.actor(r), cfg); err != nil {
		s.setFlash(w, "error", "Не вдалося зберегти налаштування ШІ: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Налаштування ШІ збережено.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminLLMTest(w http.ResponseWriter, r *http.Request) {
	parser := nlu.NewParser(s.svc, s.log)
	intent, err := parser.ParseLLM(r.Context(), "сьогодні 512 грн за їжу")
	if err != nil {
		s.setFlash(w, "error", "ШІ не пройшов перевірку: "+err.Error())
	} else {
		s.setFlash(w, "success", fmt.Sprintf("ШІ працює: розпізнано транзакцій — %d.", len(intent.Transactions)))
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminReminderTest(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	u := currentUser(r.Context())
	if u == nil || u.TelegramUserID == nil {
		s.setFlash(w, "error", "Спочатку прив'яжіть Telegram user_id до свого акаунта.")
		s.redirect(w, r, "/admin/settings")
		return
	}
	if s.OnTestReminder == nil {
		s.setFlash(w, "error", "Telegram-бот не налаштований.")
		s.redirect(w, r, "/admin/settings")
		return
	}
	if err := s.OnTestReminder(kind, *u.TelegramUserID); err != nil {
		s.setFlash(w, "error", "Тестове нагадування не надіслано: "+err.Error())
	} else {
		s.setFlash(w, "success", "Тестове нагадування надіслано в Telegram.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminTelegramAdd(w http.ResponseWriter, r *http.Request) {
	chatID, err := strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
	if err != nil {
		s.setFlash(w, "error", "Chat ID має бути числом.")
		s.redirect(w, r, "/admin/settings")
		return
	}
	if err := s.svc.AddTelegramChat(r.Context(), s.actor(r), chatID,
		r.FormValue("title"), r.FormValue("chat_type")); err != nil {
		s.setFlash(w, "error", "Не вдалося додати чат: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Чат додано.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminTelegramRemove(w http.ResponseWriter, r *http.Request) {
	chatID, err := strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
	if err != nil {
		s.setFlash(w, "error", "Chat ID має бути числом.")
		s.redirect(w, r, "/admin/settings")
		return
	}
	if err := s.svc.RemoveTelegramChat(r.Context(), s.actor(r), chatID); err != nil {
		s.setFlash(w, "error", "Не вдалося видалити чат: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Чат видалено.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminReminderUpdate(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	existing, err := s.svc.GetReminderConfig(r.Context(), kind)
	if err != nil {
		s.setFlash(w, "error", "Невідомий тип нагадування.")
		s.redirect(w, r, "/admin/settings")
		return
	}
	cfg := *existing
	cfg.CronExpr = r.FormValue("cron_expr")
	cfg.Enabled = r.FormValue("enabled") == "1"
	cfg.WindowDays = atoiDefault(r.FormValue("window_days"), cfg.WindowDays)
	cfg.TargetChatID = parseOptionalInt64(r.FormValue("target_chat_id"))
	cfg.TargetUserID = parseOptionalInt64(r.FormValue("target_user_id"))
	if tz := strings.TrimSpace(r.FormValue("timezone")); tz != "" {
		cfg.Timezone = tz
	}
	if err := s.svc.UpdateReminderConfig(r.Context(), s.actor(r), cfg); err != nil {
		s.setFlash(w, "error", "Не вдалося зберегти нагадування: "+s.errText(r, err))
	} else {
		if s.OnRemindersChanged != nil {
			s.OnRemindersChanged()
		}
		s.setFlash(w, "success", "Нагадування збережено.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminFXRefresh(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.FX.FetchLatest(r.Context()); err != nil {
		s.setFlash(w, "error", "Не вдалося оновити курси: "+err.Error())
	} else {
		s.setFlash(w, "success", "Курси NBU оновлено.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminFXOverride(w http.ResponseWriter, r *http.Request) {
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	date, err := domain.ParseDate(r.FormValue("date"))
	if err != nil {
		date = s.svc.Today()
	}
	rate := r.FormValue("rate")
	if err := s.svc.FX.ManualOverride(r.Context(), currency, date, rate, s.actor(r).UserID); err != nil {
		s.setFlash(w, "error", "Не вдалося зберегти курс: "+s.errText(r, err))
	} else {
		_ = s.svc.Store.InsertAudit(r.Context(), nil, store.AuditEntry{
			Actor: s.actor(r), Action: "fx.manual_override", EntityType: "exchange_rate",
			EntityUID: currency + "@" + date.String(),
			AfterJSON: fmt.Sprintf(`{"currency":%q,"date":%q,"rate":%q}`, currency, date.String(), rate),
		})
		s.setFlash(w, "success", "Курс збережено.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminTokenCreate(w http.ResponseWriter, r *http.Request) {
	plain, _, err := s.svc.CreateAPIToken(r.Context(), s.actor(r), service.APITokenCreateInput{
		TargetUserUID: r.FormValue("user"),
		Name:          r.FormValue("name"),
	})
	if err != nil {
		s.setFlash(w, "error", "Не вдалося створити токен: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Токен створено. Скопіюйте його зараз: "+plain)
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) adminTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RevokeAPIToken(r.Context(), s.actor(r), r.PathValue("uid")); err != nil {
		s.setFlash(w, "error", "Не вдалося відкликати токен: "+s.errText(r, err))
	} else {
		s.setFlash(w, "success", "Токен відкликано.")
	}
	s.redirect(w, r, "/admin/settings")
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request) {
	f := store.AuditFilter{
		Action:     r.URL.Query().Get("action"),
		EntityType: r.URL.Query().Get("entity_type"),
		EntityUID:  r.URL.Query().Get("entity_uid"),
		Limit:      200,
	}
	if uid := r.URL.Query().Get("actor"); uid != "" {
		if u, err := s.svc.UserByUID(r.Context(), uid); err == nil {
			f.ActorUserID = &u.ID
		}
	}
	if v := r.URL.Query().Get("from"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			t := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
			f.From = &t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if d, err := domain.ParseDate(v); err == nil {
			t := time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, time.UTC)
			f.To = &t
		}
	}
	rows, err := s.svc.ListAudit(r.Context(), s.actor(r), f)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	users, _ := s.svc.ListUsers(r.Context(), s.actor(r), true)
	s.render(w, r, "audit.html", map[string]any{
		"Title": "Журнал дій",
		"Rows":  rows,
		"Users": users,
		"Filter": map[string]string{
			"action":      f.Action,
			"entity_type": f.EntityType,
			"entity_uid":  f.EntityUID,
			"actor":       r.URL.Query().Get("actor"),
			"from":        r.URL.Query().Get("from"),
			"to":          r.URL.Query().Get("to"),
		},
	})
}

func (s *Server) backupDownload(w http.ResponseWriter, r *http.Request) {
	backupDir := filepath.Join(filepath.Dir(s.svc.Store.Path), "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, s.errText(r, err))
		return
	}
	name := "download-" + time.Now().UTC().Format("20060102T150405Z") + ".db"
	target := filepath.Join(backupDir, name)
	stmt := "VACUUM INTO '" + strings.ReplaceAll(target, "'", "''") + "'"
	if _, err := s.svc.Store.DB.ExecContext(r.Context(), stmt); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Не вдалося створити резервну копію: "+s.errText(r, err))
		return
	}
	defer os.Remove(target)
	_ = os.Chmod(target, 0o600)
	_ = s.svc.Store.InsertAudit(r.Context(), nil, store.AuditEntry{
		Actor: s.actor(r), Action: "backup.downloaded", EntityType: "backup", EntityUID: name,
	})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	http.ServeFile(w, r, target)
}
