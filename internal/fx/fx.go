package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"skarb/internal/domain"
	"skarb/internal/money"
	"skarb/internal/store"
)

const (
	dailyEndpoint = "https://bank.gov.ua/NBUStatService/v1/statdirectory/exchange"
	rangeEndpoint = "https://bank.gov.ua/NBU_Exchange/exchange_site"
)

type Service struct {
	store  *store.Store
	client *http.Client
	log    *slog.Logger
	loc    *time.Location
	now    func() time.Time
}

func New(st *store.Store, client *http.Client, logger *slog.Logger, loc *time.Location) *Service {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: st, client: client, log: logger, loc: loc, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) { s.now = now }

func (s *Service) Today() domain.Date {
	return domain.TodayIn(s.loc, s.now())
}

// FetchLatest fetches today's NBU rates. NBU may return the most recent
// business day's rates on a weekend; the response date is what gets stored.
func (s *Service) FetchLatest(ctx context.Context) error {
	return s.FetchDay(ctx, s.Today())
}

func (s *Service) FetchDay(ctx context.Context, date domain.Date) error {
	u, _ := url.Parse(dailyEndpoint)
	q := u.Query()
	q.Set("date", date.Time.Format("20060102"))
	q.Set("json", "")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Skarb/1.0 (+self-hosted personal finance)")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("NBU request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("NBU HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var rows []nbuDaily
	if err := dec.Decode(&rows); err != nil {
		return fmt.Errorf("decode NBU daily: %w", err)
	}
	count := 0
	for _, row := range rows {
		if !money.IsSupported(row.CC) {
			continue
		}
		rate, err := money.ParseRate(row.Rate.String())
		if err != nil {
			s.log.Warn("skip invalid NBU rate", "currency", row.CC, "rate", row.Rate.String(), "error", err)
			continue
		}
		d, err := parseNBUDate(row.Exchangedate)
		if err != nil {
			s.log.Warn("skip invalid NBU date", "date", row.Exchangedate, "error", err)
			continue
		}
		if err := s.store.UpsertNBURate(ctx, nil, row.CC, d, rate); err != nil {
			return fmt.Errorf("store NBU rate %s: %w", row.CC, err)
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("NBU returned no supported rates for %s", date)
	}
	return nil
}

func (s *Service) Backfill(ctx context.Context, days int) error {
	if days <= 0 {
		days = 730
	}
	end := s.Today()
	start := end.AddDays(-days)
	u, _ := url.Parse(rangeEndpoint)
	q := u.Query()
	q.Set("start", start.Time.Format("20060102"))
	q.Set("end", end.Time.Format("20060102"))
	q.Set("json", "")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Skarb/1.0 (+self-hosted personal finance)")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("NBU range request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("NBU range HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var rows []nbuRange
	if err := dec.Decode(&rows); err != nil {
		return fmt.Errorf("decode NBU range: %w", err)
	}
	for _, row := range rows {
		if !money.IsSupported(row.CC) {
			continue
		}
		rate, err := rangeRate(row)
		if err != nil {
			s.log.Warn("skip invalid NBU range rate", "currency", row.CC, "error", err)
			continue
		}
		d, err := parseNBUDate(row.Exchangedate)
		if err != nil {
			continue
		}
		if err := s.store.UpsertNBURate(ctx, nil, row.CC, d, rate); err != nil {
			return fmt.Errorf("store backfill rate %s: %w", row.CC, err)
		}
	}
	return nil
}

func rangeRate(row nbuRange) (int64, error) {
	if row.RatePerUnit != "" {
		return money.ParseRate(row.RatePerUnit.String())
	}
	if row.Rate == "" {
		return 0, fmt.Errorf("missing rate")
	}
	scaled, err := money.ParseRate(row.Rate.String())
	if err != nil {
		return 0, err
	}
	units := int64(1)
	if row.Units != "" {
		if v, err := strconv.ParseInt(row.Units.String(), 10, 64); err == nil && v > 0 {
			units = v
		}
	}
	if units == 1 {
		return scaled, nil
	}
	// Divide a scaled rate by units with half-up rounding.
	return (scaled + units/2) / units, nil
}

func (s *Service) RateFor(ctx context.Context, currency string, date domain.Date) (int64, domain.Date, bool, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "UAH"
	}
	if currency == "UAH" {
		return 100000000, date, true, nil
	}
	scaled, rateDate, ok, err := s.store.RateForDate(ctx, currency, date)
	if err != nil {
		return 0, domain.Date{}, false, err
	}
	if ok {
		return scaled, rateDate, true, nil
	}
	// Try to fetch the exact day, then look again.
	if err := s.FetchDay(ctx, date); err != nil {
		s.log.Warn("on-demand NBU fetch failed", "date", date.String(), "error", err)
		return 0, domain.Date{}, false, nil
	}
	scaled, rateDate, ok, err = s.store.RateForDate(ctx, currency, date)
	if err != nil {
		return 0, domain.Date{}, false, err
	}
	return scaled, rateDate, ok, nil
}

func (s *Service) Convert(ctx context.Context, amountMinor int64, currency string, date domain.Date) (int64, domain.Date, bool, error) {
	rate, rateDate, ok, err := s.RateFor(ctx, currency, date)
	if err != nil || !ok {
		return 0, domain.Date{}, ok, err
	}
	uah, err := money.ConvertToUAH(amountMinor, currency, rate)
	if err != nil {
		return 0, domain.Date{}, false, err
	}
	return uah, rateDate, true, nil
}

func (s *Service) ManualOverride(ctx context.Context, currency string, date domain.Date, rate string, userID int64) error {
	scaled, err := money.ParseRate(rate)
	if err != nil {
		return err
	}
	return s.store.UpsertRate(ctx, nil, currency, date, scaled, "manual", &userID)
}

func (s *Service) Latest(ctx context.Context, currency string) (int64, *domain.Date, error) {
	return s.store.LatestRate(ctx, currency)
}

func (s *Service) List(ctx context.Context, currency string, from, to *domain.Date) ([]domain.ExchangeRate, error) {
	return s.store.ListRates(ctx, currency, from, to)
}

func (s *Service) Start(ctx context.Context) {
	// Initial fetch is best-effort; the scheduler handles the daily refresh.
	if err := s.FetchLatest(ctx); err != nil {
		s.log.Warn("initial NBU refresh failed", "error", err)
	}
}

type nbuDaily struct {
	R030         int         `json:"r030"`
	Txt          string      `json:"txt"`
	Rate         json.Number `json:"rate"`
	CC           string      `json:"cc"`
	Exchangedate string      `json:"exchangedate"`
}

type nbuRange struct {
	Exchangedate string      `json:"exchangedate"`
	Rate         json.Number `json:"rate"`
	Units        json.Number `json:"units"`
	RatePerUnit  json.Number `json:"rate_per_unit"`
	CC           string      `json:"cc"`
}

func parseNBUDate(s string) (domain.Date, error) {
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return domain.Date{}, err
	}
	return domain.DateOf(t), nil
}
