package fx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"skarb/internal/domain"
	"skarb/internal/store"
)

// fakeNBU answers every request with status and body and counts the calls.
type fakeNBU struct {
	status int
	body   string
	calls  atomic.Int32
}

func (f *fakeNBU) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Request:    r,
	}, nil
}

func newTestFX(t *testing.T, nbu *fakeNBU) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, &http.Client{Transport: nbu}, logger, time.UTC), st
}

func TestRateForUAHNeedsNoLookup(t *testing.T) {
	nbu := &fakeNBU{status: http.StatusInternalServerError}
	svc, _ := newTestFX(t, nbu)
	d := domain.NewDate(2026, 10, 26)
	rate, rateDate, ok, err := svc.RateFor(context.Background(), "uah", d)
	if err != nil || !ok || rate != 100000000 || !rateDate.Equal(d) {
		t.Fatalf("UAH rate: %d %v %v %v", rate, rateDate, ok, err)
	}
	if nbu.calls.Load() != 0 {
		t.Fatalf("UAH lookup called NBU %d times", nbu.calls.Load())
	}
}

func TestRateForUsesStoredRateOnOrBeforeDate(t *testing.T) {
	ctx := context.Background()
	nbu := &fakeNBU{status: http.StatusInternalServerError}
	svc, st := newTestFX(t, nbu)
	friday := domain.NewDate(2026, 10, 23)
	if err := st.UpsertNBURate(ctx, nil, "USD", friday, 4150000000); err != nil {
		t.Fatal(err)
	}
	// A Sunday transaction uses Friday's rate, without asking NBU.
	sunday := friday.AddDays(2)
	rate, rateDate, ok, err := svc.RateFor(ctx, "USD", sunday)
	if err != nil || !ok || rate != 4150000000 || !rateDate.Equal(friday) {
		t.Fatalf("weekend rate: %d %v %v %v", rate, rateDate, ok, err)
	}
	if nbu.calls.Load() != 0 {
		t.Fatalf("stored rate still called NBU %d times", nbu.calls.Load())
	}

	uah, _, ok, err := svc.Convert(ctx, 10000, "USD", sunday) // $100.00
	if err != nil || !ok || uah != 415000 {
		t.Fatalf("convert $100 at 41.50: %d %v %v", uah, ok, err)
	}
}

func TestRateForFetchesMissingDay(t *testing.T) {
	ctx := context.Background()
	nbu := &fakeNBU{status: http.StatusOK, body: `[
		{"r030":840,"txt":"Долар США","rate":41.5,"cc":"USD","exchangedate":"26.10.2026"},
		{"r030":978,"txt":"Євро","rate":45.05,"cc":"EUR","exchangedate":"26.10.2026"}
	]`}
	svc, st := newTestFX(t, nbu)
	d := domain.NewDate(2026, 10, 26)
	rate, rateDate, ok, err := svc.RateFor(ctx, "EUR", d)
	if err != nil || !ok || rate != 4505000000 || !rateDate.Equal(d) {
		t.Fatalf("fetched EUR rate: %d %v %v %v", rate, rateDate, ok, err)
	}
	if nbu.calls.Load() != 1 {
		t.Fatalf("NBU calls = %d, want 1", nbu.calls.Load())
	}
	// The other currencies in the response were stored too.
	if usd, _, ok, err := st.RateForDate(ctx, "USD", d); err != nil || !ok || usd != 4150000000 {
		t.Fatalf("USD from the same response: %d %v %v", usd, ok, err)
	}
}

func TestRateForMissingWhenNBUFails(t *testing.T) {
	nbu := &fakeNBU{status: http.StatusServiceUnavailable}
	svc, _ := newTestFX(t, nbu)
	_, _, ok, err := svc.RateFor(context.Background(), "USD", domain.NewDate(2026, 10, 26))
	if err != nil {
		t.Fatalf("an NBU outage must not fail the caller: %v", err)
	}
	if ok {
		t.Fatal("rate reported as found while NBU is down and nothing is stored")
	}
}

func TestFetchDayRejectsResponseWithoutSupportedRates(t *testing.T) {
	nbu := &fakeNBU{status: http.StatusOK, body: `[{"r030":999,"txt":"X","rate":1,"cc":"XXX","exchangedate":"26.10.2026"}]`}
	svc, _ := newTestFX(t, nbu)
	if err := svc.FetchDay(context.Background(), domain.NewDate(2026, 10, 26)); err == nil {
		t.Fatal("expected an error when NBU returns no supported currencies")
	}
}
