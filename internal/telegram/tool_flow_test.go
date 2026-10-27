package telegram

import (
	"context"
	"encoding/json"
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
	"github.com/go-telegram/bot/models"
	"skarb/internal/agent"
	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/mcp"
	"skarb/internal/nlu"
	"skarb/internal/service"
	"skarb/internal/store"
)

type toolTestEnv struct {
	ctx     context.Context
	st      *store.Store
	svc     *service.Service
	cfg     config.Config
	admin   *domain.User
	adminTG int64
	fake    *fakeTelegram
	bot     *Bot
}

func newToolTestEnv(t *testing.T, llmHandler http.Handler) *toolTestEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		DataDir: dir, PublicBaseURL: "http://localhost", AdminUsername: "admin",
		AdminPassword: "adminpassword123", MaxActiveUsers: 2, Timezone: "Europe/Kyiv",
		TelegramBotToken: "123:test", TelegramPollTimeout: time.Second,
		TelegramToolMode: true,
		SessionSecret:    []byte("test-secret-012345678901234567890"),
	}
	fxSvc := fx.New(st, nil, logger, loc)
	svc, err := service.New(st, fxSvc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BootstrapAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	svc.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, loc) }
	admin, err := st.AdminUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminTG := int64(123)
	if err := st.SetUserTelegramID(ctx, nil, admin.ID, &adminTG); err != nil {
		t.Fatal(err)
	}
	svc.Cfg.LLMAPIKey = "test-key"

	if llmHandler != nil {
		llmSrv := httptest.NewServer(llmHandler)
		t.Cleanup(llmSrv.Close)
		llmCfg := service.DefaultLLMConfig()
		llmCfg.BaseURL = llmSrv.URL
		llmCfg.Model = "fake-model"
		llmCfg.SchemaMode = "json_schema"
		raw, err := json.Marshal(llmCfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetSetting(ctx, nil, "llm.config", string(raw), nil); err != nil {
			t.Fatal(err)
		}
	}

	fake := &fakeTelegram{sendOnce: make(chan map[string]any, 1)}
	tgSrv := httptest.NewServer(fake)
	t.Cleanup(tgSrv.Close)
	bot := &Bot{
		svc: svc, cfg: cfg, log: logger,
		parser:    nlu.NewParser(svc, logger),
		router:    agent.NewRouter(svc, logger),
		mcpServer: mcp.New(svc, cfg, logger),
		loc:       loc,
		username:  "skarb_test",
		toolMode:  true,
	}
	api, err := tgbot.New(cfg.TelegramBotToken,
		tgbot.WithServerURL(tgSrv.URL),
		tgbot.WithDefaultHandler(bot.handleUpdate),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "callback_query"}),
		tgbot.WithHTTPClient(time.Second, &http.Client{Timeout: 5 * time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	bot.api = api
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		bot.Start(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
		_ = st.Close()
	})
	return &toolTestEnv{ctx: ctx, st: st, svc: svc, cfg: cfg, admin: admin, adminTG: adminTG, fake: fake, bot: bot}
}

func (e *toolTestEnv) addUpdate(update map[string]any) {
	e.fake.mu.Lock()
	e.fake.updates = append(e.fake.updates, update)
	e.fake.mu.Unlock()
}

