package telegram

import (
	"bytes"
	"compress/zlib"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"skarb/internal/domain"
	"skarb/internal/mcp"
	"skarb/internal/money"
)

// Bot API 10.4 widget limits: the HTML must be at most 32768 bytes and at most
// 4096 bytes after zlib compression at the maximum level.
const (
	widgetMaxHTMLBytes       = 32768
	widgetMaxCompressedBytes = 4096
	widgetMaxCategories      = 8
)

var errWidgetTooLarge = errors.New("widget html exceeds Telegram size limits")

//go:embed widgets/stats.html
var statsWidgetSource string

var statsWidgetTmpl = template.Must(template.New("stats").Parse(statsWidgetSource))

// widgetCard is a rendered HTML widget ready for sendWidget. The caption is
// what notifications, the chat list and apps without widget support show, so
// it must stand on its own.
type widgetCard struct {
	HTML    string
	Caption string
}

type statsCardCategory struct {
	Label   string
	Amount  string
	Percent string
	Style   template.CSS
}

type statsCardView struct {
	Title           string
	Period          string
	HasTotals       bool
	Income          string
	Expense         string
	Net             string
	NetNegative     bool
	SpentShare      int
	SpentStyle      template.CSS
	CategoriesTitle string
	Categories      []statsCardCategory
	MoreCategories  int
	Footer          string
}

// statsTotals holds the numbers of a summary in minor UAH units.
type statsTotals struct {
	Income, Expense, Net int64
	Count, MissingRate   int
}

type statsCategory struct {
	Emoji, Name, Color string
	Amount             int64
	Percent            float64
}

type statsCardInput struct {
	From, To        domain.Date
	Totals          *statsTotals
	CategoriesTitle string
	Categories      []statsCategory
}

var cssHexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// categoryPalette is used for categories without a valid colour of their own.
var categoryPalette = []string{"#4e79a7", "#f28e2b", "#e15759", "#76b7b2", "#59a14f", "#edc948", "#b07aa1", "#ff9da7"}

var ukMonths = [...]string{"", "Січень", "Лютий", "Березень", "Квітень", "Травень", "Червень",
	"Липень", "Серпень", "Вересень", "Жовтень", "Листопад", "Грудень"}

// buildStatsCard renders the stats widget. When the HTML does not fit the
// compressed size limit, the category list is shortened until it does.
func buildStatsCard(in statsCardInput) (*widgetCard, error) {
	view := statsCardView{Title: "📊 " + periodTitle(in.From, in.To), Period: periodRange(in.From, in.To)}
	var caption strings.Builder
	caption.WriteString(view.Title)
	if t := in.Totals; t != nil {
		view.HasTotals = true
		view.Income = money.FormatAmount(t.Income, "UAH")
		view.Expense = money.FormatAmount(t.Expense, "UAH")
		view.Net = money.FormatAmount(t.Net, "UAH")
		view.NetNegative = t.Net < 0
		if t.Income > 0 && t.Expense > 0 {
			share := int((t.Expense*100 + t.Income/2) / t.Income)
			view.SpentShare = max(share, 1)
			view.SpentStyle = template.CSS(fmt.Sprintf("width:%d%%", min(view.SpentShare, 100)))
		}
		view.Footer = fmt.Sprintf("Операцій: %d", t.Count)
		if t.MissingRate > 0 {
			view.Footer += fmt.Sprintf(" · ⚠️ без курсу: %d", t.MissingRate)
		}
		fmt.Fprintf(&caption, "\nДоходи: <b>+%s</b> · Витрати: <b>−%s</b> · Баланс: <b>%s</b>",
			view.Income, view.Expense, view.Net)
	}

	cats := in.Categories
	shown := min(len(cats), widgetMaxCategories)
	if shown > 0 {
		view.CategoriesTitle = in.CategoriesTitle
		if in.Totals == nil {
			top := cats[0]
			fmt.Fprintf(&caption, "\n%s: %s %s", escape(in.CategoriesTitle), escape(top.Name),
				money.FormatAmount(top.Amount, "UAH"))
		}
	}
	for ; ; shown-- {
		view.Categories = statsCategoryRows(cats[:shown])
		view.MoreCategories = len(cats) - shown
		var buf bytes.Buffer
		if err := statsWidgetTmpl.Execute(&buf, view); err != nil {
			return nil, err
		}
		if widgetFits(buf.Bytes()) {
			return &widgetCard{HTML: buf.String(), Caption: caption.String()}, nil
		}
		if shown == 0 {
			return nil, errWidgetTooLarge
		}
	}
}

func statsCategoryRows(cats []statsCategory) []statsCardCategory {
	var maxAmount int64
	for _, c := range cats {
		maxAmount = max(maxAmount, c.Amount)
	}
	rows := make([]statsCardCategory, 0, len(cats))
	for i, c := range cats {
		color := c.Color
		if !cssHexColor.MatchString(color) {
			color = categoryPalette[i%len(categoryPalette)]
		}
		width := 0.0
		if maxAmount > 0 && c.Amount > 0 {
			width = max(float64(c.Amount)*100/float64(maxAmount), 2)
		}
		label := strings.TrimSpace(c.Emoji + " " + c.Name)
		rows = append(rows, statsCardCategory{
			Label:   label,
			Amount:  money.FormatAmount(c.Amount, "UAH"),
			Percent: fmt.Sprintf("%.1f", c.Percent),
			Style:   template.CSS(fmt.Sprintf("width:%.1f%%;background:%s", width, color)),
		})
	}
	return rows
}

