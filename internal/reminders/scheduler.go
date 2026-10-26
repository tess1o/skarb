package reminders

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"skarb/internal/backup"
	"skarb/internal/domain"
	"skarb/internal/service"
)

type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

type Scheduler struct {
	svc    *service.Service
	sender Sender
	log    *slog.Logger
	loc    *time.Location

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[string]cron.EntryID
}

func New(svc *service.Service, sender Sender, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{svc: svc, sender: sender, log: logger, loc: svc.Loc, entries: map[string]cron.EntryID{}}
}

func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	s.cron = cron.New(cron.WithLocation(s.loc))
	s.mu.Unlock()
	if err := s.Reload(ctx); err != nil {
		s.log.Error("initial reminder schedule failed", "error", err)
	}
	s.addFixed(ctx, "fx", "10 16 * * *", func() {
		if err := s.svc.FX.FetchLatest(context.Background()); err != nil {
			s.log.Warn("daily FX refresh failed", "error", err)
		}
	})
	s.addFixed(ctx, "backup", "0 3 * * *", func() {
		dir := filepath.Join(filepath.Dir(s.svc.Store.Path), "backups")
		if _, err := backup.Create(context.Background(), s.svc.Store, dir, 14); err != nil {
			s.log.Warn("daily backup failed", "error", err)
		}
	})
	s.mu.Lock()
	s.cron.Start()
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	if s.cron != nil {
		stopCtx := s.cron.Stop()
		select {
		case <-stopCtx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	s.mu.Unlock()
}

func (s *Scheduler) addFixed(ctx context.Context, name, expr string, fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron == nil {
		return
	}
	if id, ok := s.entries["fixed:"+name]; ok {
		s.cron.Remove(id)
	}
	id, err := s.cron.AddFunc(expr, fn)
	if err != nil {
		s.log.Warn("add fixed cron failed", "name", name, "error", err)
		return
	}
	s.entries["fixed:"+name] = id
}

// Reload re-registers the two user-configurable reminder crons.
func (s *Scheduler) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron == nil {
		return nil
	}
	for name, id := range s.entries {
		if strings.HasPrefix(name, "reminder:") {
			s.cron.Remove(id)
			delete(s.entries, name)
		}
	}
	configs, err := s.svc.Store.ListReminderConfigs(ctx)
	if err != nil {
		return err
	}
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		kind := cfg.Kind
		id, err := s.cron.AddFunc(cfg.CronExpr, func() {
			s.runReminder(context.Background(), kind)
		})
		if err != nil {
			s.log.Warn("add reminder cron failed", "kind", kind, "error", err)
			continue
		}
		s.entries["reminder:"+kind] = id
	}
	return nil
}

// SendTest sends a short test message to the acting admin's Telegram DM.
func (s *Scheduler) SendTest(ctx context.Context, kind string, telegramUserID int64) error {
	if s.sender == nil {
		return fmt.Errorf("telegram bot is not configured")
	}
	label := "транзакцій"
	if kind == "snapshot" {
		label = "заощаджень"
	}
	return s.sender.SendMessage(ctx, telegramUserID, "🔔 Тестове нагадування Skarb: "+label+".")
}

func (s *Scheduler) runReminder(ctx context.Context, kind string) {
	scheduledFor := time.Now().In(s.loc).Truncate(time.Minute)
	inserted, err := s.svc.Store.RecordReminderRun(ctx, kind, scheduledFor)
	if err != nil || !inserted {
		return
	}
	status, err := s.sendReminder(ctx, kind)
	if err != nil {
		s.log.Warn("reminder failed", "kind", kind, "error", err)
		_ = s.svc.Store.FinishReminderRun(ctx, kind, scheduledFor, "failed", err.Error())
		return
	}
	_ = s.svc.Store.FinishReminderRun(ctx, kind, scheduledFor, status, "")
}