func (e *toolTestEnv) waitSent(t *testing.T, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.fake.mu.Lock()
		sent := append([]map[string]any(nil), e.fake.sent...)
		e.fake.mu.Unlock()
		if len(sent) >= n {
			return sent
		}
		if time.Now().After(deadline) {
			e.fake.mu.Lock()
			answers := append([]map[string]any(nil), e.fake.answers...)
			updates := append([]map[string]any(nil), e.fake.updates...)
			e.fake.mu.Unlock()
			t.Fatalf("timed out waiting for %d messages, got %d; answers=%v updates=%v", n, len(sent), answers, updates)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *toolTestEnv) waitEdited(t *testing.T, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.fake.mu.Lock()
		edited := append([]map[string]any(nil), e.fake.edited...)
		e.fake.mu.Unlock()
		if len(edited) >= n {
			return edited
		}
		if time.Now().After(deadline) {
			e.fake.mu.Lock()
			answers := append([]map[string]any(nil), e.fake.answers...)
			e.fake.mu.Unlock()
			t.Fatalf("timed out waiting for %d edits, got %d; answers=%v", n, len(edited), answers)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *toolTestEnv) seedTransactions(t *testing.T, count int) {
	t.Helper()
	actor := e.svc.ActorForUser(e.admin, domain.SourceWeb)
	var items []service.TransactionItemInput
	for i := 0; i < count; i++ {
		items = append(items, service.TransactionItemInput{
			Amount: "100", Currency: "UAH",
			Date:        fmt.Sprintf("2026-10-%02d", 1+i%26),
			Category:    "Їжа",
			Description: fmt.Sprintf("tx %d", i),
		})
	}
	for len(items) > 0 {
		n := 10
		if len(items) < n {
			n = len(items)
		}
		if _, err := e.svc.CreateTransactions(e.ctx, actor, service.CreateTransactionsInput{
			Items: items[:n], Source: domain.SourceWeb,
		}); err != nil {
			t.Fatal(err)
		}
		items = items[n:]
	}
}

func fixedDecision(tool string, args map[string]any) http.HandlerFunc {
	argsJSON, _ := json.Marshal(args)
	content, _ := json.Marshal(map[string]any{
		"tool": tool, "arguments_json": string(argsJSON), "confidence": 0.9,
	})
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": string(content)},
				"finish_reason": "stop",
			}},
		})
	}
}

func messageUpdate(updateID int64, chatID int64, chatType string, fromID int64, text string) map[string]any {
	return map[string]any{
		"update_id": updateID,
		"message": map[string]any{
			"message_id": 10, "date": time.Now().Unix(),
			"chat": map[string]any{"id": chatID, "type": chatType},
			"from": map[string]any{"id": fromID, "is_bot": false, "first_name": "Admin"},
			"text": text,
		},
	}
}

func callbackUpdate(updateID int64, chatID int64, chatType string, fromID int64, messageID int, data, text string) map[string]any {
	return map[string]any{
		"update_id": updateID,
		"callback_query": map[string]any{
			"id":   fmt.Sprintf("cb-%d", updateID),
			"from": map[string]any{"id": fromID, "is_bot": false, "first_name": "Admin"},
			"message": map[string]any{
				"message_id": messageID, "date": time.Now().Unix(),
				"chat": map[string]any{"id": chatID, "type": chatType},
				"text": text,
			},
			"data": data,
		},
	}
}

func findButton(t *testing.T, sent map[string]any, label string) string {
	t.Helper()
	markup, ok := sent["reply_markup"].(map[string]any)
	if !ok {
		return ""
	}
	rows, _ := markup["inline_keyboard"].([]any)
	for _, row := range rows {
		buttons, _ := row.([]any)
		for _, button := range buttons {
			b, _ := button.(map[string]any)
			if strings.Contains(fmt.Sprint(b["text"]), label) {
				data, _ := b["callback_data"].(string)
				return data
			}
		}
	}
	return ""
}

func TestToolModeListsOctoberTransactionsWithPagination(t *testing.T) {
	env := newToolTestEnv(t, fixedDecision("list_transactions", map[string]any{
		"from": "2026-10-01", "to": "2026-10-31",
	}))
	env.seedTransactions(t, 45)
	env.addUpdate(messageUpdate(1, 123, "private", 123, "покажи всі транзакції за жовтень"))
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "Показано 1–20 із 45") {
		t.Fatalf("unexpected first page: %s", text)
	}
	more := findButton(t, sent, "Ще")
	if !strings.HasPrefix(more, "tp:") {
		t.Fatalf("missing page button, markup: %#v", sent["reply_markup"])
	}
	env.addUpdate(callbackUpdate(2, 123, "private", 123, 77, more, text))
	edited := env.waitEdited(t, 1)[0]
	editedText, _ := edited["text"].(string)
	if !strings.Contains(editedText, "Показано 21–40 із 45") {
		t.Fatalf("unexpected second page: %s", editedText)
	}
}

