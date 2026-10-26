package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/nlu"
	"skarb/internal/service"
	"skarb/internal/store"
)

func (b *Bot) handleCommand(ctx context.Context, chatID, tgID int64, user *domain.User, actor domain.Actor, text string, msg *models.Message) error {
	args := commandArgs(text)
	cmd := strings.Fields(text)[0]
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	switch cmd {
	case "/stats":
		return b.cmdStats(ctx, chatID, actor, args)
	case "/compare":
		return b.cmdCompare(ctx, chatID, actor, args)
	case "/categories":
		return b.cmdCategories(ctx, chatID, actor, args)
	case "/trend":
		return b.cmdTrend(ctx, chatID, actor, args)
	case "/last":
		return b.cmdLast(ctx, chatID, actor, args)
	case "/undo":
		return b.cmdUndo(ctx, chatID, actor)
	case "/cancel":
		return b.cmdCancel(ctx, chatID, msg, actor)
	case "/snapshot":
		return b.cmdSnapshot(ctx, chatID, user, actor)
	default:
		return b.reply(ctx, chatID, helpText(), nil)
	}
}

func (b *Bot) cmdStats(ctx context.Context, chatID int64, actor domain.Actor, args []string) error {
	month := ""
	if len(args) > 0 {
		month = args[0]
	}
	if month == "" {
		month = b.svc.Today().Format("2006-01")
	}
	sum, r, err := b.svc.MonthlySummary(ctx, actor, month)
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося порахувати: "+escape(err.Error()), nil)
	}
	cats, _ := b.svc.CategoryBreakdown(ctx, actor, store.TransactionFilter{From: &r.From, To: &r.To}, domain.KindExpense, 6)
	return b.reply(ctx, chatID, formatSummary(sum)+"\n\n"+formatCategoryRows(cats, "Витрати за категоріями"), nil)
}

func (b *Bot) cmdCompare(ctx context.Context, chatID int64, actor domain.Actor, args []string) error {
	today := b.svc.Today()
	a, _ := service.ParseMonth(today.Format("2006-01"))
	bRange := service.PreviousMonth(a)
	if len(args) == 1 {
		parsed, err := service.ParseMonth(args[0])
		if err != nil {
			return b.reply(ctx, chatID, "Формат місяця: YYYY-MM", nil)
		}
		a = parsed
		bRange = service.PreviousMonth(a)
	} else if len(args) >= 2 {
		var err error
		a, err = service.ParseMonth(args[0])
		if err != nil {
			return b.reply(ctx, chatID, "Формат місяця: YYYY-MM", nil)
		}
		bRange, err = service.ParseMonth(args[1])
		if err != nil {
			return b.reply(ctx, chatID, "Формат місяця: YYYY-MM", nil)
		}
	}
	comp, err := b.svc.Compare(ctx, actor, a, bRange, nil)
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося порівняти: "+escape(err.Error()), nil)
	}
	return b.reply(ctx, chatID, formatCompare(comp), nil)
}

func (b *Bot) cmdCategories(ctx context.Context, chatID int64, actor domain.Actor, args []string) error {
	month := b.svc.Today().Format("2006-01")
	kind := domain.KindExpense
	for _, arg := range args {
		if arg == "income" || arg == "дохід" || arg == "доходи" {
			kind = domain.KindIncome
			continue
		}
		if arg == "expense" || arg == "витрати" {
			kind = domain.KindExpense
			continue
		}
		if _, err := service.ParseMonth(arg); err == nil {
			month = arg
		}
	}
	r, err := service.ParseMonth(month)
	if err != nil {
		return b.reply(ctx, chatID, "Формат місяця: YYYY-MM", nil)
	}
	rows, err := b.svc.CategoryBreakdown(ctx, actor, store.TransactionFilter{From: &r.From, To: &r.To}, kind, 15)
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося порахувати: "+escape(err.Error()), nil)
	}
	title := "Витрати за категоріями"
	if kind == domain.KindIncome {
		title = "Доходи за категоріями"
	}
	return b.reply(ctx, chatID, fmt.Sprintf("📂 <b>%s</b> · %s\n\n%s", title, month, formatCategoryRows(rows, title)), nil)
}

