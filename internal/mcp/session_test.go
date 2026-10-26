package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"skarb/internal/config"
	"skarb/internal/domain"
	"skarb/internal/fx"
	"skarb/internal/service"
	"skarb/internal/store"
)

func TestToolSessionAdminAndMember(t *testing.T) {
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
	admin, err := st.AdminUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminActor := svc.ActorForUser(admin, domain.SourceTelegram)
	adminActor.TelegramUserID = 123
	adminActor.ChatID = 456
	adminActor.RequestID = "tg:1"

	srv := New(svc, cfg, logger)
	adminSession, err := srv.NewToolSession(ctx, adminActor)
	if err != nil {
		t.Fatalf("admin session: %v", err)
	}
	defer adminSession.Close()
	if got := len(adminSession.ListTools()); got != 25 {
		t.Fatalf("admin tools = %d, want 25", got)
	}

	res, err := adminSession.CallTool(ctx, "add_transactions", json.RawMessage(`{
		"transactions":[{"amount":"512","currency":"UAH","category":"Їжа","description":"обід"}],
		"original_text":"512 грн обід"
	}`))
	if err != nil {
		t.Fatalf("add_transactions: %v", err)
	}
	if res.IsError {
		t.Fatalf("add_transactions error: %s", res.Text)
	}
	var created AddTransactionsOutput
	if err := json.Unmarshal(res.Structured, &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Created) != 1 {
		t.Fatalf("created = %d, want 1", len(created.Created))
	}
	tx, err := svc.GetTransaction(ctx, adminActor, created.Created[0].UID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Source != domain.SourceTelegram {
		t.Fatalf("source = %q, want telegram", tx.Source)
	}
	if tx.OriginalText != "512 грн обід" {
		t.Fatalf("original_text = %q", tx.OriginalText)
	}

	member, _, err := svc.CreateUser(ctx, adminActor, service.CreateUserInput{
		Username: "wife", DisplayName: "Wife", Password: "wifepassword123",
	})
	if err != nil {
		t.Fatal(err)
	}
	memberActor := svc.ActorForUser(member, domain.SourceTelegram)
	memberSession, err := srv.NewToolSession(ctx, memberActor)
	if err != nil {
		t.Fatalf("member session: %v", err)
	}
	defer memberSession.Close()
	if got := len(memberSession.ListTools()); got != 15 {
		t.Fatalf("member tools = %d, want 15", got)
	}
	_, err = memberSession.CallTool(ctx, "create_category", json.RawMessage(`{"name":"X","kind":"expense"}`))
	if !errors.Is(err, ErrToolNotAdvertised) {
		t.Fatalf("member create_category error = %v, want ErrToolNotAdvertised", err)
	}
}