func widgetFits(html []byte) bool {
	if len(html) == 0 || len(html) > widgetMaxHTMLBytes || bytes.IndexByte(html, 0) >= 0 {
		return false
	}
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return false
	}
	if _, err := w.Write(html); err != nil {
		return false
	}
	if err := w.Close(); err != nil {
		return false
	}
	return buf.Len() <= widgetMaxCompressedBytes
}

// periodTitle names a whole calendar month ("Листопад 2026") and otherwise
// falls back to the date range.
func periodTitle(from, to domain.Date) string {
	if from.IsZero() || to.IsZero() {
		return "Статистика"
	}
	if from.Day() == 1 && from.Year() == to.Year() && from.Month() == to.Month() &&
		to.AddDays(1).Month() != to.Month() {
		return fmt.Sprintf("%s %d", ukMonths[from.Month()], from.Year())
	}
	return "Статистика"
}

func periodRange(from, to domain.Date) string {
	if from.IsZero() || to.IsZero() {
		return ""
	}
	if from.Year() == to.Year() {
		return from.Format("02.01") + " — " + to.Format("02.01.2006")
	}
	return from.Format("02.01.2006") + " — " + to.Format("02.01.2006")
}

func statsCardFromSummary(sum *domain.Summary, cats []domain.CategoryTotal, catTitle string) statsCardInput {
	in := statsCardInput{From: sum.From, To: sum.To, CategoriesTitle: catTitle,
		Totals: &statsTotals{Income: sum.IncomeUAH, Expense: sum.ExpenseUAH, Net: sum.NetUAH,
			Count: sum.Count, MissingRate: sum.MissingRate}}
	for _, c := range cats {
		in.Categories = append(in.Categories, statsCategory{Emoji: c.Emoji, Name: c.Name, Color: c.Color,
			Amount: c.AmountUAH, Percent: c.Percent})
	}
	return in
}

// statsCardFromTool builds a card from a tool-mode stats result. It returns
// false for tools without a card and for outputs it cannot read.
func statsCardFromTool(tool string, raw []byte) (*widgetCard, bool) {
	scale := money.MinorUnits("UAH")
	var in statsCardInput
	switch tool {
	case "stats_summary":
		var out mcp.SummaryOutput
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, false
		}
		income, ok1 := parseDisplayAmount(out.Income, scale)
		expense, ok2 := parseDisplayAmount(out.Expenses, scale)
		net, ok3 := parseDisplayAmount(out.Net, scale)
		if !ok1 || !ok2 || !ok3 {
			return nil, false
		}
		in.From, _ = domain.ParseDate(out.From)
		in.To, _ = domain.ParseDate(out.To)
		in.Totals = &statsTotals{Income: income, Expense: expense, Net: net, Count: out.Count, MissingRate: out.MissingRate}
	case "stats_category_breakdown":
		var out mcp.StatsCategoryOutput
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Rows) == 0 {
			return nil, false
		}
		in.From, _ = domain.ParseDate(out.From)
		in.To, _ = domain.ParseDate(out.To)
		in.CategoriesTitle = "Витрати за категоріями"
		if out.Rows[0].Kind == string(domain.KindIncome) {
			in.CategoriesTitle = "Доходи за категоріями"
		}
		for _, row := range out.Rows {
			amount, ok := parseDisplayAmount(row.Amount, scale)
			if !ok {
				return nil, false
			}
			in.Categories = append(in.Categories, statsCategory{Name: row.Category, Amount: amount, Percent: row.Percent})
		}
	default:
		return nil, false
	}
	card, err := buildStatsCard(in)
	if err != nil {
		return nil, false
	}
	return card, true
}

// isPrivateChatID reports whether a chat ID belongs to a private chat. In the
// Bot API, users (and so private chats) have positive IDs, while groups,
// supergroups and channels have negative ones.
func isPrivateChatID(chatID int64) bool { return chatID > 0 }

// replyCard sends card as an HTML widget where Telegram allows it and falls
// back to the plain HTML text otherwise. Widgets can currently be sent only
// to private chats in production, and older apps show only the caption.
func (b *Bot) replyCard(ctx context.Context, chatID int64, card *widgetCard, fallback string) error {
	if card == nil || !b.widgets || !isPrivateChatID(chatID) {
		return b.reply(ctx, chatID, fallback, nil)
	}
	msg, err := b.api.SendWidget(ctx, &tgbot.SendWidgetParams{
		ChatID:    chatID,
		HTML:      card.HTML,
		Caption:   card.Caption,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		b.log.Warn("telegram sendWidget failed; sending text instead",
			"chat_id", chatID, "html_bytes", len(card.HTML), "error", redactToken(err, b.cfg.TelegramBotToken))
		return b.reply(ctx, chatID, fallback, nil)
	}
	b.log.Debug("telegram widget sent", "chat_id", chatID, "message_id", msg.ID)
	return nil
}
