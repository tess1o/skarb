package telegram

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
	"skarb/internal/domain"
	"skarb/internal/mcp"
	"skarb/internal/money"
)

const toolPageSize = 20

type toolRow struct {
	Text      string
	DeleteUID string
}

type toolRender struct {
	Title       string
	Lines       []string
	Rows        []toolRow
	Total       int
	NextCursor  string
	ServerPaged bool
}

func buildToolRender(tool string, raw json.RawMessage) (*toolRender, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty tool result")
	}
	switch tool {
	case "list_transactions":
		return renderTransactions(raw)
	case "stats_summary":
		return renderSummary(raw)
	case "stats_category_breakdown":
		return renderCategoryTotals(raw)
	case "stats_user_breakdown":
		return renderUserTotals(raw)
	case "stats_compare_periods":
		return renderCompare(raw)
	case "stats_trend":
		return renderTrend(raw)
	case "net_worth":
		return renderNetWorth(raw)
	case "savings_history":
		return renderSavingsHistory(raw)
	case "list_categories":
		return renderCategories(raw)
	case "list_accounts":
		return renderAccounts(raw)
	case "list_exchange_rates":
		return renderRates(raw)
	case "list_users":
		return renderUsers(raw)
	case "update_transaction":
		return renderTransactionResult(raw)
	case "create_category", "update_category":
		return renderCategoryResult(raw)
	case "create_account", "update_account":
		return renderAccountResult(raw)
	case "upsert_snapshot":
		return renderSnapshotResult(raw)
	case "update_user":
		return renderUserResult(raw)
	case "delete_transaction", "delete_category", "delete_account", "delete_snapshot":
		return renderDeleteResult(raw)
	default:
		return renderGeneric(tool, raw)
	}
}

func renderTransactions(raw json.RawMessage) (*toolRender, error) {
	var out mcp.ListTransactionsOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "🧾 Транзакції", Total: out.Total, NextCursor: out.NextCursor, ServerPaged: true}
	for _, tx := range out.Transactions {
		sign := "−"
		if tx.Kind == string(domain.KindIncome) {
			sign = "+"
		}
		line := fmt.Sprintf("<b>%s</b> · %s %s%s · %s",
			escape(tx.Category), formatShortDate(tx.Date), sign, formatMoney(tx.Amount, tx.Currency), escape(tx.Owner))
		if strings.TrimSpace(tx.Description) != "" && tx.Description != tx.Category {
			line += "\n   <i>" + escape(truncate(tx.Description, 80)) + "</i>"
		} else if strings.TrimSpace(tx.Note) != "" {
			line += "\n   <i>" + escape(truncate(tx.Note, 80)) + "</i>"
		}
		if tx.Deleted {
			line += " · ❌ скасовано"
		}
		r.Rows = append(r.Rows, toolRow{Text: line, DeleteUID: tx.UID})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Немає транзакцій за цим фільтром.")
	}
	return r, nil
}

func renderSummary(raw json.RawMessage) (*toolRender, error) {
	var out mcp.SummaryOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "📊 Підсумок"}
	r.Lines = append(r.Lines,
		fmt.Sprintf("Період: %s – %s", formatDate(out.From), formatDate(out.To)),
		fmt.Sprintf("Доходи: <b>+%s</b>", formatMoney(out.Income, "UAH")),
		fmt.Sprintf("Витрати: <b>−%s</b>", formatMoney(out.Expenses, "UAH")),
		fmt.Sprintf("Баланс: <b>%s</b>", formatMoney(out.Net, "UAH")),
		fmt.Sprintf("Операцій: %d", out.Count),
	)
	if out.MissingRate > 0 {
		r.Lines = append(r.Lines, fmt.Sprintf("⚠️ Без курсу: %d", out.MissingRate))
	}
	if len(out.ByCurrency) > 0 {
		codes := make([]string, 0, len(out.ByCurrency))
		for code := range out.ByCurrency {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		for _, code := range codes {
			ct := out.ByCurrency[code]
			r.Lines = append(r.Lines, fmt.Sprintf("• %s: +%s / −%s (%d)",
				escape(code), formatMoney(ct.Income, code), formatMoney(ct.Expenses, code), ct.Count))
		}
	}
	return r, nil
}

