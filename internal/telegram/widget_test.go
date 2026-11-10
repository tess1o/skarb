package telegram

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"skarb/internal/domain"
)

func TestBuildStatsCardRendersTotalsAndCategories(t *testing.T) {
	in := statsCardInput{
		From: domain.NewDate(2026, 11, 1), To: domain.NewDate(2026, 11, 30),
		Totals:          &statsTotals{Income: 1_000_000, Expense: 630_000, Net: 370_000, Count: 42, MissingRate: 1},
		CategoriesTitle: "Витрати за категоріями",
		Categories: []statsCategory{
			{Emoji: "🍲", Name: "Їжа", Color: "#59a14f", Amount: 400_000, Percent: 63.5},
			{Name: `<script>alert(1)</script>`, Color: "red;background:url(x)", Amount: 230_000, Percent: 36.5},
		},
	}
	card, err := buildStatsCard(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Листопад 2026", "01.11 — 30.11.2026", "Їжа", "Витрачено 63% доходів",
		"width:63%", "background:#59a14f", "Операцій: 42", "без курсу: 1"} {
		if !strings.Contains(card.HTML, want) {
			t.Errorf("card HTML lacks %q", want)
		}
	}
	if strings.Contains(card.HTML, "<script>alert") || strings.Contains(card.HTML, "url(x)") {
		t.Fatal("category name or colour was not sanitised")
	}
	if !strings.Contains(card.HTML, "background:"+categoryPalette[1]) {
		t.Error("invalid colour should fall back to the palette")
	}
	if !strings.Contains(card.Caption, "Листопад 2026") || !strings.Contains(card.Caption, "Баланс") {
		t.Errorf("caption should summarise the card, got %q", card.Caption)
	}
}

func TestBuildStatsCardStaysWithinTelegramLimits(t *testing.T) {
	var cats []statsCategory
	for i := range 40 {
		cats = append(cats, statsCategory{
			Name:   fmt.Sprintf("Категорія з дуже довгою назвою номер %d %s", i, strings.Repeat("ж", i*7)),
			Amount: int64(1000 * (40 - i)), Percent: 2.5,
		})
	}
	card, err := buildStatsCard(statsCardInput{
		From: domain.NewDate(2026, 10, 3), To: domain.NewDate(2026, 10, 17),
		CategoriesTitle: "Витрати за категоріями", Categories: cats,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !widgetFits([]byte(card.HTML)) {
		t.Fatal("card exceeds the widget size limits")
	}
	if !strings.Contains(card.HTML, "…і ще") {
		t.Error("hidden categories should be counted")
	}
	if !strings.Contains(card.HTML, "Статистика") || !strings.Contains(card.HTML, "03.10 — 17.10.2026") {
		t.Error("a partial month should be titled with its date range")
	}
}

func TestWidgetFitsRejectsOversizedHTML(t *testing.T) {
	if widgetFits([]byte(strings.Repeat("a", widgetMaxHTMLBytes+1))) {
		t.Fatal("raw size limit not enforced")
	}
	rng := rand.New(rand.NewPCG(1, 2))
	noise := make([]byte, 12000)
	for i := range noise {
		noise[i] = byte('!' + rng.IntN(90))
	}
	if widgetFits(noise) {
		t.Fatal("compressed size limit not enforced")
	}
	if widgetFits([]byte("a\x00b")) {
		t.Fatal("NUL bytes must be rejected")
	}
}

func TestStatsCardFromTool(t *testing.T) {
	summary := `{"from":"2026-10-01","to":"2026-10-31","income_uah":"1000.00","expenses_uah":"512.00","net_uah":"488.00","count":2}`
	card, ok := statsCardFromTool("stats_summary", []byte(summary))
	if !ok || !strings.Contains(card.HTML, "Жовтень 2026") || !strings.Contains(card.HTML, "−512 ₴") {
		t.Fatalf("stats_summary card not built: ok=%v", ok)
	}
	cats := `{"from":"2026-10-01","to":"2026-10-31","rows":[{"category":"Зарплата","kind":"income","amount_uah":"1000.00","count":1,"percent":100}]}`
	card, ok = statsCardFromTool("stats_category_breakdown", []byte(cats))
	if !ok || !strings.Contains(card.HTML, "Доходи за категоріями") || !strings.Contains(card.Caption, "Зарплата") {
		t.Fatalf("stats_category_breakdown card not built: ok=%v", ok)
	}
	if _, ok := statsCardFromTool("stats_category_breakdown", []byte(`{"rows":[]}`)); ok {
		t.Error("empty breakdown should not produce a card")
	}
	if _, ok := statsCardFromTool("list_transactions", []byte(`{}`)); ok {
		t.Error("unrelated tools should not produce a card")
	}
}

func TestStatsCommandSendsWidgetOnlyWhereAllowed(t *testing.T) {
	env := newToolTestEnv(t, nil)
	actor := env.svc.ActorForUser(env.admin, domain.SourceTelegram)

	env.bot.widgets = true
	if err := env.bot.cmdStats(env.ctx, env.adminTG, actor, nil); err != nil {
		t.Fatal(err)
	}
	env.fake.mu.Lock()
	widgets, sent := len(env.fake.widgets), len(env.fake.sent)
	var body map[string]any
	if widgets > 0 {
		body = env.fake.widgets[0]
	}
	env.fake.mu.Unlock()
	if widgets != 1 || sent != 0 {
		t.Fatalf("private chat: want 1 widget and no text, got %d widgets, %d messages", widgets, sent)
	}
	if html, _ := body["html"].(string); !strings.Contains(html, "Жовтень 2026") {
		t.Errorf("widget HTML lacks the month title: %q", html)
	}
	if body["parse_mode"] != "HTML" || body["caption"] == "" {
		t.Errorf("widget caption not sent as HTML: %v", body)
	}

	// Groups cannot receive widgets yet.
	if err := env.bot.cmdStats(env.ctx, -100123, actor, nil); err != nil {
		t.Fatal(err)
	}
	// A rejected widget falls back to text.
	env.fake.mu.Lock()
	env.fake.failWidget = true
	env.fake.mu.Unlock()
	if err := env.bot.cmdStats(env.ctx, env.adminTG, actor, nil); err != nil {
		t.Fatal(err)
	}
	// Disabled widgets always send text.
	env.bot.widgets = false
	env.fake.mu.Lock()
	env.fake.failWidget = false
	env.fake.mu.Unlock()
	if err := env.bot.cmdStats(env.ctx, env.adminTG, actor, nil); err != nil {
		t.Fatal(err)
	}

	env.fake.mu.Lock()
	defer env.fake.mu.Unlock()
	if len(env.fake.widgets) != 1 || len(env.fake.sent) != 3 {
		t.Fatalf("want 1 widget and 3 text fallbacks, got %d widgets, %d messages", len(env.fake.widgets), len(env.fake.sent))
	}
	for _, msg := range env.fake.sent {
		if text, _ := msg["text"].(string); !strings.Contains(text, "Доходи") {
			t.Errorf("fallback text lacks the summary: %q", text)
		}
	}
}
