package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"skarb/internal/domain"
)

func MonthRange(year int, month time.Month) domain.DateRange {
	start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, -1)
	return domain.DateRange{From: domain.DateOf(start), To: domain.DateOf(end)}
}

func ParseMonth(raw string) (domain.DateRange, error) {
	raw = strings.TrimSpace(raw)
	t, err := time.Parse("2006-01", raw)
	if err != nil {
		return domain.DateRange{}, fmt.Errorf("invalid month %q, expected YYYY-MM", raw)
	}
	return MonthRange(t.Year(), t.Month()), nil
}

func ParsePeriod(fromRaw, toRaw string) (domain.DateRange, error) {
	from, err := domain.ParseDate(strings.TrimSpace(fromRaw))
	if err != nil {
		return domain.DateRange{}, err
	}
	to, err := domain.ParseDate(strings.TrimSpace(toRaw))
	if err != nil {
		return domain.DateRange{}, err
	}
	if to.Before(from) {
		return domain.DateRange{}, fmt.Errorf("period end is before start")
	}
	return domain.DateRange{From: from, To: to}, nil
}

func CurrentMonth(today domain.Date) domain.DateRange {
	return MonthRange(today.Year(), today.Month())
}

func PreviousMonth(r domain.DateRange) domain.DateRange {
	first := time.Date(r.From.Year(), r.From.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	return MonthRange(first.Year(), first.Month())
}

func MonthToDate(today domain.Date) domain.DateRange {
	return domain.DateRange{From: domain.NewDate(today.Year(), today.Month(), 1), To: today}
}

func PreviousMonthSameDays(today domain.Date) domain.DateRange {
	prevFirst := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	lastDay := time.Date(prevFirst.Year(), prevFirst.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := today.Day()
	if day > lastDay {
		day = lastDay
	}
	return domain.DateRange{
		From: domain.DateOf(prevFirst),
		To:   domain.NewDate(prevFirst.Year(), prevFirst.Month(), day),
	}
}

func YearRange(today domain.Date) domain.DateRange {
	return domain.DateRange{
		From: domain.NewDate(today.Year(), time.January, 1),
		To:   domain.NewDate(today.Year(), time.December, 31),
	}
}

func ParseYearMonthParts(raw string) (int, time.Month, error) {
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid month %q", raw)
	}
	y, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 1 || m > 12 {
		return 0, 0, fmt.Errorf("invalid month %q", raw)
	}
	return y, time.Month(m), nil
}

func MonthsBetween(from, to domain.Date) []domain.Date {
	var out []domain.Date
	cur := domain.NewDate(from.Year(), from.Month(), 1)
	end := domain.NewDate(to.Year(), to.Month(), 1)
	for !cur.After(end) {
		out = append(out, cur)
		cur = domain.DateOf(cur.Time.AddDate(0, 1, 0))
	}
	return out
}

func MonthEnd(d domain.Date) domain.Date {
	return domain.DateOf(time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC))
}