func renderCategoryTotals(raw json.RawMessage) (*toolRender, error) {
	var out mcp.StatsCategoryOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	title := "📂 Витрати за категоріями"
	if len(out.Rows) > 0 && out.Rows[0].Kind == string(domain.KindIncome) {
		title = "📂 Доходи за категоріями"
	}
	r := &toolRender{Title: title, Total: len(out.Rows)}
	for _, row := range out.Rows {
		r.Rows = append(r.Rows, toolRow{Text: fmt.Sprintf("<b>%s</b> — %s (%.1f%%, %d)",
			escape(row.Category), formatMoney(row.Amount, "UAH"), row.Percent, row.Count)})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Немає даних за період.")
	}
	return r, nil
}

func renderUserTotals(raw json.RawMessage) (*toolRender, error) {
	var out mcp.StatsUsersOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "👥 По користувачах", Total: len(out.Rows)}
	for _, row := range out.Rows {
		r.Rows = append(r.Rows, toolRow{Text: fmt.Sprintf("<b>%s</b> — +%s / −%s, баланс %s (%d)",
			escape(row.User), formatMoney(row.Income, "UAH"), formatMoney(row.Expenses, "UAH"),
			formatMoney(row.Net, "UAH"), row.Count)})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Немає даних за період.")
	}
	return r, nil
}

func renderCompare(raw json.RawMessage) (*toolRender, error) {
	var out mcp.StatsCompareOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "📊 Порівняння", Total: len(out.ByCategory)}
	r.Lines = append(r.Lines,
		fmt.Sprintf("A: %s – %s · +%s / −%s", formatDate(out.A.From), formatDate(out.A.To),
			formatMoney(out.A.Income, "UAH"), formatMoney(out.A.Expenses, "UAH")),
		fmt.Sprintf("B: %s – %s · +%s / −%s", formatDate(out.B.From), formatDate(out.B.To),
			formatMoney(out.B.Income, "UAH"), formatMoney(out.B.Expenses, "UAH")),
		fmt.Sprintf("Δ доходи: %s", formatSignedMoney(out.IncomeDelta, "UAH")),
		fmt.Sprintf("Δ витрати: %s", formatSignedMoney(out.ExpenseDelta, "UAH")),
		fmt.Sprintf("Δ баланс: %s", formatSignedMoney(out.NetDelta, "UAH")),
	)
	for _, row := range out.ByCategory {
		r.Rows = append(r.Rows, toolRow{Text: fmt.Sprintf("<b>%s</b>: %s (%s → %s)",
			escape(row.Category), formatSignedMoney(row.Delta, "UAH"),
			formatMoney(row.Previous, "UAH"), formatMoney(row.Current, "UAH"))})
	}
	return r, nil
}

func renderTrend(raw json.RawMessage) (*toolRender, error) {
	var out mcp.StatsTrendOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "📈 Динаміка", Total: len(out.Points)}
	for _, p := range out.Points {
		r.Rows = append(r.Rows, toolRow{Text: fmt.Sprintf("<b>%s</b> — +%s / −%s",
			escape(p.Label), formatMoney(p.Income, "UAH"), formatMoney(p.Expenses, "UAH"))})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Немає даних.")
	}
	return r, nil
}

func renderNetWorth(raw json.RawMessage) (*toolRender, error) {
	var out mcp.NetWorthOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "💰 Заощадження", Total: len(out.Accounts)}
	r.Lines = append(r.Lines,
		fmt.Sprintf("Станом на %s", formatDate(out.AsOf)),
		fmt.Sprintf("Разом: <b>%s</b>", formatMoney(out.TotalUAH, "UAH")),
	)
	for _, a := range out.Accounts {
		text := fmt.Sprintf("<b>%s</b> (%s) — %s", escape(a.Account), escape(a.Owner), formatMoney(a.Balance, a.Currency))
		if a.HasRate {
			text += " ≈ " + formatMoney(a.BalanceUAH, "UAH")
		} else {
			text += " · без курсу"
		}
		r.Rows = append(r.Rows, toolRow{Text: text})
	}
	for _, warning := range out.Warnings {
		r.Lines = append(r.Lines, "⚠️ "+escape(warning))
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Немає рахунків або знімків.")
	}
	return r, nil
}

