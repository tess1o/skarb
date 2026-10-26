package store

import (
	"context"
	"path/filepath"
	"testing"

	"skarb/internal/domain"
)

func TestRatesNearestPriorAndManualOverride(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "skarb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := domain.NewDate(2026, 10, 26)
	if err := st.UpsertNBURate(ctx, nil, "USD", d, 4500000000); err != nil {
		t.Fatal(err)
	}
	rate, rateDate, ok, err := st.RateForDate(ctx, "USD", d)
	if err != nil || !ok || rate != 4500000000 || !rateDate.Equal(d) {
		t.Fatalf("initial rate: %d %v %v %v", rate, rateDate, ok, err)
	}
	if err := st.UpsertRate(ctx, nil, "USD", d, 4600000000, "manual", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNBURate(ctx, nil, "USD", d, 4700000000); err != nil {
		t.Fatal(err)
	}
	rate, _, ok, err = st.RateForDate(ctx, "USD", d)
	if err != nil || !ok || rate != 4600000000 {
		t.Fatalf("manual override was overwritten: %d %v %v", rate, ok, err)
	}
	next, _, ok, err := st.RateForDate(ctx, "USD", d.AddDays(1))
	if err != nil || !ok || next != 4600000000 {
		t.Fatalf("nearest prior failed: %d %v %v", next, ok, err)
	}
}
