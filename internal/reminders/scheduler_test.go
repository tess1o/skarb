package reminders

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/service"
	"skarb/internal/store"
)

type fakeSender struct {
	mu      sync.Mutex
	chatIDs []int64
	texts   []string
}

func (f *fakeSender) SendMessage(_ context.Context, chatID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chatIDs = append(f.chatIDs, chatID)
	f.texts = append(f.texts, text)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.texts)
}

func TestReminderSendAndSkip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	loc, _ := time.LoadLocation("Europe/Kyiv")
	cfg := config.Config{
		DataDir: dir, PublicBaseURL: "http://localhost", AdminUsername: "admin",
		AdminPassword: "adminpassword123", MaxActiveUsers: 2, Timezone: "Europe/Kyiv",
		SessionSecret: []byte("test-secret-012345678901234567890"),
	}
	fxSvc := fx.New(st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), loc)
	svc, err := service.New(st, fxSvc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BootstrapAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.AdminUser(ctx)
	tgID := int64(123)
	if err := st.SetUserTelegramID(ctx, nil, admin.ID, &tgID); err != nil {
		t.Fatal(err)
	}
	actor := svc.ActorForUser(admin, domain.SourceWeb)
	sender := &fakeSender{}
	s := New(svc, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Transaction reminder: no activity, then activity.
	tc, err := st.GetReminderConfig(ctx, "transactions")
	if err != nil {
		t.Fatal(err)
	}
	tc.Enabled = true
	tc.WindowDays = 7
	if err := st.UpdateReminderConfig(ctx, nil, *tc); err != nil {
		t.Fatal(err)
	}
	status, err := s.sendReminder(ctx, "transactions")
	if err != nil || status != "sent" || sender.count() != 1 || sender.chatIDs[0] != tgID {
		t.Fatalf("transaction reminder: status=%s err=%v count=%d", status, err, sender.count())
	}
	cats, _ := svc.ListCategories(ctx, actor, false)
	var food domain.Category
	for _, c := range cats {
		if c.Name == "Їжа" {
			food = c
			break
		}
	}
	if food.ID == 0 {
		t.Fatal("seed category not found")
	}
	if _, err := svc.CreateTransaction(ctx, actor, service.TransactionItemInput{
		Amount: "10", Currency: "UAH", Date: svc.Today().String(), Category: food.UID, Description: "activity",
	}, domain.SourceWeb, "activity"); err != nil {
		t.Fatal(err)
	}
	status, err = s.sendReminder(ctx, "transactions")
	if err != nil || status != "skipped" || sender.count() != 1 {
		t.Fatalf("transaction reminder after activity: status=%s err=%v count=%d", status, err, sender.count())
	}

	// Snapshot reminder: missing balance, then present.
	sc, err := st.GetReminderConfig(ctx, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	sc.Enabled = true
	if err := st.UpdateReminderConfig(ctx, nil, *sc); err != nil {
		t.Fatal(err)
	}
	account, err := svc.CreateAccount(ctx, actor, service.CreateAccountInput{
		OwnerUserID: admin.ID, Name: "bank1", Currency: "UAH", Kind: domain.AccountBank, IncludeInNetWorth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err = s.sendReminder(ctx, "snapshot")
	if err != nil || status != "sent" || sender.count() != 2 {
		t.Fatalf("snapshot reminder: status=%s err=%v count=%d", status, err, sender.count())
	}
	if _, err := svc.UpsertSnapshot(ctx, actor, service.UpsertSnapshotInput{
		AccountUID: account.UID, Date: svc.Today().String(), Amount: "100",
	}); err != nil {
		t.Fatal(err)
	}
	status, err = s.sendReminder(ctx, "snapshot")
	if err != nil || status != "skipped" || sender.count() != 2 {
		t.Fatalf("snapshot reminder after balance: status=%s err=%v count=%d", status, err, sender.count())
	}
}