func renderSavingsHistory(raw json.RawMessage) (*toolRender, error) {
	var out mcp.SavingsHistoryOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "💰 Історія заощаджень", Total: len(out.Points)}
	for _, p := range out.Points {
		text := fmt.Sprintf("<b>%s</b> — %s", formatDate(p.Date), formatMoney(p.TotalUAH, "UAH"))
		if p.DeltaUAH != "" {
			text += " (" + formatSignedMoney(p.DeltaUAH, "UAH")
			if p.Pct != 0 {
				text += fmt.Sprintf(", %.1f%%", p.Pct)
			}
			text += ")"
		}
		if p.Missing > 0 {
			text += fmt.Sprintf(" · ⚠️ без курсу: %d", p.Missing)
		}
		r.Rows = append(r.Rows, toolRow{Text: text})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Немає даних.")
	}
	return r, nil
}

func renderCategories(raw json.RawMessage) (*toolRender, error) {
	var out mcp.ListCategoriesOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "📂 Категорії", Total: len(out.Categories)}
	for _, c := range out.Categories {
		text := fmt.Sprintf("%s <b>%s</b> — %s", escape(c.Emoji), escape(c.Name), escape(c.Kind))
		if len(c.Aliases) > 0 {
			text += " · " + escape(truncate(strings.Join(c.Aliases, ", "), 80))
		}
		if c.Archived {
			text += " · 📦 архів"
		}
		r.Rows = append(r.Rows, toolRow{Text: text})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Категорій немає.")
	}
	return r, nil
}

func renderAccounts(raw json.RawMessage) (*toolRender, error) {
	var out mcp.ListAccountsOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "🏦 Рахунки", Total: len(out.Accounts)}
	for _, a := range out.Accounts {
		text := fmt.Sprintf("<b>%s</b> (%s) — %s · %s", escape(a.Name), escape(a.Owner), escape(a.Currency), escape(a.Kind))
		if !a.InNetWorth {
			text += " · не в net worth"
		}
		if a.Archived {
			text += " · 📦 архів"
		}
		r.Rows = append(r.Rows, toolRow{Text: text})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Рахунків немає.")
	}
	return r, nil
}

func renderRates(raw json.RawMessage) (*toolRender, error) {
	var out mcp.ListExchangeRatesOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "💱 Курси NBU", Total: len(out.Rates)}
	for _, rate := range out.Rates {
		r.Rows = append(r.Rows, toolRow{Text: fmt.Sprintf("<b>%s</b> · %s — %s · %s",
			escape(rate.Currency), formatDate(rate.Date), formatRate(rate.Rate), escape(rate.Source))})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Курсів немає.")
	}
	return r, nil
}

func renderUsers(raw json.RawMessage) (*toolRender, error) {
	var out mcp.ListUsersOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "👥 Користувачі", Total: len(out.Users)}
	for _, u := range out.Users {
		status := "активний"
		if !u.Active {
			status = "неактивний"
		}
		linked := "не прив'язаний"
		if u.Linked {
			linked = "Telegram прив'язано"
		}
		r.Rows = append(r.Rows, toolRow{Text: fmt.Sprintf("<b>%s</b> (@%s) — %s, %s, %s",
			escape(u.DisplayName), escape(u.Username), escape(u.Role), status, linked)})
	}
	if len(r.Rows) == 0 {
		r.Lines = append(r.Lines, "Користувачів немає.")
	}
	return r, nil
}