func TestToolModeAddPreservesUndoAndOriginalText(t *testing.T) {
	env := newToolTestEnv(t, fixedDecision("add_transactions", map[string]any{
		"transactions": []map[string]any{{
			"amount": "512", "currency": "UAH", "category": "Їжа", "description": "обід",
		}},
	}))
	env.addUpdate(messageUpdate(1, 123, "private", 123, "додай 512 грн за їжу"))
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "512") || !strings.Contains(text, "Записано") {
		t.Fatalf("unexpected add confirmation: %s", text)
	}
	undo := findButton(t, sent, "Скасувати")
	if !strings.HasPrefix(undo, "u:") {
		t.Fatalf("missing undo button: %#v", sent["reply_markup"])
	}
	action, err := env.st.GetAction(env.ctx, undo)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := env.st.GetTransactionByID(env.ctx, action.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Source != domain.SourceTelegram {
		t.Fatalf("source = %q, want telegram", tx.Source)
	}
	if tx.OriginalText != "додай 512 грн за їжу" {
		t.Fatalf("original_text = %q", tx.OriginalText)
	}
}

func TestGroupQuickCaptureDoesNotCallLLM(t *testing.T) {
	base := fixedDecision("stats_summary", map[string]any{
		"from": "2026-10-01", "to": "2026-10-31",
	})
	calls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		base(w, r)
	})
	env := newToolTestEnv(t, handler)
	if err := env.st.AddTelegramChat(env.ctx, 456, "Family", "supergroup", &env.admin.ID); err != nil {
		t.Fatal(err)
	}

	env.addUpdate(messageUpdate(1, 456, "supergroup", 123, "512 грн за їжу"))
	sent := env.waitSent(t, 1)[0]
	if text, _ := sent["text"].(string); !strings.Contains(text, "512") {
		t.Fatalf("quick capture failed: %s", text)
	}
	if calls != 0 {
		t.Fatalf("quick capture called LLM %d times", calls)
	}

	env.addUpdate(messageUpdate(2, 456, "supergroup", 123, "@skarb_test скільки витратили за жовтень"))
	sent = env.waitSent(t, 2)[1]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "Підсумок") || !strings.Contains(text, "512") {
		t.Fatalf("unexpected summary: %s", text)
	}
	if calls != 1 {
		t.Fatalf("mention called LLM %d times, want 1", calls)
	}
}

func TestGroupAllMessagesUsesToolMode(t *testing.T) {
	calls := 0
	base := fixedDecision("stats_summary", map[string]any{
		"from": "2026-10-01", "to": "2026-10-31",
	})
	env := newToolTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		base(w, r)
	}))
	if err := env.st.AddTelegramChat(env.ctx, 456, "Family", "supergroup", &env.admin.ID); err != nil {
		t.Fatal(err)
	}
	cfg, err := env.svc.GetLLMConfig(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ListenAllGroupMessages = true
	if err := env.svc.SetLLMConfig(env.ctx, env.svc.ActorForUser(env.admin, domain.SourceWeb), cfg); err != nil {
		t.Fatal(err)
	}
	env.seedTransactions(t, 1)

	env.addUpdate(messageUpdate(1, 456, "supergroup", 123, "скільки витратили за жовтень"))
	sent := env.waitSent(t, 1)[0]
	if text, _ := sent["text"].(string); !strings.Contains(text, "Підсумок") {
		t.Fatalf("unexpected result: %s", text)
	}
	if calls != 1 {
		t.Fatalf("unaddressed group message called LLM %d times, want 1", calls)
	}
}

