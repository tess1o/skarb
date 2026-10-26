package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/store"
)

func (s *Server) addStatsTools(srv *mcpsdk.Server, actor domain.Actor) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "stats_summary",
		Description: "Totals for a period, converted to UAH, plus native-currency totals. Defaults to the current month.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in statsSummaryIn) (*mcpsdk.CallToolResult, summaryOut, error) {
		r, err := s.dateRange(in.From, in.To)
		if err != nil {
			return nil, summaryOut{}, toolError(err)
		}
		f := store.TransactionFilter{From: &r.From, To: &r.To}
		if f.OwnerUserID, err = s.resolveUserFilter(ctx, in.User); err != nil {
			return nil, summaryOut{}, toolError(err)
		}
		if f.CategoryID, err = s.resolveCategoryFilter(ctx, in.Category); err != nil {
			return nil, summaryOut{}, toolError(err)
		}
		sum, err := s.svc.Summary(ctx, actor, f)
		if err != nil {
			return nil, summaryOut{}, toolError(err)
		}
		return nil, summaryToOut(sum), nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "stats_category_breakdown",
		Description: "Category totals for one kind (income or expense), with counts and percentages, converted to UAH.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in statsCategoryIn) (*mcpsdk.CallToolResult, statsCategoryOut, error) {
		r, err := s.dateRange(in.From, in.To)
		if err != nil {
			return nil, statsCategoryOut{}, toolError(err)
		}
		kind, err := kindFromString(in.Kind)
		if err != nil {
			return nil, statsCategoryOut{}, err
		}
		f := store.TransactionFilter{From: &r.From, To: &r.To}
		if f.OwnerUserID, err = s.resolveUserFilter(ctx, in.User); err != nil {
			return nil, statsCategoryOut{}, toolError(err)
		}
		rows, err := s.svc.CategoryBreakdown(ctx, actor, f, kind, in.Limit)
		if err != nil {
			return nil, statsCategoryOut{}, toolError(err)
		}
		out := statsCategoryOut{From: r.From.String(), To: r.To.String(), Rows: make([]categoryTotalOut, 0, len(rows))}
		for _, row := range rows {
			out.Rows = append(out.Rows, categoryTotalOut{
				Category: row.Name, Kind: string(row.Kind),
				Amount: money.PlainAmount(row.AmountUAH, 2), Count: row.Count, Percent: row.Percent,
			})
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "stats_user_breakdown",
		Description: "Income, expenses, and net per user for a period, converted to UAH.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in statsUsersIn) (*mcpsdk.CallToolResult, statsUsersOut, error) {
		r, err := s.dateRange(in.From, in.To)
		if err != nil {
			return nil, statsUsersOut{}, toolError(err)
		}
		rows, err := s.svc.UserBreakdown(ctx, actor, store.TransactionFilter{From: &r.From, To: &r.To})
		if err != nil {
			return nil, statsUsersOut{}, toolError(err)
		}
		out := statsUsersOut{From: r.From.String(), To: r.To.String(), Rows: make([]userTotalOut, 0, len(rows))}
		for _, row := range rows {
			out.Rows = append(out.Rows, userTotalOut{
				User: row.DisplayName, Income: money.PlainAmount(row.IncomeUAH, 2),
				Expenses: money.PlainAmount(row.ExpenseUAH, 2), Net: money.PlainAmount(row.NetUAH, 2), Count: row.Count,
			})
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "stats_compare_periods",
		Description: "Compare two periods. If B is omitted, the previous equal-length period is used. Deltas are A minus B, converted to UAH.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in statsCompareIn) (*mcpsdk.CallToolResult, statsCompareOut, error) {
		a, err := s.dateRange(in.AFrom, in.ATo)
		if err != nil {
			return nil, statsCompareOut{}, toolError(err)
		}
		var b domain.DateRange
		if strings.TrimSpace(in.BFrom) == "" && strings.TrimSpace(in.BTo) == "" {
			days := int(a.To.Time.Sub(a.From.Time).Hours()/24) + 1
			b = domain.DateRange{From: a.From.AddDays(-days), To: a.From.AddDays(-1)}
		} else {
			b, err = s.dateRange(in.BFrom, in.BTo)
			if err != nil {
				return nil, statsCompareOut{}, toolError(err)
			}
		}
		var userID *int64
		if userID, err = s.resolveUserFilter(ctx, in.User); err != nil {
			return nil, statsCompareOut{}, toolError(err)
		}
		comp, err := s.svc.Compare(ctx, actor, a, b, userID)
		if err != nil {
			return nil, statsCompareOut{}, toolError(err)
		}
		out := statsCompareOut{
			A: summaryToOut(&comp.A), B: summaryToOut(&comp.B),
			IncomeDelta:  money.PlainAmount(comp.IncomeDelta, 2),
			ExpenseDelta: money.PlainAmount(comp.ExpenseDelta, 2),
			NetDelta:     money.PlainAmount(comp.NetDelta, 2),
			ByCategory:   make([]categoryDeltaOut, 0, len(comp.ByCategory)),
		}
		for _, row := range comp.ByCategory {
			out.ByCategory = append(out.ByCategory, categoryDeltaOut{
				Category: row.Name, Kind: string(row.Kind),
				Current: money.PlainAmount(row.AUAH, 2), Previous: money.PlainAmount(row.BUAH, 2),
				Delta: money.PlainAmount(row.Delta, 2),
			})
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "stats_trend",
		Description: "Income and expense trend by day or month, converted to UAH.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in statsTrendIn) (*mcpsdk.CallToolResult, statsTrendOut, error) {
		r, err := s.dateRange(in.From, in.To)
		if err != nil {
			return nil, statsTrendOut{}, toolError(err)
		}
		granularity := "day"
		if strings.EqualFold(strings.TrimSpace(in.Granularity), "month") {
			granularity = "month"
		}
		points, err := s.svc.Trend(ctx, actor, r.From, r.To, granularity)
		if err != nil {
			return nil, statsTrendOut{}, toolError(err)
		}
		out := statsTrendOut{Points: make([]trendPointOut, 0, len(points))}
		for _, p := range points {
			out.Points = append(out.Points, trendPointOut{
				Date: p.Date.String(), Label: p.Label,
				Income: money.PlainAmount(p.IncomeUAH, 2), Expenses: money.PlainAmount(p.ExpenseUAH, 2),
			})
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "net_worth",
		Description: "Current net worth from the latest snapshot of each active account, converted to UAH.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in netWorthIn) (*mcpsdk.CallToolResult, netWorthOut, error) {
		asOf, err := parseDateDefault(in.AsOf, s.svc.Today())
		if err != nil {
			return nil, netWorthOut{}, toolError(err)
		}
		nw, err := s.svc.NetWorth(ctx, actor, asOf)
		if err != nil {
			return nil, netWorthOut{}, toolError(err)
		}
		out := netWorthOut{
			AsOf: nw.AsOf.String(), TotalUAH: money.PlainAmount(nw.TotalUAH, 2),
			Accounts: make([]accountBalanceOut, 0, len(nw.Accounts)), Warnings: nw.MissingRate,
		}
		for _, a := range nw.Accounts {
			item := accountBalanceOut{
				Account: a.AccountName, Owner: a.OwnerName, Currency: a.Currency,
				Balance: money.PlainAmount(a.BalanceMinor, money.MinorUnits(a.Currency)),
				HasRate: a.HasRate,
			}
			if a.HasRate {
				item.RateDate = a.RateDate.String()
				item.BalanceUAH = money.PlainAmount(a.BalanceUAH, 2)
			}
			out.Accounts = append(out.Accounts, item)
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "savings_history",
		Description: "Net-worth history with deltas and percent change. Defaults to the last 12 months, monthly.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in savingsHistoryIn) (*mcpsdk.CallToolResult, savingsHistoryOut, error) {
		today := s.svc.Today()
		from, err := parseDateDefault(in.From, today.AddDays(-365))
		if err != nil {
			return nil, savingsHistoryOut{}, toolError(err)
		}
		to, err := parseDateDefault(in.To, today)
		if err != nil {
			return nil, savingsHistoryOut{}, toolError(err)
		}
		granularity := "month"
		if strings.EqualFold(strings.TrimSpace(in.Granularity), "day") {
			granularity = "day"
		}
		points, err := s.svc.SavingsHistory(ctx, actor, from, to, granularity)
		if err != nil {
			return nil, savingsHistoryOut{}, toolError(err)
		}
		out := savingsHistoryOut{Points: make([]savingsPointOut, 0, len(points))}
		for _, p := range points {
			out.Points = append(out.Points, savingsPointOut{
				Date: p.Date.String(), TotalUAH: money.PlainAmount(p.TotalUAH, 2),
				DeltaUAH: money.PlainAmount(p.DeltaUAH, 2), Pct: p.Pct, Missing: p.MissingRate,
			})
		}
		return nil, out, nil
	})
}