func (b *Bot) cmdTrend(ctx context.Context, chatID int64, actor domain.Actor, args []string) error {
	n := 6
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v >= 1 && v <= 24 {
			n = v
		}
	}
	today := b.svc.Today()
	from := domain.NewDate(today.Year(), today.Month(), 1)
	for i := 1; i < n; i++ {
		from = domain.DateOf(from.Time.AddDate(0, -1, 0))
	}
	points, err := b.svc.Trend(ctx, actor, from, today, "month")
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося порахувати: "+escape(err.Error()), nil)
	}
	return b.reply(ctx, chatID, formatTrend(points), nil)
}

func (b *Bot) cmdLast(ctx context.Context, chatID int64, actor domain.Actor, args []string) error {
	n := 10
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v >= 1 && v <= 30 {
			n = v
		}
	}
	rows, _, err := b.svc.ListTransactions(ctx, actor, store.TransactionFilter{Limit: n})
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося завантажити: "+escape(err.Error()), nil)
	}
	return b.reply(ctx, chatID, formatTransactions(rows), nil)
}

func (b *Bot) cmdUndo(ctx context.Context, chatID int64, actor domain.Actor) error {
	tx, err := b.svc.LatestTransactionByOwner(ctx, actor)
	if err != nil {
		return b.reply(ctx, chatID, "Немає активної транзакції для скасування.", nil)
	}
	restored, err := b.svc.UndoTransaction(ctx, actor, tx.UID)
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося скасувати: "+escape(err.Error()), nil)
	}
	if restored != nil {
		return b.reply(ctx, chatID, "↩ Відновлено попередню версію: "+escape(restored.CategoryName)+" "+money.FormatAmount(restored.AmountMinor, restored.Currency), nil)
	}
	return b.reply(ctx, chatID, "↩ Скасовано: "+escape(tx.CategoryName)+" "+money.FormatAmount(tx.AmountMinor, tx.Currency), nil)
}

func (b *Bot) cmdCancel(ctx context.Context, chatID int64, msg *models.Message, actor domain.Actor) error {
	if msg.ReplyToMessage == nil {
		return b.reply(ctx, chatID, "Дайте відповідь на запит коментаря командою /cancel.", nil)
	}
	pending, err := b.svc.Store.GetPendingEdit(ctx, chatID, int64(msg.ReplyToMessage.ID))
	if err != nil || pending.ActorTelegramUserID != actor.TelegramUserID {
		return b.reply(ctx, chatID, "Активного запиту коментаря не знайдено.", nil)
	}
	_ = b.svc.Store.DeletePendingEdit(ctx, chatID, int64(msg.ReplyToMessage.ID))
	_ = b.reply(ctx, chatID, "Коментар скасовано.", nil)
	return nil
}

func (b *Bot) cmdSnapshot(ctx context.Context, chatID int64, user *domain.User, actor domain.Actor) error {
	if !user.IsAdmin() {
		return b.reply(ctx, chatID, "Оновлювати заощадження може лише адміністратор.", nil)
	}
	accounts, err := b.svc.ListAccounts(ctx, actor, false)
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося завантажити рахунки.", nil)
	}
	return b.reply(ctx, chatID, "💰 <b>Заощадження</b>\nНапишіть, наприклад:\n<code>5000 USD bank2 для дружини</code>\nабо <code>12000 грн готівка</code>\n\n"+formatAccounts(accounts), nil)
}