func TestUnaddressedGroupNoopStaysSilent(t *testing.T) {
	env := newToolTestEnv(t, fixedDecision("noop", map[string]any{}))
	msg := &models.Message{
		Chat: models.Chat{ID: 456, Type: models.ChatTypeSupergroup},
		From: &models.User{ID: 123}, Text: "звичайна розмова",
	}
	handled, err := env.bot.handleToolMessage(env.ctx, msg, env.admin,
		env.svc.ActorForUser(env.admin, domain.SourceTelegram), msg.Text)
	if err != nil || !handled {
		t.Fatalf("handled = %v, err = %v", handled, err)
	}
	env.fake.mu.Lock()
	defer env.fake.mu.Unlock()
	if len(env.fake.sent) != 0 {
		t.Fatalf("noop sent %d messages, want none", len(env.fake.sent))
	}
}

func TestDestructiveToolRequiresConfirmation(t *testing.T) {
	var uid string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		args, _ := json.Marshal(map[string]any{"uid": uid})
		content, _ := json.Marshal(map[string]any{
			"tool": "delete_transaction", "arguments_json": string(args), "confidence": 0.9,
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": string(content)},
				"finish_reason": "stop",
			}},
		})
	})
	env := newToolTestEnv(t, handler)
	env.seedTransactions(t, 1)
	actor := env.svc.ActorForUser(env.admin, domain.SourceWeb)
	rows, _, err := env.svc.ListTransactions(env.ctx, actor, store.TransactionFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	uid = rows[0].UID

	env.addUpdate(messageUpdate(1, 123, "private", 123, "видали цю транзакцію"))
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "Підтвердження") || !strings.Contains(text, "Видалити транзакцію") {
		t.Fatalf("unexpected confirmation card: %s", text)
	}
	confirm := findButton(t, sent, "Підтвердити")
	if !strings.HasPrefix(confirm, "tc:") {
		t.Fatalf("missing confirm button: %#v", sent["reply_markup"])
	}
	tx, err := env.st.GetTransactionByUID(env.ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Deleted() {
		t.Fatal("transaction was deleted before confirmation")
	}

	env.addUpdate(callbackUpdate(2, 123, "private", 123, 77, confirm, text))
	edited := env.waitEdited(t, 1)[0]
	editedText, _ := edited["text"].(string)
	if !strings.Contains(editedText, "Видалено") {
		t.Fatalf("unexpected confirm result: %s", editedText)
	}
	tx, err = env.st.GetTransactionByUID(env.ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if !tx.Deleted() {
		t.Fatal("transaction was not deleted after confirmation")
	}
}

func TestToolModeFallsBackToRegexWithoutLLM(t *testing.T) {
	env := newToolTestEnv(t, nil)
	env.addUpdate(messageUpdate(1, 123, "private", 123, "сьогодні 512 грн за їжу"))
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "512") || !strings.Contains(text, "Записано") {
		t.Fatalf("regex fallback failed: %s", text)
	}
}

func TestCreateUserPasswordStaysOutOfGroup(t *testing.T) {
	env := newToolTestEnv(t, fixedDecision("create_user", map[string]any{
		"username": "wife", "display_name": "Wife",
	}))
	if err := env.st.AddTelegramChat(env.ctx, 456, "Family", "supergroup", &env.admin.ID); err != nil {
		t.Fatal(err)
	}
	env.addUpdate(messageUpdate(1, 456, "supergroup", 123, "@skarb_test створи користувача wife"))
	sent := env.waitSent(t, 2)
	var groupText, dmText string
	for _, m := range sent {
		text, _ := m["text"].(string)
		if fmt.Sprint(m["chat_id"]) == "456" {
			groupText = text
		} else {
			dmText = text
		}
	}
	if strings.Contains(groupText, "Тимчасовий пароль") || strings.Contains(groupText, "<code>") {
		t.Fatalf("password leaked into group: %s", groupText)
	}
	if !strings.Contains(groupText, "Пароль надіслано в DM") {
		t.Fatalf("group message did not mention DM: %s", groupText)
	}
	if !strings.Contains(dmText, "Тимчасовий пароль") {
		t.Fatalf("DM did not contain password: %s", dmText)
	}
}