func renderTransactionResult(raw json.RawMessage) (*toolRender, error) {
	var out mcp.TransactionOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	sign := "−"
	if out.Kind == string(domain.KindIncome) {
		sign = "+"
	}
	r := &toolRender{Title: "✅ Транзакцію оновлено"}
	r.Lines = append(r.Lines,
		fmt.Sprintf("<b>%s</b> — %s%s · %s · %s", escape(out.Category), sign,
			formatMoney(out.Amount, out.Currency), formatShortDate(out.Date), escape(out.Owner)),
	)
	if out.Description != "" && out.Description != out.Category {
		r.Lines = append(r.Lines, "<i>"+escape(out.Description)+"</i>")
	}
	return r, nil
}

func renderCategoryResult(raw json.RawMessage) (*toolRender, error) {
	var out mcp.CategoryOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "✅ Категорію збережено"}
	r.Lines = append(r.Lines, fmt.Sprintf("%s <b>%s</b> — %s", escape(out.Emoji), escape(out.Name), escape(out.Kind)))
	if len(out.Aliases) > 0 {
		r.Lines = append(r.Lines, "Аліаси: "+escape(strings.Join(out.Aliases, ", ")))
	}
	return r, nil
}

func renderAccountResult(raw json.RawMessage) (*toolRender, error) {
	var out mcp.AccountOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "✅ Рахунок збережено"}
	r.Lines = append(r.Lines, fmt.Sprintf("<b>%s</b> (%s) — %s · %s", escape(out.Name), escape(out.Owner), escape(out.Currency), escape(out.Kind)))
	if out.Archived {
		r.Lines = append(r.Lines, "📦 Рахунок в архіві.")
	}
	return r, nil
}

func renderSnapshotResult(raw json.RawMessage) (*toolRender, error) {
	var out mcp.SnapshotOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "✅ Залишок збережено"}
	r.Lines = append(r.Lines, fmt.Sprintf("<b>%s</b> — %s на %s", escape(out.AccountName),
		formatMoney(out.Amount, out.Currency), formatDate(out.Date)))
	if out.Note != "" {
		r.Lines = append(r.Lines, "<i>"+escape(out.Note)+"</i>")
	}
	return r, nil
}

func renderUserResult(raw json.RawMessage) (*toolRender, error) {
	var out mcp.UserOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	status := "активний"
	if !out.Active {
		status = "неактивний"
	}
	r := &toolRender{Title: "✅ Користувача оновлено"}
	r.Lines = append(r.Lines, fmt.Sprintf("<b>%s</b> (@%s) — %s, %s", escape(out.DisplayName), escape(out.Username), escape(out.Role), status))
	return r, nil
}

func renderDeleteResult(raw json.RawMessage) (*toolRender, error) {
	var out mcp.DeleteOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	r := &toolRender{Title: "🗑 Видалено"}
	if out.Message != "" {
		r.Lines = append(r.Lines, escape(out.Message))
	}
	if out.UID != "" {
		r.Lines = append(r.Lines, "UID: <code>"+escape(out.UID)+"</code>")
	}
	return r, nil
}

func renderGeneric(tool string, raw json.RawMessage) (*toolRender, error) {
	var value any
	_ = json.Unmarshal(raw, &value)
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		pretty = raw
	}
	text := string(pretty)
	if runes := []rune(text); len(runes) > 3000 {
		text = string(runes[:3000]) + "…"
	}
	r := &toolRender{Title: "🧾 " + tool}
	r.Lines = append(r.Lines, "<pre>"+escape(text)+"</pre>")
	return r, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (r *toolRender) page(offset int) ([]toolRow, bool) {
	if r.ServerPaged {
		return r.Rows, r.NextCursor != ""
	}
	start := offset
	if start < 0 {
		start = 0
	}
	if start > len(r.Rows) {
		start = len(r.Rows)
	}
	end := start + toolPageSize
	if end > len(r.Rows) {
		end = len(r.Rows)
	}
	return r.Rows[start:end], end < len(r.Rows)
}

