package service_test

import (
	"context"
	"errors"
	"testing"

	"skarb/internal/domain"
	"skarb/internal/service"
)

func TestSaveSnapshotAndHistoryUseCompleteDates(t *testing.T) {
	ctx := context.Background()
	svc, actor, cleanup := setupService(t)
	defer cleanup()

	uah, err := svc.CreateAccount(ctx, actor, service.CreateAccountInput{
		OwnerUserID: actor.UserID, Name: "Cash", Currency: "UAH", Kind: domain.AccountCash, IncludeInNetWorth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	usd, err := svc.CreateAccount(ctx, actor, service.CreateAccountInput{
		OwnerUserID: actor.UserID, Name: "Deposit", Currency: "USD", Kind: domain.AccountBank, IncludeInNetWorth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for date, rate := range map[string]int64{"2026-10-24": 40_00000000, "2026-10-25": 40_00000000, "2026-10-26": 42_00000000} {
		d, _ := domain.ParseDate(date)
		if err := svc.Store.UpsertRate(ctx, nil, "USD", d, rate, "manual", nil); err != nil {
			t.Fatal(err)
		}
	}
	save := func(date, cash, deposit string) {
		t.Helper()
		if err := svc.SaveSnapshot(ctx, actor, service.SaveSnapshotInput{Date: date, Balances: []service.SnapshotBalanceInput{
			{AccountUID: uah.UID, Amount: cash}, {AccountUID: usd.UID, Amount: deposit},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	save("2026-10-24", "0", "0")
	save("2026-10-25", "1000", "10")
	if _, err := svc.UpsertSnapshot(ctx, actor, service.UpsertSnapshotInput{AccountUID: uah.UID, Date: "2026-10-26", Amount: "1200"}); err != nil {
		t.Fatal(err)
	}
	points, err := svc.SnapshotHistory(ctx, actor, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Date.String() != "2026-10-25" || points[0].TotalUAH != 140_000 {
		t.Fatalf("history before complete snapshot = %+v", points)
	}
	save("2026-10-26", "1200", "10")
	points, err = svc.SnapshotHistory(ctx, actor, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[1].Date.String() != "2026-10-26" || points[1].TotalUAH != 162_000 || points[1].DeltaUAH != 22_000 {
		t.Fatalf("complete snapshot history = %+v", points)
	}

	if err := svc.SaveSnapshot(ctx, actor, service.SaveSnapshotInput{Date: "2026-10-27", Balances: []service.SnapshotBalanceInput{
		{AccountUID: uah.UID, Amount: "1500"}, {AccountUID: usd.UID, Amount: "bad"},
	}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid batch error = %v, want validation", err)
	}
	rows, err := svc.Store.ListSnapshots(ctx, uah.ID, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.SnapshotDate.String() == "2026-10-27" {
			t.Fatal("invalid batch wrote a partial snapshot")
		}
	}
}
