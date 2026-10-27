package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/store"
)

type converter struct {
	svc   *Service
	ctx   context.Context
	cache map[string]convResult
}

type convResult struct {
	rate     int64
	rateDate domain.Date
	ok       bool
	err      error
}

func (s *Service) newConverter(ctx context.Context) *converter {
	return &converter{svc: s, ctx: ctx, cache: map[string]convResult{}}
}

func (c *converter) convert(amountMinor int64, currency string, date domain.Date) (int64, domain.Date, bool, error) {
	key := strings.ToUpper(currency) + "|" + date.String()
	v, cached := c.cache[key]
	if !cached {
		rate, rateDate, ok, err := c.svc.FX.RateFor(c.ctx, currency, date)
		v = convResult{rate: rate, rateDate: rateDate, ok: ok, err: err}
		c.cache[key] = v
	}
	if v.err != nil {
		return 0, v.rateDate, false, v.err
	}
	if !v.ok {
		return 0, v.rateDate, false, nil
	}
	uah, err := money.ConvertToUAH(amountMinor, currency, v.rate)
	return uah, v.rateDate, err == nil, err
}

func statsFilter(f store.TransactionFilter) store.TransactionFilter {
	f.IncludeDeleted = false
	f.Limit = -1
	f.Offset = 0
	return f
}

func (s *Service) Summary(ctx context.Context, actor domain.Actor, f store.TransactionFilter) (*domain.Summary, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	f = statsFilter(f)
	rows, err := s.Store.ListTransactionsForStats(ctx, f)
	if err != nil {
		return nil, err
	}
	conv := s.newConverter(ctx)
	out := &domain.Summary{ByCurrency: map[string]domain.CurrencyTotal{}}
	if f.From != nil {
		out.From = *f.From
	}
	if f.To != nil {
		out.To = *f.To
	}
	for _, t := range rows {
		ct := out.ByCurrency[t.Currency]
		ct.Currency = t.Currency
		ct.Count++
		if t.Kind == domain.KindIncome {
			ct.Income += t.AmountMinor
		} else {
			ct.Expense += t.AmountMinor
		}
		out.ByCurrency[t.Currency] = ct
		out.Count++

		uah, _, ok, err := conv.convert(t.AmountMinor, t.Currency, t.OccurredOn)
		if err != nil {
			return nil, err
		}
		if !ok {
			out.MissingRate++
			continue
		}
		if t.Kind == domain.KindIncome {
			out.IncomeUAH += uah
		} else {
			out.ExpenseUAH += uah
		}
	}
	out.NetUAH = out.IncomeUAH - out.ExpenseUAH
	return out, nil
}

func (s *Service) CategoryBreakdown(ctx context.Context, actor domain.Actor, f store.TransactionFilter, kind domain.Kind, limit int) ([]domain.CategoryTotal, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if kind != domain.KindIncome && kind != domain.KindExpense {
		return nil, validation("kind", "kind must be income or expense")
	}
	f = statsFilter(f)
	f.Kind = &kind
	rows, err := s.Store.ListTransactionsForStats(ctx, f)
	if err != nil {
		return nil, err
	}
	conv := s.newConverter(ctx)
	byID := map[int64]*domain.CategoryTotal{}
	var total int64
	for _, t := range rows {
		uah, _, ok, err := conv.convert(t.AmountMinor, t.Currency, t.OccurredOn)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		row := byID[t.CategoryID]
		if row == nil {
			row = &domain.CategoryTotal{
				CategoryID: t.CategoryID,
				Name:       t.CategoryName,
				Emoji:      t.CategoryEmoji,
				Color:      t.CategoryColor,
				Kind:       kind,
			}
			byID[t.CategoryID] = row
		}
		row.AmountUAH += uah
		row.Count++
		total += uah
	}
	out := make([]domain.CategoryTotal, 0, len(byID))
	for _, row := range byID {
		if total > 0 {
			row.Percent = float64(row.AmountUAH) / float64(total) * 100
		}
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AmountUAH == out[j].AmountUAH {
			return out[i].Name < out[j].Name
		}
		return out[i].AmountUAH > out[j].AmountUAH
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Service) UserBreakdown(ctx context.Context, actor domain.Actor, f store.TransactionFilter) ([]domain.UserTotal, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	f = statsFilter(f)
	rows, err := s.Store.ListTransactionsForStats(ctx, f)
	if err != nil {
		return nil, err
	}
	conv := s.newConverter(ctx)
	byID := map[int64]*domain.UserTotal{}
	for _, t := range rows {
		uah, _, ok, err := conv.convert(t.AmountMinor, t.Currency, t.OccurredOn)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		row := byID[t.OwnerUserID]
		if row == nil {
			row = &domain.UserTotal{UserID: t.OwnerUserID, DisplayName: t.OwnerName}
			byID[t.OwnerUserID] = row
		}
		row.Count++
		if t.Kind == domain.KindIncome {
			row.IncomeUAH += uah
		} else {
			row.ExpenseUAH += uah
		}
	}
	out := make([]domain.UserTotal, 0, len(byID))
	for _, row := range byID {
		row.NetUAH = row.IncomeUAH - row.ExpenseUAH
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DisplayName < out[j].DisplayName
	})
	return out, nil
}