func (b *Bot) handleStatsIntent(ctx context.Context, chatID int64, actor domain.Actor, intent *nlu.Intent, text string) error {
	q := intent.Query
	if q == nil {
		return b.reply(ctx, chatID, helpText(), nil)
	}
	r, err := b.queryRange(q)
	if err != nil {
		return b.reply(ctx, chatID, "Не вдалося розібрати період: "+escape(err.Error()), nil)
	}
	f := store.TransactionFilter{From: &r.From, To: &r.To}
	if q.Category != nil && strings.TrimSpace(*q.Category) != "" {
		cat, err := b.svc.Store.ResolveCategory(ctx, *q.Category, nil)
		if err != nil {
			return b.reply(ctx, chatID, "Категорію не знайдено.", nil)
		}
		f.CategoryID = &cat.ID
	}
	if q.User != nil && strings.TrimSpace(*q.User) != "" {
		u, err := b.resolveUserHint(ctx, *q.User)
		if err != nil {
			return b.reply(ctx, chatID, "Користувача не знайдено.", nil)
		}
		f.OwnerUserID = &u.ID
	}
	metric := "summary"
	if q.Metric != nil && *q.Metric != "" {
		metric = *q.Metric
	}
	switch metric {
	case "categories":
		kind := kindFromText(text)
		rows, err := b.svc.CategoryBreakdown(ctx, actor, f, kind, 15)
		if err != nil {
			return b.reply(ctx, chatID, "Не вдалося порахувати: "+escape(err.Error()), nil)
		}
		title := "Витрати за категоріями"
		if kind == domain.KindIncome {
			title = "Доходи за категоріями"
		}
		return b.reply(ctx, chatID, fmt.Sprintf("📂 <b>%s</b>\n\n%s", title, formatCategoryRows(rows, title)), nil)
	case "compare":
		days := int(r.To.Time.Sub(r.From.Time).Hours()/24) + 1
		prev := domain.DateRange{From: r.From.AddDays(-days), To: r.From.AddDays(-1)}
		comp, err := b.svc.Compare(ctx, actor, r, prev, nil)
		if err != nil {
			return b.reply(ctx, chatID, "Не вдалося порівняти: "+escape(err.Error()), nil)
		}
		return b.reply(ctx, chatID, formatCompare(comp), nil)
	case "trend":
		points, err := b.svc.Trend(ctx, actor, r.From, r.To, "month")
		if err != nil {
			return b.reply(ctx, chatID, "Не вдалося порахувати: "+escape(err.Error()), nil)
		}
		return b.reply(ctx, chatID, formatTrend(points), nil)
	case "list":
		limit := 10
		if q.Limit != nil && *q.Limit > 0 && *q.Limit <= 30 {
			limit = *q.Limit
		}
		f.Limit = limit
		rows, _, err := b.svc.ListTransactions(ctx, actor, f)
		if err != nil {
			return b.reply(ctx, chatID, "Не вдалося завантажити: "+escape(err.Error()), nil)
		}
		return b.reply(ctx, chatID, formatTransactions(rows), nil)
	default:
		sum, err := b.svc.Summary(ctx, actor, f)
		if err != nil {
			return b.reply(ctx, chatID, "Не вдалося порахувати: "+escape(err.Error()), nil)
		}
		cats, _ := b.svc.CategoryBreakdown(ctx, actor, f, domain.KindExpense, 6)
		return b.reply(ctx, chatID, formatSummary(sum)+"\n\n"+formatCategoryRows(cats, "Витрати за категоріями"), nil)
	}
}

func (b *Bot) queryRange(q *nlu.StatsQuery) (domain.DateRange, error) {
	today := b.svc.Today()
	var from, to domain.Date
	if q.From != nil && strings.TrimSpace(*q.From) != "" {
		d, err := domain.ParseDate(*q.From)
		if err != nil {
			return domain.DateRange{}, err
		}
		from = d
	}
	if q.To != nil && strings.TrimSpace(*q.To) != "" {
		d, err := domain.ParseDate(*q.To)
		if err != nil {
			return domain.DateRange{}, err
		}
		to = d
	}
	if from.IsZero() && to.IsZero() {
		return service.CurrentMonth(today), nil
	}
	if from.IsZero() {
		from = to.AddDays(-30)
	}
	if to.IsZero() {
		to = today
	}
	if to.Before(from) {
		return domain.DateRange{}, fmt.Errorf("end is before start")
	}
	return domain.DateRange{From: from, To: to}, nil
}

