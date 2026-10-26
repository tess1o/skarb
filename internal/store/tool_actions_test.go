package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestToolActionLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	action, err := st.CreateToolAction(ctx, nil, CreateToolActionInput{
		Token:               "tok-confirm",
		ChatID:              123,
		ActorTelegramUserID: 456,
		Action:              "confirm",
		ToolName:            "delete_transaction",
		ArgumentsJSON:       `{"uid":"abc"}`,
		ExpiresAt:           time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if action.Status != "pending" || action.PageSize != 20 {
		t.Fatalf("unexpected action: %+v", action)
	}
	if err := st.BindToolActionMessage(ctx, "tok-confirm", 77); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetToolAction(ctx, "tok-confirm")
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID != 77 || got.ToolName != "delete_transaction" {
		t.Fatalf("unexpected stored action: %+v", got)
	}
	used, err := st.UseToolAction(ctx, "tok-confirm", time.Now())
	if err != nil || !used {
		t.Fatalf("first use = %v, %v", used, err)
	}
	used, err = st.UseToolAction(ctx, "tok-confirm", time.Now())
	if err != nil || used {
		t.Fatalf("second use = %v, %v", used, err)
	}

	if _, err := st.CreateToolAction(ctx, nil, CreateToolActionInput{
		Token: "tok-cancel", ChatID: 123, ActorTelegramUserID: 456,
		Action: "confirm", ToolName: "delete_transaction",
		ArgumentsJSON: `{}`, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := st.CancelToolAction(ctx, "tok-cancel", time.Now())
	if err != nil || !cancelled {
		t.Fatalf("cancel = %v, %v", cancelled, err)
	}
	cancelled, err = st.CancelToolAction(ctx, "tok-cancel", time.Now())
	if err != nil || cancelled {
		t.Fatalf("second cancel = %v, %v", cancelled, err)
	}

	if _, err := st.CreateToolAction(ctx, nil, CreateToolActionInput{
		Token: "tok-page", ChatID: 123, ActorTelegramUserID: 456,
		Action: "page", ToolName: "list_categories",
		ArgumentsJSON: `{}`, ResultJSON: `{"categories":[]}`,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	advanced, err := st.AdvanceToolPage(ctx, "tok-page", 0, "cursor-1", time.Now())
	if err != nil || !advanced {
		t.Fatalf("advance = %v, %v", advanced, err)
	}
	advanced, err = st.AdvanceToolPage(ctx, "tok-page", 0, "cursor-2", time.Now())
	if err != nil || advanced {
		t.Fatalf("stale advance = %v, %v", advanced, err)
	}
	page, err := st.GetToolAction(ctx, "tok-page")
	if err != nil {
		t.Fatal(err)
	}
	if page.PageNumber != 1 || page.NextCursor != "cursor-1" {
		t.Fatalf("unexpected page state: %+v", page)
	}

	if _, err := st.CreateToolAction(ctx, nil, CreateToolActionInput{
		Token: "tok-expired", ChatID: 123, ActorTelegramUserID: 456,
		Action: "confirm", ToolName: "delete_transaction",
		ArgumentsJSON: `{}`, ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	used, err = st.UseToolAction(ctx, "tok-expired", time.Now())
	if err != nil || used {
		t.Fatalf("expired use = %v, %v", used, err)
	}
}
