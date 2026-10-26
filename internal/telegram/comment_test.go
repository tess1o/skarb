package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestTelegramCommentReplacesTransaction(t *testing.T) {
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
	markup := confirmation["reply_markup"].(map[string]any)
	buttonRows := markup["inline_keyboard"].([]any)
	commentToken := buttonRows[0].([]any)[1].(map[string]any)["callback_data"].(string)
	action, err := st.GetAction(ctx, commentToken)
	if err != nil {
		t.Fatal(err)
	}
	original, err := st.GetTransactionByID(ctx, action.TransactionID)
	if err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	fake.updates = append(fake.updates, map[string]any{
		"update_id": 2,
		"callback_query": map[string]any{
			"id":   "cb-comment",
			"from": map[string]any{"id": 123, "is_bot": false, "first_name": "Admin"},
			"message": map[string]any{
				"message_id": 77, "date": time.Now().Unix(),
				"chat": map[string]any{"id": 123, "type": "private"},
				"text": confirmation["text"],
			},
			"data": commentToken,
		},
	})
	fake.mu.Unlock()

	// The second outbound message is the ForceReply prompt.
	deadline := time.Now().Add(3 * time.Second)
	for {
		fake.mu.Lock()
		n := len(fake.sent)
		fake.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for comment prompt")
		}
		time.Sleep(50 * time.Millisecond)
	}
	fake.mu.Lock()
	prompt := fake.sent[1]
	fake.mu.Unlock()
	if text, _ := prompt["text"].(string); text == "" {
		t.Fatalf("empty comment prompt: %#v", prompt)
	}

	fake.mu.Lock()
	fake.updates = append(fake.updates, map[string]any{
		"update_id": 3,
		"message": map[string]any{
			"message_id": 11, "date": time.Now().Unix(),
			"chat": map[string]any{"id": 123, "type": "private"},
			"from": map[string]any{"id": 123, "is_bot": false, "first_name": "Admin"},
			"text": "з колегою",
			"reply_to_message": map[string]any{
				"message_id": 77, "date": time.Now().Unix(),
				"chat": map[string]any{"id": 123, "type": "private"},
				"from": map[string]any{"id": 999, "is_bot": true, "first_name": "Skarb"},
				"text": prompt["text"],
			},
		},
	})
	fake.mu.Unlock()

	deadline = time.Now().Add(3 * time.Second)
	for {
		versions, err := svc.TransactionVersions(ctx, svc.ActorForUser(admin, domain.SourceWeb), original.UID)
		if err == nil {
			for _, v := range versions {
				if !v.Deleted() && v.Note == "з колегою" {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for comment replacement")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
