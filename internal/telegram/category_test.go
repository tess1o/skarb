package telegram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/nlu"
	"skarb/internal/service"
	"skarb/internal/store"
)

func TestTelegramCategoryChangeAndUndo(t *testing.T) {
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
		TelegramBotToken: "123:test", TelegramPollTimeout: time.Second,
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

	fake := &fakeTelegram{sendOnce: make(chan map[string]any, 1)}
	fake.updates = []map[string]any{{
		"update_id": 1,
		"message": map[string]any{
			"message_id": 10, "date": time.Now().Unix(),
			"chat": map[string]any{"id": 123, "type": "private"},
			"from": map[string]any{"id": 123, "is_bot": false, "first_name": "Admin"},
			"text": "сьогодні 512 грн за їжу",
		},
	}}
	ts := httptest.NewServer(fake)
	defer ts.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := &Bot{svc: svc, cfg: cfg, log: logger, parser: nlu.NewParser(svc, logger), loc: loc}
	api, err := tgbot.New(cfg.TelegramBotToken,
		tgbot.WithServerURL(ts.URL),
		tgbot.WithDefaultHandler(b.handleUpdate),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "callback_query"}),
		tgbot.WithHTTPClient(time.Second, &http.Client{Timeout: 5 * time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	b.api = api
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Start(runCtx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}()

	var confirmation map[string]any
	select {
	case confirmation = <-fake.sendOnce:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for confirmation")
	}
	row := confirmation["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)
	categoryToken := row[2].(map[string]any)["callback_data"].(string)
	action, err := st.GetAction(ctx, categoryToken)
	if err != nil || action.Action != "category" {
		t.Fatalf("category action: %+v, %v", action, err)
	}
	original, err := st.GetTransactionByID(ctx, action.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	var target *domain.Category
	cats, err := st.ListCategories(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cats {
		if cats[i].Kind == original.Kind && cats[i].ID != original.CategoryID {
			target = &cats[i]
			break
		}
	}
	if target == nil {
		t.Fatal("no alternative category in seed data")
	}

	callbackID := 0
	press := func(data string) {
		callbackID++
		fake.mu.Lock()
		fake.updates = append(fake.updates, map[string]any{
			"update_id": 10 + callbackID,
			"callback_query": map[string]any{
				"id":   fmt.Sprintf("cb-%d", callbackID),
				"from": map[string]any{"id": 123, "is_bot": false, "first_name": "Admin"},
				"message": map[string]any{
					"message_id": 77, "date": time.Now().Unix(),
					"chat": map[string]any{"id": 123, "type": "private"},
					"text": confirmation["text"],
				},
				"data": data,
			},
		})
		fake.mu.Unlock()
	}
	waitEdits := func(n int) map[string]any {
		deadline := time.Now().Add(3 * time.Second)
		for {
			fake.mu.Lock()
			if len(fake.edited) >= n {
				last := fake.edited[n-1]
				fake.mu.Unlock()
				return last
			}
			fake.mu.Unlock()
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for edit %d", n)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Opening the picker lists categories and leaves the token usable.
	press(categoryToken)
	picker := waitEdits(1)
	if text, _ := picker["text"].(string); !strings.Contains(text, "Нова категорія") {
		t.Fatalf("picker text: %q", text)
	}
	choice := fmt.Sprintf("%s|%d", categoryToken, target.ID)
	found := false
	for _, r := range picker["reply_markup"].(map[string]any)["inline_keyboard"].([]any) {
		for _, btn := range r.([]any) {
			if btn.(map[string]any)["callback_data"] == choice {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("picker has no button for %q: %#v", target.Name, picker["reply_markup"])
	}
	if a, _ := st.GetAction(ctx, categoryToken); a == nil || a.UsedAt != nil {
		t.Fatal("opening the picker consumed the token")
	}

	press(choice)
	updatedMsg := waitEdits(2)
	if text, _ := updatedMsg["text"].(string); !strings.Contains(text, target.Name) {
		t.Fatalf("confirmation not updated with new category: %q", text)
	}
	actor := svc.ActorForUser(admin, domain.SourceWeb)
	active, total, err := svc.ListTransactions(ctx, actor, store.TransactionFilter{Limit: 10})
	if err != nil || total != 1 || active[0].CategoryID != target.ID {
		t.Fatalf("expected one transaction in %q, got %d: %+v (%v)", target.Name, total, active, err)
	}

	// Undo on the refreshed confirmation restores the original category.
	undoToken := updatedMsg["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)["callback_data"].(string)
	press(undoToken)
	deadline := time.Now().Add(3 * time.Second)
	for {
		restored, err := st.GetTransactionByID(ctx, original.ID)
		if err == nil && !restored.Deleted() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("undo did not restore the original category")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