func (b *Bot) handleSnapshotIntent(ctx context.Context, chatID int64, user *domain.User, actor domain.Actor, intent *nlu.Intent) error {
	if !user.IsAdmin() {
		return b.reply(ctx, chatID, "Оновлювати заощадження може лише адміністратор.", nil)
	}
	var lines []string
	for _, s := range intent.Snapshot {
		ownerHint := ""
		if s.User != nil {
			ownerHint = *s.User
		}
		account, err := b.svc.ResolveAccount(ctx, s.Account, ownerHint)
		if err != nil {
			lines = append(lines, "⚠️ Рахунок «"+escape(s.Account)+"» не знайдено.")
			continue
		}
		if s.Currency != "" && s.Currency != account.Currency {
			lines = append(lines, "⚠️ Валюта "+escape(s.Currency)+" не збігається з рахунком "+escape(account.Name)+" ("+escape(account.Currency)+").")
			continue
		}
		if _, err := b.svc.UpsertSnapshot(ctx, actor, service.UpsertSnapshotInput{
			AccountUID: account.UID, Date: s.Date, Amount: s.Amount,
		}); err != nil {
			lines = append(lines, "⚠️ "+escape(account.Name)+": "+escape(err.Error()))
			continue
		}
		lines = append(lines, "✅ "+escape(account.Name)+" ("+escape(account.OwnerName)+"): "+escape(s.Amount)+" "+escape(account.Currency)+" на "+escape(s.Date))
	}
	if len(lines) == 0 {
		return b.reply(ctx, chatID, "Не зрозумів залишок.", nil)
	}
	return b.reply(ctx, chatID, "💰 <b>Заощадження</b>\n"+strings.Join(lines, "\n"), nil)
}