func (r *toolRender) text(offset int) string {
	rows, _ := r.page(offset)
	var b strings.Builder
	if r.Title != "" {
		b.WriteString("<b>" + escape(r.Title) + "</b>\n\n")
	}
	for _, line := range r.Lines {
		b.WriteString(line + "\n")
	}
	if len(r.Lines) > 0 && len(rows) > 0 {
		b.WriteString("\n")
	}
	start := offset + 1
	for i, row := range rows {
		fmt.Fprintf(&b, "%d. %s\n", start+i, row.Text)
	}
	if r.Total > 0 && len(rows) > 0 {
		fmt.Fprintf(&b, "\nПоказано %d–%d із %d", start, start+len(rows)-1, r.Total)
	}
	out := strings.TrimRight(b.String(), "\n")
	if out == "" {
		return "Немає даних."
	}
	return out
}

func (r *toolRender) markup(offset int, pageToken string) models.ReplyMarkup {
	rows, more := r.page(offset)
	var keyboard [][]models.InlineKeyboardButton
	var deleteButtons []models.InlineKeyboardButton
	for i, row := range rows {
		if row.DeleteUID == "" {
			continue
		}
		deleteButtons = append(deleteButtons, models.InlineKeyboardButton{
			Text:         fmt.Sprintf("🗑 %d", offset+i+1),
			CallbackData: "td:" + row.DeleteUID,
		})
	}
	for len(deleteButtons) > 0 {
		n := 5
		if len(deleteButtons) < n {
			n = len(deleteButtons)
		}
		keyboard = append(keyboard, deleteButtons[:n])
		deleteButtons = deleteButtons[n:]
	}
	if more && pageToken != "" {
		keyboard = append(keyboard, []models.InlineKeyboardButton{{
			Text: "Ще ▶", CallbackData: "tp:" + pageToken,
		}})
	}
	if len(keyboard) == 0 {
		return nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: keyboard}
}

func formatMoney(raw, currency string) string {
	if strings.TrimSpace(raw) == "" {
		return "—"
	}
	c, err := money.CurrencyFor(currency)
	if err != nil {
		c = money.Currency{Code: strings.ToUpper(currency), MinorUnits: 2}
	}
	minor, ok := parseDisplayAmount(raw, c.MinorUnits)
	if !ok {
		return escape(raw) + " " + escape(currency)
	}
	rendered := money.FormatScaled(minor, c.MinorUnits)
	if c.Code == "UAH" {
		return rendered + " ₴"
	}
	return rendered + " " + c.Code
}

// parseDisplayAmount parses a signed decimal string for display. Unlike
// money.ParseAmount it accepts zero and negative values, which are needed for
// balances, deltas, and net totals.
func parseDisplayAmount(raw string, scale int) (int64, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "+")
	neg := false
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "−") {
		neg = true
		s = s[1:]
	}
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, false
	}
	intPart := s
	fracPart := ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart = s[:i]
		fracPart = s[i+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > scale {
		fracPart = fracPart[:scale]
	}
	if len(fracPart) < scale {
		fracPart += strings.Repeat("0", scale-len(fracPart))
	}
	digits := intPart + fracPart
	if digits == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

func formatSignedMoney(raw, currency string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "—"
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "−") {
		return formatMoney(s, currency)
	}
	unsigned := strings.TrimPrefix(s, "+")
	if unsigned == "0" || strings.HasPrefix(unsigned, "0.0") {
		return formatMoney(s, currency)
	}
	return "+" + formatMoney(s, currency)
}

func formatDate(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "—"
	}
	if d, err := domain.ParseDate(raw); err == nil {
		return d.Format("02.01.2006")
	}
	return escape(raw)
}

func formatShortDate(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "—"
	}
	if d, err := domain.ParseDate(raw); err == nil {
		return d.Format("02.01")
	}
	return escape(raw)
}

func formatRate(raw string) string {
	scaled, err := money.ParseRate(raw)
	if err != nil {
		return escape(raw)
	}
	return money.FormatRate(scaled)
}
