package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/nlu"
	"skarb/internal/service"
	"skarb/internal/store"
)

type fakeTelegram struct {
	mu       sync.Mutex
	sent     []map[string]any
	edited   []map[string]any
	answers  []map[string]any
	sendOnce chan map[string]any
	updates  []map[string]any
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := path[strings.LastIndex(path, "/")+1:]
	w.Header().Set("Content-Type", "application/json")
	switch method {
	case "getMe":
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":999,"is_bot":true,"first_name":"Skarb","username":"skarb_test"}}`))
	case "getUpdates":
		f.mu.Lock()
		updates := f.updates
		f.updates = nil
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates})
	case "sendMessage":
		body := map[string]any{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			_ = r.ParseMultipartForm(10 << 20)
			body["text"] = r.FormValue("text")
			body["chat_id"] = r.FormValue("chat_id")
			if raw := r.FormValue("reply_markup"); raw != "" {
				var markup any
				_ = json.Unmarshal([]byte(raw), &markup)
				body["reply_markup"] = markup
			}
		} else {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		f.mu.Lock()
		f.sent = append(f.sent, body)
		f.mu.Unlock()
		select {
		case f.sendOnce <- body:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{
			"message_id": 77, "date": time.Now().Unix(),
			"chat": map[string]any{"id": 123, "type": "private"},
			"text": body["text"],
		}})
	case "editMessageText":
		body := map[string]any{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			_ = r.ParseMultipartForm(10 << 20)
			body["text"] = r.FormValue("text")
			body["chat_id"] = r.FormValue("chat_id")
			body["message_id"] = r.FormValue("message_id")
			if raw := r.FormValue("reply_markup"); raw != "" {
				var markup any
				_ = json.Unmarshal([]byte(raw), &markup)
				body["reply_markup"] = markup
			}
		} else {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		f.mu.Lock()
		f.edited = append(f.edited, body)
		f.mu.Unlock()
		text, _ := body["text"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{
			"message_id": 77, "date": time.Now().Unix(),
			"chat": map[string]any{"id": 123, "type": "private"},
			"text": text,
		}})
	case "answerCallbackQuery":
		body := map[string]any{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			_ = r.ParseMultipartForm(10 << 20)
			body["text"] = r.FormValue("text")
			body["callback_query_id"] = r.FormValue("callback_query_id")
		} else {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		f.mu.Lock()
		f.answers = append(f.answers, body)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	default:
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}
}

func TestShouldParseLetsConfiguredLLMSeeAddressedAmountLists(t *testing.T) {
	env := newToolTestEnv(t, nil)
	const text = "вчора:\nресторан 2100\nінше 300\nкава 75"
	if nlu.LooksFinancial(text) {
		t.Fatal("test message unexpectedly passes the deterministic prefilter")
	}
	privateMsg := &models.Message{
		Chat: models.Chat{ID: 123, Type: models.ChatTypePrivate},
		Text: text,
	}
	if env.bot.shouldParse(env.ctx, privateMsg, text) {
		t.Fatal("unconfigured LLM should not bypass the prefilter")
	}

	llmCfg := service.DefaultLLMConfig()
	llmCfg.BaseURL = "http://127.0.0.1:1"
	llmCfg.Model = "fake-model"
	raw, err := json.Marshal(llmCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.st.SetSetting(env.ctx, nil, "llm.config", string(raw), nil); err != nil {
		t.Fatal(err)
	}
	if !env.bot.shouldParse(env.ctx, privateMsg, text) {
		t.Fatal("configured LLM should see an addressed message containing numbers")
	}

	groupMsg := &models.Message{
		Chat: models.Chat{ID: -100, Type: models.ChatTypeSupergroup},
		Text: "вчора 2100",
	}
	if env.bot.shouldParse(env.ctx, groupMsg, groupMsg.Text) {
		t.Fatal("unaddressed group chatter should not reach the LLM")
	}
	if !env.bot.shouldParse(env.ctx, privateMsg, "сьогодні 512 грн кава") {
		t.Fatal("financial messages should always be parsed")
	}
}

func TestTelegramMessageCreatesConfirmation(t *testing.T) {
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
	if err := st.AddTelegramChat(ctx, 123, "DM", "private", &admin.ID); err != nil {
		t.Fatal(err)
	}
	_ = svc // used by the bot below

	fake := &fakeTelegram{sendOnce: make(chan map[string]any, 1)}
	fake.updates = []map[string]any{{
		"update_id": 1,
		"message": map[string]any{
			"message_id": 10,
			"date":       time.Now().Unix(),
			"chat":       map[string]any{"id": 123, "type": "private"},
			"from":       map[string]any{"id": 123, "is_bot": false, "first_name": "Admin"},
			"text":       "сьогодні 512 грн за їжу, 300 грн за каву",
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
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Start(runCtx)
	}()

	var sent map[string]any
	select {
	case sent = <-fake.sendOnce:
		text, _ := sent["text"].(string)
		if !strings.Contains(text, "512") || !strings.Contains(text, "300") {
			t.Fatalf("confirmation text missing amounts: %s", text)
		}
		markup, _ := sent["reply_markup"].(map[string]any)
		rows, _ := markup["inline_keyboard"].([]any)
		if len(rows) != 2 {
			t.Fatalf("expected 2 button rows, got %#v", markup)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for confirmation")
	}

	markup := sent["reply_markup"].(map[string]any)
	buttonRows := markup["inline_keyboard"].([]any)
	undoToken := buttonRows[0].([]any)[0].(map[string]any)["callback_data"].(string)
	action, err := st.GetAction(ctx, undoToken)
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.updates = append(fake.updates, map[string]any{
		"update_id": 2,
		"callback_query": map[string]any{
			"id":   "cb-1",
			"from": map[string]any{"id": 123, "is_bot": false, "first_name": "Admin"},
			"message": map[string]any{
				"message_id": 77, "date": time.Now().Unix(),
				"chat": map[string]any{"id": 123, "type": "private"},
				"text": sent["text"],
			},
			"data": undoToken,
		},
	})
	fake.mu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
	for {
		undone, err := st.GetTransactionByID(ctx, action.TransactionID)
		if err == nil && undone.Deleted() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Telegram undo callback")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bot did not stop after cancel")
	}

	actor := svc.ActorForUser(admin, domain.SourceWeb)
	rows, total, err := svc.ListTransactions(ctx, actor, store.TransactionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("expected 1 active transaction after undo, got %d: %+v", total, rows)
	}
}