func (b *Bot) resolveUserHint(ctx context.Context, hint string) (*domain.User, error) {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return nil, domain.ErrNotFound
	}
	if u, err := b.svc.Store.GetUserByUID(ctx, hint); err == nil {
		return u, nil
	}
	if u, err := b.svc.Store.GetUserByUsername(ctx, hint); err == nil {
		return u, nil
	}
	users, err := b.svc.Store.ListUsers(ctx, false)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(hint)
	for i := range users {
		if strings.ToLower(users[i].DisplayName) == lower || strings.Contains(strings.ToLower(users[i].DisplayName), lower) {
			return &users[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func kindFromText(text string) domain.Kind {
	lower := strings.ToLower(text)
	for _, token := range []string{"дохід", "доходи", "income", "зарплат", "отрима"} {
		if strings.Contains(lower, token) {
			return domain.KindIncome
		}
	}
	return domain.KindExpense
}

func helpText() string {
	return `💸 <b>Skarb</b> — сімейний бюджет

Приклади:
• <code>сьогодні 512 грн за їжу, 300 грн за каву</code>
• <code>вчора 20 USD кава</code>
• <code>5000 грн зарплата</code>

У приватному чаті або з @згадкою/reply бот розуміє складні запити:
• <code>покажи всі транзакції за жовтень</code>
• <code>скільки витратили за жовтень</code>
• <code>видали останню каву</code>
• <code>створи категорію Подарунки</code> (адмін)

Команди:
/stats [YYYY-MM] — підсумок місяця
/compare [YYYY-MM] [YYYY-MM] — порівняння
/categories [YYYY-MM] — за категоріями
/trend [N] — динаміка за N місяців
/last [N] — останні транзакції
/undo — скасувати останню
/id — chat/user ID
/snapshot — заощадження (адмін)

Після запису кожна транзакція має кнопки «Скасувати» та «Коментар».
Руйнівні дії бот спершу показує з кнопками підтвердження.`
}

func formatSummary(s *domain.Summary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📊 <b>%s — %s</b>\n", s.From.Format("02.01"), s.To.Format("02.01"))
	fmt.Fprintf(&b, "Доходи: <b>+%s</b>\n", money.FormatAmount(s.IncomeUAH, "UAH"))
	fmt.Fprintf(&b, "Витрати: <b>−%s</b>\n", money.FormatAmount(s.ExpenseUAH, "UAH"))
	fmt.Fprintf(&b, "Баланс: <b>%s</b>\n", money.FormatAmount(s.NetUAH, "UAH"))
	fmt.Fprintf(&b, "Операцій: %d", s.Count)
	if s.MissingRate > 0 {
		fmt.Fprintf(&b, "\n⚠️ Без курсу: %d", s.MissingRate)
	}
	return b.String()
}

func formatCategoryRows(rows []domain.CategoryTotal, title string) string {
	if len(rows) == 0 {
		return title + ": немає даних."
	}
	var b strings.Builder
	b.WriteString("<b>" + escape(title) + "</b>\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "• %s %s — %s (%s%%)\n", escape(row.Emoji), escape(row.Name),
			money.FormatAmount(row.AmountUAH, "UAH"), fmt.Sprintf("%.1f", row.Percent))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatCompare(c *domain.Comparison) string {
	var b strings.Builder
	b.WriteString("📊 <b>Порівняння</b>\n")
	fmt.Fprintf(&b, "Зараз: +%s / −%s\n", money.FormatAmount(c.A.IncomeUAH, "UAH"), money.FormatAmount(c.A.ExpenseUAH, "UAH"))
	fmt.Fprintf(&b, "Раніше: +%s / −%s\n", money.FormatAmount(c.B.IncomeUAH, "UAH"), money.FormatAmount(c.B.ExpenseUAH, "UAH"))
	fmt.Fprintf(&b, "Δ доходи: %s\n", money.FormatAmount(c.IncomeDelta, "UAH"))
	fmt.Fprintf(&b, "Δ витрати: %s\n", money.FormatAmount(c.ExpenseDelta, "UAH"))
	fmt.Fprintf(&b, "Δ баланс: %s", money.FormatAmount(c.NetDelta, "UAH"))
	if len(c.ByCategory) > 0 {
		b.WriteString("\n\n<b>Найбільші зміни</b>\n")
		limit := len(c.ByCategory)
		if limit > 6 {
			limit = 6
		}
		for _, row := range c.ByCategory[:limit] {
			fmt.Fprintf(&b, "• %s: %s\n", escape(row.Name), money.FormatAmount(row.Delta, "UAH"))
		}
	}
	return b.String()
}

func formatTrend(points []domain.TrendPoint) string {
	if len(points) == 0 {
		return "📈 Немає даних."
	}
	var b strings.Builder
	b.WriteString("📈 <b>Динаміка</b>\n")
	for _, p := range points {
		fmt.Fprintf(&b, "• %s: +%s / −%s\n", escape(p.Label),
			money.FormatAmount(p.IncomeUAH, "UAH"), money.FormatAmount(p.ExpenseUAH, "UAH"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatTransactions(rows []domain.Transaction) string {
	if len(rows) == 0 {
		return "🧾 Транзакцій немає."
	}
	var b strings.Builder
	b.WriteString("🧾 <b>Останні транзакції</b>\n")
	for _, tx := range rows {
		sign := "−"
		if tx.Kind == domain.KindIncome {
			sign = "+"
		}
		fmt.Fprintf(&b, "• %s · %s %s — %s%s · %s\n",
			tx.OccurredOn.Format("02.01"), escape(tx.CategoryEmoji), escape(tx.CategoryName),
			sign, money.FormatAmount(tx.AmountMinor, tx.Currency), escape(tx.OwnerName))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatAccounts(accounts []domain.Account) string {
	if len(accounts) == 0 {
		return "Рахунків ще немає. Створіть їх у веб-інтерфейсі."
	}
	var b strings.Builder
	b.WriteString("<b>Рахунки</b>\n")
	for _, a := range accounts {
		fmt.Fprintf(&b, "• %s · %s · %s\n", escape(a.Name), escape(a.OwnerName), escape(a.Currency))
	}
	return strings.TrimRight(b.String(), "\n")
}