func (s *Service) Compare(ctx context.Context, actor domain.Actor, a, b domain.DateRange, userID *int64) (*domain.Comparison, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	fa := store.TransactionFilter{From: &a.From, To: &a.To, OwnerUserID: userID}
	fb := store.TransactionFilter{From: &b.From, To: &b.To, OwnerUserID: userID}
	sumA, err := s.Summary(ctx, actor, fa)
	if err != nil {
		return nil, err
	}
	sumB, err := s.Summary(ctx, actor, fb)
	if err != nil {
		return nil, err
	}
	comp := &domain.Comparison{
		A:            *sumA,
		B:            *sumB,
		IncomeDelta:  sumA.IncomeUAH - sumB.IncomeUAH,
		ExpenseDelta: sumA.ExpenseUAH - sumB.ExpenseUAH,
		NetDelta:     sumA.NetUAH - sumB.NetUAH,
	}
	conv := s.newConverter(ctx)
	type acc struct {
		name string
		kind domain.Kind
		a    int64
		b    int64
	}
	byCat := map[int64]*acc{}
	add := func(f store.TransactionFilter, side string) error {
		f = statsFilter(f)
		rows, err := s.Store.ListTransactionsForStats(ctx, f)
		if err != nil {
			return err
		}
		for _, t := range rows {
			uah, _, ok, err := conv.convert(t.AmountMinor, t.Currency, t.OccurredOn)
			if err != nil || !ok {
				continue
			}
			x := byCat[t.CategoryID]
			if x == nil {
				x = &acc{name: t.CategoryName, kind: t.Kind}
				byCat[t.CategoryID] = x
			}
			if side == "a" {
				x.a += uah
			} else {
				x.b += uah
			}
		}
		return nil
	}
	if err := add(fa, "a"); err != nil {
		return nil, err
	}
	if err := add(fb, "b"); err != nil {
		return nil, err
	}
	for id, x := range byCat {
		comp.ByCategory = append(comp.ByCategory, domain.CategoryComparison{
			CategoryID: id, Name: x.name, Kind: x.kind, AUAH: x.a, BUAH: x.b, Delta: x.a - x.b,
		})
	}
	sort.Slice(comp.ByCategory, func(i, j int) bool {
		return abs64(comp.ByCategory[i].Delta) > abs64(comp.ByCategory[j].Delta)
	})
	return comp, nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func (s *Service) Trend(ctx context.Context, actor domain.Actor, from, to domain.Date, granularity string) ([]domain.TrendPoint, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if to.Before(from) {
		return nil, validation("period", "end is before start")
	}
	rows, err := s.Store.ListTransactionsForStats(ctx, store.TransactionFilter{From: &from, To: &to})
	if err != nil {
		return nil, err
	}
	conv := s.newConverter(ctx)
	type trendBucket struct {
		income  int64
		expense int64
	}
	buckets := map[string]*trendBucket{}
	key := func(d domain.Date) string {
		if granularity == "month" {
			return d.Format("2006-01")
		}
		return d.String()
	}
	for _, t := range rows {
		uah, _, ok, err := conv.convert(t.AmountMinor, t.Currency, t.OccurredOn)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		k := key(t.OccurredOn)
		bucket := buckets[k]
		if bucket == nil {
			bucket = &trendBucket{}
			buckets[k] = bucket
		}
		if t.Kind == domain.KindIncome {
			bucket.income += uah
		} else {
			bucket.expense += uah
		}
	}
	var out []domain.TrendPoint
	if granularity == "month" {
		for _, m := range MonthsBetween(from, to) {
			k := m.Format("2006-01")
			b := buckets[k]
			if b == nil {
				b = &trendBucket{}
			}
			out = append(out, domain.TrendPoint{Date: m, Label: m.Format("01.2006"), IncomeUAH: b.income, ExpenseUAH: b.expense})
		}
	} else {
		days := int(to.Time.Sub(from.Time).Hours()/24) + 1
		if days > 366 {
			return nil, validation("period", "daily trend is limited to 366 days")
		}
		for d := from; !d.After(to); d = d.AddDays(1) {
			b := buckets[d.String()]
			if b == nil {
				b = &trendBucket{}
			}
			out = append(out, domain.TrendPoint{Date: d, Label: d.Format("02.01"), IncomeUAH: b.income, ExpenseUAH: b.expense})
		}
	}
	return out, nil
}

func (s *Service) NetWorth(ctx context.Context, actor domain.Actor, asOf domain.Date) (*domain.NetWorth, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if asOf.IsZero() {
		asOf = s.Today()
	}
	snaps, err := s.Store.LatestSnapshots(ctx, asOf)
	if err != nil {
		return nil, err
	}
	conv := s.newConverter(ctx)
	out := &domain.NetWorth{AsOf: asOf}
	for _, item := range snaps {
		uah, rateDate, ok, err := conv.convert(item.Snapshot.BalanceMinor, item.Account.Currency, asOf)
		if err != nil {
			return nil, err
		}
		bal := domain.AccountBalance{
			AccountID:    item.Account.ID,
			AccountUID:   item.Account.UID,
			AccountName:  item.Account.Name,
			OwnerName:    item.Account.OwnerName,
			Currency:     item.Account.Currency,
			BalanceMinor: item.Snapshot.BalanceMinor,
			RateDate:     rateDate,
			BalanceUAH:   uah,
			HasRate:      ok,
		}
		out.Accounts = append(out.Accounts, bal)
		if ok {
			out.TotalUAH += uah
		} else {
			out.MissingRate = append(out.MissingRate, fmt.Sprintf("%s: немає курсу на %s", item.Account.Currency, asOf.String()))
		}
	}
	return out, nil
}

func (s *Service) SavingsHistory(ctx context.Context, actor domain.Actor, from, to domain.Date, granularity string) ([]domain.SavingsPoint, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if to.Before(from) {
		return nil, validation("period", "end is before start")
	}
	var dates []domain.Date
	if granularity == "day" {
		days := int(to.Time.Sub(from.Time).Hours()/24) + 1
		if days > 366 {
			return nil, validation("period", "daily history is limited to 366 days")
		}
		for d := from; !d.After(to); d = d.AddDays(1) {
			dates = append(dates, d)
		}
	} else {
		for _, m := range MonthsBetween(from, to) {
			end := MonthEnd(m)
			if end.After(to) {
				end = to
			}
			dates = append(dates, end)
		}
	}
	var out []domain.SavingsPoint
	var prev int64
	first := true
	for _, d := range dates {
		nw, err := s.NetWorth(ctx, actor, d)
		if err != nil {
			return nil, err
		}
		point := domain.SavingsPoint{Date: d, TotalUAH: nw.TotalUAH, MissingRate: len(nw.MissingRate)}
		if !first && prev != 0 {
			point.DeltaUAH = nw.TotalUAH - prev
			point.Pct = float64(point.DeltaUAH) / float64(prev) * 100
		} else if !first {
			point.DeltaUAH = nw.TotalUAH - prev
		}
		first = false
		prev = nw.TotalUAH
		out = append(out, point)
	}
	return out, nil
}

func (s *Service) MonthlySummary(ctx context.Context, actor domain.Actor, month string) (*domain.Summary, domain.DateRange, error) {
	r, err := ParseMonth(month)
	if err != nil {
		return nil, domain.DateRange{}, err
	}
	sum, err := s.Summary(ctx, actor, store.TransactionFilter{From: &r.From, To: &r.To})
	return sum, r, err
}

type DashboardData struct {
	Today       domain.Date
	Month       string // YYYY-MM being shown
	PrevMonth   string // YYYY-MM of the month before
	NextMonth   string // YYYY-MM of the month after; empty when Month is the current month
	IsCurrent   bool
	Period      domain.DateRange
	Previous    domain.DateRange
	Summary     *domain.Summary
	PrevSummary *domain.Summary
	Categories  []domain.CategoryTotal
	Trend       []domain.TrendPoint
	TrendMax    int64
	NetWorth    *domain.NetWorth
	Recent      []domain.Transaction
}

func (s *Service) Dashboard(ctx context.Context, actor domain.Actor) (*DashboardData, error) {
	return s.DashboardMonth(ctx, actor, "")
}

// DashboardMonth builds the overview for month (YYYY-MM). An empty, invalid or
// future month shows the current month to date, compared with the same days of
// the previous month; a past month is shown in full against the full month before.
func (s *Service) DashboardMonth(ctx context.Context, actor domain.Actor, month string) (*DashboardData, error) {
	today := s.Today()
	mtd := MonthToDate(today)
	prev := PreviousMonthSameDays(today)
	current := true
	if r, err := ParseMonth(month); err == nil && r.To.Before(CurrentMonth(today).From) {
		mtd, prev, current = r, PreviousMonth(r), false
	}
	cur, err := s.Summary(ctx, actor, store.TransactionFilter{From: &mtd.From, To: &mtd.To})
	if err != nil {
		return nil, err
	}
	prevSum, err := s.Summary(ctx, actor, store.TransactionFilter{From: &prev.From, To: &prev.To})
	if err != nil {
		return nil, err
	}
	cats, err := s.CategoryBreakdown(ctx, actor, store.TransactionFilter{From: &mtd.From, To: &mtd.To}, domain.KindExpense, 7)
	if err != nil {
		return nil, err
	}
	trend, err := s.Trend(ctx, actor, mtd.From, mtd.To, "day")
	if err != nil {
		return nil, err
	}
	nw, err := s.NetWorth(ctx, actor, mtd.To)
	if err != nil {
		return nil, err
	}
	var recent []domain.Transaction
	if current {
		recent, err = s.RecentTransactions(ctx, actor, 10)
	} else {
		recent, err = s.Store.ListTransactions(ctx, store.TransactionFilter{From: &mtd.From, To: &mtd.To, Limit: 10})
	}
	if err != nil {
		return nil, err
	}
	var trendMax int64
	for _, p := range trend {
		if p.ExpenseUAH > trendMax {
			trendMax = p.ExpenseUAH
		}
		if p.IncomeUAH > trendMax {
			trendMax = p.IncomeUAH
		}
	}
	next := ""
	if !current {
		next = MonthRange(mtd.From.Year(), mtd.From.Month()+1).From.Format("2006-01")
	}
	return &DashboardData{
		Today:       today,
		Month:       mtd.From.Format("2006-01"),
		PrevMonth:   prev.From.Format("2006-01"),
		NextMonth:   next,
		IsCurrent:   current,
		Period:      mtd,
		Previous:    prev,
		Summary:     cur,
		PrevSummary: prevSum,
		Categories:  cats,
		Trend:       trend,
		TrendMax:    trendMax,
		NetWorth:    nw,
		Recent:      recent,
	}, nil
}