func (s *Scheduler) sendReminder(ctx context.Context, kind string) (string, error) {
	cfg, err := s.svc.Store.GetReminderConfig(ctx, kind)
	if err != nil {
		return "failed", err
	}
	if !cfg.Enabled {
		return "skipped", nil
	}
	if s.sender == nil {
		return "skipped", nil
	}
	today := s.svc.Today()
	switch kind {
	case "transactions":
		from := today.AddDays(-(cfg.WindowDays - 1))
		n, err := s.svc.Store.CountTransactionsInRange(ctx, from, today)
		if err != nil {
			return "failed", err
		}
		if n > 0 {
			_ = s.svc.Store.SetReminderLastRun(ctx, kind, false)
			return "skipped", nil
		}
		target, err := s.targetChat(ctx, cfg)
		if err != nil || target == 0 {
			return "skipped", err
		}
		text := fmt.Sprintf("🔔 За останні %d %s немає жодної транзакції.\nДодайте витрати або доходи: %s/transactions",
			cfg.WindowDays, pluralDays(cfg.WindowDays), s.svc.Cfg.PublicBaseURL)
		if err := s.sender.SendMessage(ctx, target, text); err != nil {
			return "failed", err
		}
		_ = s.svc.Store.SetReminderLastRun(ctx, kind, true)
		return "sent", nil
	case "snapshot":
		missing, err := s.missingAccounts(ctx, today)
		if err != nil {
			return "failed", err
		}
		if len(missing) == 0 {
			_ = s.svc.Store.SetReminderLastRun(ctx, kind, false)
			return "skipped", nil
		}
		target, err := s.targetChat(ctx, cfg)
		if err != nil || target == 0 {
			return "skipped", err
		}
		text := "💰 Час оновити залишки заощаджень.\nБез залишку за цей місяць: " + strings.Join(missing, ", ") +
			"\nОновити: " + s.svc.Cfg.PublicBaseURL + "/snapshots"
		if err := s.sender.SendMessage(ctx, target, text); err != nil {
			return "failed", err
		}
		_ = s.svc.Store.SetReminderLastRun(ctx, kind, true)
		return "sent", nil
	default:
		return "skipped", nil
	}
}

func (s *Scheduler) targetChat(ctx context.Context, cfg *domain.ReminderConfig) (int64, error) {
	if cfg.TargetChatID != nil {
		ok, err := s.svc.IsTelegramChatAllowed(ctx, *cfg.TargetChatID)
		if err != nil || !ok {
			return 0, fmt.Errorf("target chat is not allowlisted")
		}
		return *cfg.TargetChatID, nil
	}
	if cfg.TargetUserID != nil {
		u, err := s.svc.Store.GetUserByID(ctx, *cfg.TargetUserID)
		if err != nil {
			return 0, err
		}
		if u.TelegramUserID == nil {
			return 0, fmt.Errorf("target user has no Telegram ID")
		}
		return *u.TelegramUserID, nil
	}
	admin, err := s.svc.Store.AdminUser(ctx)
	if err != nil {
		return 0, nil
	}
	if admin.TelegramUserID == nil {
		return 0, nil
	}
	return *admin.TelegramUserID, nil
}

func (s *Scheduler) missingAccounts(ctx context.Context, today domain.Date) ([]string, error) {
	accounts, err := s.svc.Store.ListAccounts(ctx, false)
	if err != nil {
		return nil, err
	}
	monthStart := domain.NewDate(today.Year(), today.Month(), 1)
	var out []string
	for _, a := range accounts {
		if !a.IncludeInNetWorth {
			continue
		}
		snaps, err := s.svc.Store.ListSnapshots(ctx, a.ID, &monthStart, nil, false)
		if err != nil {
			return nil, err
		}
		if len(snaps) == 0 {
			out = append(out, a.Name+" ("+a.OwnerName+")")
		}
	}
	return out, nil
}

func pluralDays(n int) string {
	mod10 := n % 10
	mod100 := n % 100
	if mod10 == 1 && mod100 != 11 {
		return "день"
	}
	if mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
		return "дні"
	}
	return "днів"
}
