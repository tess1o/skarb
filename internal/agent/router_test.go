package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/mcp"
	"skarb/internal/service"
	"skarb/internal/store"
)

func routerEnv(t *testing.T) (*Router, *service.Service, domain.Actor, []mcp.ToolInfo, func()) {
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
	actor := svc.ActorForUser(admin, domain.SourceTelegram)
	actor.TelegramUserID = 123
	actor.ChatID = 456
	session, err := mcp.New(svc, cfg, logger).NewToolSession(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	tools := session.ListTools()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	cleanup := func() { _ = st.Close() }
	return NewRouter(svc, logger), svc, actor, tools, cleanup
}

func fakeLLM(t *testing.T, status int, responses ...string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if status != http.StatusOK {
			http.Error(w, "upstream failure", status)
			return
		}
		content := ""
		if len(responses) > 0 {
			index := calls - 1
			if index >= len(responses) {
				index = len(responses) - 1
			}
			content = responses[index]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": content},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(ts.Close)
	return ts, &calls
}

func configureLLM(t *testing.T, svc *service.Service, baseURL string) {
	t.Helper()
	cfg := service.DefaultLLMConfig()
	cfg.BaseURL = baseURL
	cfg.Model = "fake-model"
	cfg.SchemaMode = "json_schema"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetSetting(context.Background(), nil, "llm.config", string(raw), nil); err != nil {
		t.Fatal(err)
	}
	svc.Cfg.LLMAPIKey = "test-key"
}

func TestRouterValidDecision(t *testing.T) {
	router, svc, actor, tools, cleanup := routerEnv(t)
	defer cleanup()
	ts, _ := fakeLLM(t, http.StatusOK,
		`{"tool":"list_transactions","arguments_json":"{\"from\":\"2026-10-01\",\"to\":\"2026-10-31\"}","confidence":0.9}`)
	configureLLM(t, svc, ts.URL)

	decision, err := router.Decide(context.Background(), actor, "покажи всі транзакції за жовтень", tools)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Tool != "list_transactions" {
		t.Fatalf("tool = %q", decision.Tool)
	}
	var args map[string]any
	if err := json.Unmarshal(decision.Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["from"] != "2026-10-01" || args["to"] != "2026-10-31" {
		t.Fatalf("args = %v", args)
	}
	if args["limit"] != float64(20) {
		t.Fatalf("limit = %v, want 20", args["limit"])
	}
}

func TestRouterRepairsInvalidJSON(t *testing.T) {
	router, svc, actor, tools, cleanup := routerEnv(t)
	defer cleanup()
	ts, calls := fakeLLM(t, http.StatusOK,
		"not json",
		`{"tool":"noop","arguments_json":"","confidence":0}`)
	configureLLM(t, svc, ts.URL)

	decision, err := router.Decide(context.Background(), actor, "привіт", tools)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Tool != "noop" {
		t.Fatalf("tool = %q", decision.Tool)
	}
	if *calls != 2 {
		t.Fatalf("calls = %d, want 2", *calls)
	}
}

func TestRouterRejectsAdminToolForMember(t *testing.T) {
	router, svc, _, _, cleanup := routerEnv(t)
	defer cleanup()
	ctx := context.Background()
	admin, err := svc.Store.AdminUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	member, _, err := svc.CreateUser(ctx, svc.ActorForUser(admin, domain.SourceTelegram), service.CreateUserInput{
		Username: "wife", DisplayName: "Wife", Password: "wifepassword123",
	})
	if err != nil {
		t.Fatal(err)
	}
	memberActor := svc.ActorForUser(member, domain.SourceTelegram)
	session, err := mcp.New(svc, svc.Cfg, slog.Default()).NewToolSession(ctx, memberActor)
	if err != nil {
		t.Fatal(err)
	}
	memberTools := session.ListTools()
	_ = session.Close()
	ts, calls := fakeLLM(t, http.StatusOK,
		`{"tool":"create_category","arguments_json":"{\"name\":\"X\",\"kind\":\"expense\"}","confidence":0.9}`,
		`{"tool":"noop","arguments_json":"","confidence":0}`)
	configureLLM(t, svc, ts.URL)

	decision, err := router.Decide(ctx, memberActor, "створи категорію X", memberTools)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Tool != "noop" {
		t.Fatalf("tool = %q, want noop after repair", decision.Tool)
	}
	if *calls != 2 {
		t.Fatalf("calls = %d, want 2", *calls)
	}
}

func TestRouterRejectsSchemaInvalidArguments(t *testing.T) {
	router, svc, actor, tools, cleanup := routerEnv(t)
	defer cleanup()
	ts, calls := fakeLLM(t, http.StatusOK,
		`{"tool":"list_transactions","arguments_json":"{\"from\":123}","confidence":0.9}`,
		`{"tool":"list_transactions","arguments_json":"{\"limit\":10}","confidence":0.9}`)
	configureLLM(t, svc, ts.URL)

	decision, err := router.Decide(context.Background(), actor, "покажи транзакції", tools)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Tool != "list_transactions" {
		t.Fatalf("tool = %q", decision.Tool)
	}
	if *calls != 2 {
		t.Fatalf("calls = %d, want 2", *calls)
	}
}

func TestRouterUnconfiguredDoesNotCallLLM(t *testing.T) {
	router, _, actor, tools, cleanup := routerEnv(t)
	defer cleanup()
	_, err := router.Decide(context.Background(), actor, "покажи транзакції", tools)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

func TestRouterUpstreamErrorDoesNotRepair(t *testing.T) {
	router, svc, actor, tools, cleanup := routerEnv(t)
	defer cleanup()
	ts, calls := fakeLLM(t, http.StatusInternalServerError)
	configureLLM(t, svc, ts.URL)
	_, err := router.Decide(context.Background(), actor, "покажи транзакції", tools)
	if err == nil {
		t.Fatal("expected error")
	}
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}
}

func TestNormalizeArgumentsExpandsMonths(t *testing.T) {
	out, err := normalizeArguments("list_transactions", json.RawMessage(`{
		"from":"2026-10",
		"to":"2026-10-31T12:00:00Z",
		"limit":50,
		"idempotency_key":"x"
	}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal(out, &args); err != nil {
		t.Fatal(err)
	}
	if args["from"] != "2026-10-01" {
		t.Fatalf("from = %v", args["from"])
	}
	if args["to"] != "2026-10-31" {
		t.Fatalf("to = %v", args["to"])
	}
	if args["limit"] != float64(30) {
		t.Fatalf("limit = %v", args["limit"])
	}
	if _, ok := args["idempotency_key"]; ok {
		t.Fatal("idempotency_key was not stripped")
	}
}
