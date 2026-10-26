package telegram

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestBuildToolRenderCoversEveryMCPOutput(t *testing.T) {
	tests := []struct {
		tool string
		raw  string
		want string
	}{
		{"list_transactions", `{"transactions":[{"uid":"t1","kind":"expense","amount":"512.00","currency":"UAH","date":"2026-10-01","category":"Їжа","owner":"Admin","description":"обід"}],"total":1}`, "512"},
		{"stats_summary", `{"from":"2026-10-01","to":"2026-10-31","income_uah":"1000.00","expenses_uah":"512.00","net_uah":"488.00","count":2,"by_currency":{"UAH":{"currency":"UAH","income":"1000.00","expenses":"512.00","count":2}}}`, "Підсумок"},
		{"stats_category_breakdown", `{"from":"2026-10-01","to":"2026-10-31","rows":[{"category":"Їжа","kind":"expense","amount_uah":"512.00","count":1,"percent":100}]}`, "Їжа"},
		{"stats_user_breakdown", `{"from":"2026-10-01","to":"2026-10-31","rows":[{"user":"Admin","income_uah":"1000.00","expenses_uah":"512.00","net_uah":"488.00","count":2}]}`, "Admin"},
		{"stats_compare_periods", `{"a":{"from":"2026-10-01","to":"2026-10-31","income_uah":"1000.00","expenses_uah":"512.00","net_uah":"488.00","count":2},"b":{"from":"2026-09-01","to":"2026-09-30","income_uah":"900.00","expenses_uah":"400.00","net_uah":"500.00","count":2},"income_delta_uah":"100.00","expense_delta_uah":"112.00","net_delta_uah":"-12.00","by_category":[{"category":"Їжа","kind":"expense","current_uah":"512.00","previous_uah":"400.00","delta_uah":"112.00"}]}`, "Порівняння"},
		{"stats_trend", `{"points":[{"date":"2026-10-01","label":"Жовтень","income_uah":"1000.00","expenses_uah":"512.00"}]}`, "Жовтень"},
		{"net_worth", `{"as_of":"2026-10-26","total_uah":"1000.00","accounts":[{"account":"Cash","owner":"Admin","currency":"UAH","balance":"1000.00","has_rate":true,"balance_uah":"1000.00"}]}`, "Cash"},
		{"savings_history", `{"points":[{"date":"2026-10-01","total_uah":"1000.00","delta_uah":"100.00","pct":10}]}`, "100"},
		{"list_categories", `{"categories":[{"uid":"c1","name":"Їжа","kind":"expense","color":"","emoji":"🍲","sort_order":1,"aliases":["їжа"],"archived":false}]}`, "Їжа"},
		{"list_accounts", `{"accounts":[{"uid":"a1","owner":"Admin","name":"Cash","currency":"UAH","kind":"cash","include_in_net_worth":true,"archived":false,"sort_order":1}]}`, "Cash"},
		{"list_exchange_rates", `{"rates":[{"currency":"USD","date":"2026-10-26","uah_per_unit":"41.23456789","source":"NBU"}]}`, "USD"},
		{"list_users", `{"users":[{"uid":"u1","username":"admin","display_name":"Admin","role":"admin","active":true,"telegram_user_id":"123","telegram_linked":true}]}`, "Admin"},
		{"update_transaction", `{"uid":"t1","kind":"expense","amount":"512.00","currency":"UAH","date":"2026-10-01","category":"Їжа","owner":"Admin","description":"обід","source":"telegram"}`, "оновлено"},
		{"create_category", `{"uid":"c1","name":"Їжа","kind":"expense","color":"","emoji":"🍲","sort_order":1,"aliases":[],"archived":false}`, "Їжа"},
		{"update_category", `{"uid":"c1","name":"Їжа","kind":"expense","color":"","emoji":"🍲","sort_order":1,"aliases":[],"archived":false}`, "Їжа"},
		{"create_account", `{"uid":"a1","owner":"Admin","name":"Cash","currency":"UAH","kind":"cash","include_in_net_worth":true,"archived":false,"sort_order":1}`, "Cash"},
		{"update_account", `{"uid":"a1","owner":"Admin","name":"Cash","currency":"UAH","kind":"cash","include_in_net_worth":true,"archived":true,"sort_order":1}`, "архів"},
		{"upsert_snapshot", `{"uid":"s1","account":"a1","account_name":"Cash","date":"2026-10-26","amount":"1000.00","currency":"UAH"}`, "Cash"},
		{"update_user", `{"uid":"u1","username":"admin","display_name":"Admin","role":"admin","active":true,"telegram_linked":true}`, "Admin"},
		{"delete_transaction", `{"deleted":true,"uid":"t1"}`, "Видалено"},
		{"delete_category", `{"deleted":true,"uid":"c1"}`, "Видалено"},
		{"delete_account", `{"deleted":true,"uid":"a1"}`, "Видалено"},
		{"delete_snapshot", `{"deleted":true,"uid":"s1"}`, "Видалено"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			render, err := buildToolRender(tt.tool, json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			text := render.text(0)
			if !strings.Contains(text, tt.want) {
				t.Fatalf("text %q does not contain %q", text, tt.want)
			}
		})
	}
}

func TestTransactionPageButtons(t *testing.T) {
	raw := `{"transactions":[{"uid":"u1","kind":"expense","amount":"10.00","currency":"UAH","date":"2026-10-01","category":"Їжа","owner":"Admin"},{"uid":"u2","kind":"income","amount":"20.00","currency":"UAH","date":"2026-10-02","category":"Зарплата","owner":"Admin"}],"total":2}`
	render, err := buildToolRender("list_transactions", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	markup := render.markup(0, "page-token")
	keyboard, ok := markup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("unexpected markup: %T", markup)
	}
	if len(keyboard.InlineKeyboard) != 1 {
		t.Fatalf("keyboard rows = %d, want 1", len(keyboard.InlineKeyboard))
	}
	if got := keyboard.InlineKeyboard[0][0].CallbackData; got != "td:u1" {
		t.Fatalf("first delete callback = %q", got)
	}
	if got := keyboard.InlineKeyboard[0][1].CallbackData; got != "td:u2" {
		t.Fatalf("second delete callback = %q", got)
	}
}