func TestLocalToolPagination(t *testing.T) {
	env := newToolTestEnv(t, fixedDecision("list_categories", map[string]any{}))
	actor := env.svc.ActorForUser(env.admin, domain.SourceWeb)
	for i := 0; i < 25; i++ {
		if _, err := env.svc.CreateCategory(env.ctx, actor, store.CreateCategoryInput{
			Name: fmt.Sprintf("Test Cat %d", i), Kind: domain.KindExpense,
		}); err != nil {
			t.Fatal(err)
		}
	}
	env.addUpdate(messageUpdate(1, 123, "private", 123, "покажи категорії"))
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "Показано 1–20 із") {
		t.Fatalf("unexpected first category page: %s", text)
	}
	more := findButton(t, sent, "Ще")
	if !strings.HasPrefix(more, "tp:") {
		t.Fatalf("missing category page button: %#v", sent["reply_markup"])
	}
	env.addUpdate(callbackUpdate(2, 123, "private", 123, 77, more, text))
	edited := env.waitEdited(t, 1)[0]
	editedText, _ := edited["text"].(string)
	if !strings.Contains(editedText, "Показано 21–") {
		t.Fatalf("unexpected second category page: %s", editedText)
	}
}

func TestDestructiveToolCancel(t *testing.T) {
	var uid string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		args, _ := json.Marshal(map[string]any{"uid": uid})
		content, _ := json.Marshal(map[string]any{
			"tool": "delete_transaction", "arguments_json": string(args), "confidence": 0.9,
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": string(content)},
				"finish_reason": "stop",
			}},
		})
	})
	env := newToolTestEnv(t, handler)
	env.seedTransactions(t, 1)
	actor := env.svc.ActorForUser(env.admin, domain.SourceWeb)
	rows, _, err := env.svc.ListTransactions(env.ctx, actor, store.TransactionFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	uid = rows[0].UID
	env.addUpdate(messageUpdate(1, 123, "private", 123, "видали цю транзакцію"))
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	cancel := findButton(t, sent, "Скасувати")
	if !strings.HasPrefix(cancel, "tk:") {
		t.Fatalf("missing cancel button: %#v", sent["reply_markup"])
	}
	env.addUpdate(callbackUpdate(2, 123, "private", 123, 77, cancel, text))
	edited := env.waitEdited(t, 1)[0]
	editedText, _ := edited["text"].(string)
	if !strings.Contains(editedText, "Скасовано") {
		t.Fatalf("unexpected cancel result: %s", editedText)
	}
	tx, err := env.st.GetTransactionByUID(env.ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Deleted() {
		t.Fatal("transaction was deleted after cancel")
	}
}

func TestGroupReplyToBotUsesToolMode(t *testing.T) {
	base := fixedDecision("stats_summary", map[string]any{
		"from": "2026-10-01", "to": "2026-10-31",
	})
	calls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		base(w, r)
	})
	env := newToolTestEnv(t, handler)
	if err := env.st.AddTelegramChat(env.ctx, 456, "Family", "supergroup", &env.admin.ID); err != nil {
		t.Fatal(err)
	}
	update := messageUpdate(1, 456, "supergroup", 123, "скільки витратили за жовтень")
	msg := update["message"].(map[string]any)
	msg["reply_to_message"] = map[string]any{
		"message_id": 77, "date": time.Now().Unix(),
		"chat": map[string]any{"id": 456, "type": "supergroup"},
		"from": map[string]any{"id": 123, "is_bot": true, "first_name": "Skarb", "username": "skarb_test"},
		"text": "Skarb",
	}
	env.addUpdate(update)
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "Підсумок") {
		t.Fatalf("unexpected reply-to-bot result: %s", text)
	}
	if calls != 1 {
		t.Fatalf("reply-to-bot called LLM %d times, want 1", calls)
	}
}

func TestToolModeLLMFailureFallsBackToRegex(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failure", http.StatusInternalServerError)
	})
	env := newToolTestEnv(t, handler)
	env.addUpdate(messageUpdate(1, 123, "private", 123, "сьогодні 512 грн за їжу"))
	sent := env.waitSent(t, 1)[0]
	text, _ := sent["text"].(string)
	if !strings.Contains(text, "512") || !strings.Contains(text, "Записано") {
		t.Fatalf("LLM failure fallback failed: %s", text)
	}
}
