package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"skarb/internal/domain"
)

func (s *Store) UpsertRate(ctx context.Context, q DBTX, currency string, date domain.Date, rateScaled int64, source string, updatedBy *int64) error {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || rateScaled <= 0 || date.IsZero() {
		return &domain.ValidationError{Field: "rate", Message: "invalid rate"}
	}
	if source != "nbu" && source != "manual" {
		source = "nbu"
	}
	_, err := s.q(q).ExecContext(ctx,
		`INSERT INTO exchange_rates(currency, rate_date, rate_scaled, rate_scale, source, fetched_at, updated_by_user_id)
		 VALUES (?, ?, ?, 8, ?, ?, ?)
		 ON CONFLICT(currency, rate_date) DO UPDATE SET
		   rate_scaled = excluded.rate_scaled,
		   source = excluded.source,
		   fetched_at = excluded.fetched_at,
		   updated_by_user_id = excluded.updated_by_user_id
		 WHERE exchange_rates.source <> 'manual' OR excluded.source = 'manual'`,
		currency, date.String(), rateScaled, source, Now(), nullInt64(updatedBy),
	)
	return err
}

// UpsertNBURate inserts an NBU rate unless a manual override already exists for the date.
func (s *Store) UpsertNBURate(ctx context.Context, q DBTX, currency string, date domain.Date, rateScaled int64) error {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	_, err := s.q(q).ExecContext(ctx,
		`INSERT INTO exchange_rates(currency, rate_date, rate_scaled, rate_scale, source, fetched_at)
		 VALUES (?, ?, ?, 8, 'nbu', ?)
		 ON CONFLICT(currency, rate_date) DO UPDATE SET
		   rate_scaled = excluded.rate_scaled,
		   fetched_at = excluded.fetched_at
		 WHERE exchange_rates.source <> 'manual'`,
		currency, date.String(), rateScaled, Now(),
	)
	return err
}

func (s *Store) GetRate(ctx context.Context, currency string, date domain.Date) (int64, *domain.Date, error) {
	var scaled int64
	var ds string
	err := s.DB.QueryRowContext(ctx,
		`SELECT rate_scaled, rate_date FROM exchange_rates WHERE currency = ? AND rate_date = ?`,
		strings.ToUpper(currency), date.String(),
	).Scan(&scaled, &ds)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, domain.ErrNotFound
	}
	if err != nil {
		return 0, nil, err
	}
	d, err := domain.ParseDate(ds)
	if err != nil {
		return 0, nil, err
	}
	return scaled, &d, nil
}

// RateForDate returns the rate on the date or the nearest prior date.
func (s *Store) RateForDate(ctx context.Context, currency string, date domain.Date) (int64, domain.Date, bool, error) {
	currency = strings.ToUpper(currency)
	if currency == "UAH" {
		return 100000000, date, true, nil
	}
	var scaled int64
	var ds string
	err := s.DB.QueryRowContext(ctx,
		`SELECT rate_scaled, rate_date FROM exchange_rates
		 WHERE currency = ? AND rate_date <= ?
		 ORDER BY rate_date DESC LIMIT 1`,
		currency, date.String(),
	).Scan(&scaled, &ds)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, domain.Date{}, false, nil
	}
	if err != nil {
		return 0, domain.Date{}, false, err
	}
	d, err := domain.ParseDate(ds)
	if err != nil {
		return 0, domain.Date{}, false, err
	}
	return scaled, d, true, nil
}

func (s *Store) LatestRate(ctx context.Context, currency string) (int64, *domain.Date, error) {
	currency = strings.ToUpper(currency)
	if currency == "UAH" {
		d := domain.DateOf(NowTime())
		return 100000000, &d, nil
	}
	var scaled int64
	var ds string
	err := s.DB.QueryRowContext(ctx,
		`SELECT rate_scaled, rate_date FROM exchange_rates WHERE currency = ?
		 ORDER BY rate_date DESC LIMIT 1`, currency,
	).Scan(&scaled, &ds)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, domain.ErrNotFound
	}
	if err != nil {
		return 0, nil, err
	}
	d, err := domain.ParseDate(ds)
	if err != nil {
		return 0, nil, err
	}
	return scaled, &d, nil
}

func (s *Store) ListRates(ctx context.Context, currency string, from, to *domain.Date) ([]domain.ExchangeRate, error) {
	where := []string{"1=1"}
	args := []any{}
	if currency != "" {
		where = append(where, "currency = ?")
		args = append(args, strings.ToUpper(currency))
	}
	if from != nil {
		where = append(where, "rate_date >= ?")
		args = append(args, from.String())
	}
	if to != nil {
		where = append(where, "rate_date <= ?")
		args = append(args, to.String())
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT currency, rate_date, rate_scaled, source, fetched_at FROM exchange_rates
		 WHERE `+strings.Join(where, " AND ")+` ORDER BY rate_date DESC, currency`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ExchangeRate
	for rows.Next() {
		var r domain.ExchangeRate
		var ds, fetched string
		if err := rows.Scan(&r.Currency, &ds, &r.RateScaled, &r.Source, &fetched); err != nil {
			return nil, err
		}
		if r.RateDate, err = domain.ParseDate(ds); err != nil {
			return nil, err
		}
		if r.FetchedAt, err = scanTime(fetched); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RateCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM exchange_rates`).Scan(&n)
	return n, err
}
