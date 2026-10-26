package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/service"
	"skarb/internal/store"
	"skarb/internal/web"
)

type testBearer struct{ token string }

func (t testBearer) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestMCPHTTPAddAndStats(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DataDir: dir, PublicBaseURL: "http://localhost:8080", AdminUsername: "admin",
		AdminPassword: "adminpassword123", MaxActiveUsers: 2, Timezone: "Europe/Kyiv",
		SessionSecret: []byte("test-secret-012345678901234567890"),
	}
	fxSvc := fx.New(st, nil, slog.Default(), loc)
	svc, err := service.New(st, fxSvc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BootstrapAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := st.AdminUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actor := svc.ActorForUser(admin, domain.SourceMCP)
	token, _, err := svc.CreateAPIToken(ctx, actor, service.APITokenCreateInput{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}

	srv := New(svc, cfg, slog.Default())
	webSrv, err := web.New(svc, cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	webSrv.MCPHandler = srv.HTTPHandler()
	ts := httptest.NewServer(webSrv.Handler())
	defer ts.Close()
	endpoint := ts.URL + "/mcp"

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: testBearer{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	list, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "add_transactions" {
			found = true
		}
	}
	if !found {
		t.Fatalf("add_transactions not advertised: %+v", list.Tools)
	}

	today := svc.Today().String()
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "add_transactions",
		Arguments: map[string]any{
			"transactions": []map[string]any{{
				"amount": "512", "currency": "UAH", "date": today,
				"category": "Їжа", "description": "test lunch",
			}},
			"idempotency_key": "test-add-1",
		},
	})
	if err != nil {
		t.Fatalf("add_transactions: %v", err)
	}
	if res.IsError {
		t.Fatalf("add_transactions returned tool error: %+v", res.Content)
	}
	var created addTransactionsOut
	if err := decodeStructured(res.StructuredContent, &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Created) != 1 || created.Created[0].Amount != "512.00" {
		t.Fatalf("unexpected created output: %+v", created)
	}

	res, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "stats_summary",
		Arguments: map[string]any{
			"from": today, "to": today,
		},
	})
	if err != nil {
		t.Fatalf("stats_summary: %v", err)
	}
	if res.IsError {
		t.Fatalf("stats_summary returned tool error: %+v", res.Content)
	}
	var summary summaryOut
	if err := decodeStructured(res.StructuredContent, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Expenses != "512.00" || summary.Count != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	// Same idempotency key replays the stored response without a second row.
	res, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "add_transactions",
		Arguments: map[string]any{
			"transactions": []map[string]any{{
				"amount": "512", "currency": "UAH", "date": today,
				"category": "Їжа", "description": "test lunch",
			}},
			"idempotency_key": "test-add-1",
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("idempotent replay failed: %v %+v", err, res)
	}
	var replay addTransactionsOut
	if err := decodeStructured(res.StructuredContent, &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay.Created) != 1 || replay.Created[0].UID != created.Created[0].UID {
		t.Fatalf("idempotency did not replay: %+v vs %+v", replay, created)
	}

	member, _, err := svc.CreateUser(ctx, actor, service.CreateUserInput{
		Username: "wife", DisplayName: "Wife", Password: "wifepassword123",
	})
	if err != nil {
		t.Fatal(err)
	}
	memberToken, _, err := svc.CreateAPIToken(ctx, svc.ActorForUser(member, domain.SourceMCP), service.APITokenCreateInput{Name: "member"})
	if err != nil {
		t.Fatal(err)
	}
	memberClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "member-client", Version: "1.0.0"}, nil)
	memberSession, err := memberClient.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: testBearer{token: memberToken}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("member connect: %v", err)
	}
	defer memberSession.Close()
	memberTools, err := memberSession.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("member list tools: %v", err)
	}
	for _, tool := range memberTools.Tools {
		if tool.Name == "create_category" || tool.Name == "create_user" || tool.Name == "upsert_snapshot" {
			t.Fatalf("admin tool %q advertised to member", tool.Name)
		}
	}
	res, _ = memberSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "create_category",
		Arguments: map[string]any{
			"name": "Should not exist", "kind": "expense",
		},
	})
	if res != nil && !res.IsError {
		t.Fatalf("member unexpectedly created a category: %+v", res)
	}
}

func decodeStructured(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
