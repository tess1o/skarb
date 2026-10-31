package service

import (
	"context"
	"sort"

	"skarb/internal/domain"
	"skarb/internal/store"
)

// CategoryHistory uses calendar months, clipped to the requested period.
type CategoryHistory struct {
	Months []domain.DateRange
	Rows   []CategoryHistoryRow
	Totals []CategoryMonth
	Total  CategoryMonth
}
type CategoryMonth struct {
	AmountUAH   int64
	MissingRate int
}
type CategoryHistoryRow struct {
	Category domain.Category
	Months   []CategoryMonth
	Total    CategoryMonth
}

func (s *Service) CategoryHistory(ctx context.Context, actor domain.Actor, f store.TransactionFilter, kind domain.Kind) (*CategoryHistory, error) {
	if err := s.RequireAuth(actor); err != nil {
		return nil, err
	}
	if f.From == nil || f.To == nil || f.To.Before(*f.From) {
		return nil, validation("period", "invalid period")
	}
	if kind != domain.KindExpense && kind != domain.KindIncome {
		return nil, validation("kind", "invalid kind")
	}
	to := *f.To
	if to.After(s.Today()) {
		to = s.Today()
	}
	out := &CategoryHistory{}
	if to.Before(*f.From) {
		return out, nil
	}
	indices := map[string]int{}
	for _, m := range MonthsBetween(*f.From, to) {
		from, end := m, MonthEnd(m)
		if from.Before(*f.From) {
			from = *f.From
		}
		if end.After(to) {
			end = to
		}
		indices[m.Format("2006-01")] = len(out.Months)
		out.Months = append(out.Months, domain.DateRange{From: from, To: end})
	}
	out.Totals = make([]CategoryMonth, len(out.Months))
	f = statsFilter(f)
	f.Kind = &kind
	f.To = &to
	transactions, err := s.Store.ListTransactionsForStats(ctx, f)
	if err != nil {
		return nil, err
	}
	categories, err := s.Store.ListCategories(ctx, true)
	if err != nil {
		return nil, err
	}
	byID := map[int64]domain.Category{}
	for _, c := range categories {
		byID[c.ID] = c
	}
	rows := map[int64]*CategoryHistoryRow{}
	conv := s.newConverter(ctx)
	for _, t := range transactions {
		row := rows[t.CategoryID]
		if row == nil {
			row = &CategoryHistoryRow{Category: byID[t.CategoryID], Months: make([]CategoryMonth, len(out.Months))}
			rows[t.CategoryID] = row
		}
		i := indices[t.OccurredOn.Format("2006-01")]
		amount, _, ok, err := conv.convert(t.AmountMinor, t.Currency, t.OccurredOn)
		if err != nil {
			return nil, err
		}
		if !ok {
			row.Months[i].MissingRate++
			row.Total.MissingRate++
			out.Totals[i].MissingRate++
			out.Total.MissingRate++
			continue
		}
		row.Months[i].AmountUAH += amount
		row.Total.AmountUAH += amount
		out.Totals[i].AmountUAH += amount
		out.Total.AmountUAH += amount
	}
	for _, row := range rows {
		out.Rows = append(out.Rows, *row)
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].Total.AmountUAH == out.Rows[j].Total.AmountUAH {
			return out.Rows[i].Category.Name < out.Rows[j].Category.Name
		}
		return out.Rows[i].Total.AmountUAH > out.Rows[j].Total.AmountUAH
	})
	return out, nil
}
