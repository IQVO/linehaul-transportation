package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

var unitOfWorkCases = []named{
	{"a returned nil commits every save, the claim and the event", uowCommit},
	{"a returned error rolls everything back and is returned unchanged", uowRollback},
	{"a panic rolls back and propagates", uowPanic},
	{"a nested Do joins the outer unit", uowNested},
}

func twoDepartures(t *testing.T, f Fixture, ctx context.Context) error {
	t.Helper()
	d1 := NewDeparture(t, "dep-1", "n-fc", departure.KindPickup, time.Hour, 3)
	d2 := NewDeparture(t, "dep-2", "n-fc", departure.KindPickup, 2*time.Hour, 3)
	if err := f.Departures.Save(ctx, d1); err != nil {
		return err
	}
	if _, err := f.Inbox.Claim(ctx, "node-topology", "evt-1", at); err != nil {
		return err
	}
	if err := f.Outbox.Append(ctx, departureScheduledEvent(t, d2)); err != nil {
		return err
	}
	return f.Departures.Save(ctx, d2)
}

func departureScheduledEvent(t *testing.T, d *departure.Departure) departure.DepartureScheduled {
	t.Helper()
	return departure.DepartureScheduled{Header: shared.Header{Version: 1, At: at}, DepartureID: d.ID(), LaneID: d.LaneID(), Kind: d.Kind(), OriginNodeID: d.OriginNodeID(),
		DestinationNodeID: d.DestinationNodeID(), CutoffAt: d.CutoffAt(), DepartsAt: d.CutoffAt(), ArrivesAt: d.CutoffAt().Add(time.Hour), CapacityPackages: d.Capacity()}
}

func stored(f Fixture, id departure.ID) bool {
	_, err := f.Departures.Get(context.Background(), id)
	return err == nil
}

func outboxCount(t *testing.T, f Fixture) int {
	t.Helper()
	n, err := f.OutboxCount()
	noErr(t, err)
	return n
}

func claimable(t *testing.T, f Fixture) bool {
	t.Helper()
	first, err := f.Inbox.Claim(context.Background(), "node-topology", "evt-1", at)
	noErr(t, err)
	return first
}

func uowCommit(t *testing.T, f Fixture) {
	noErr(t, f.UoW.Do(context.Background(), func(ctx context.Context) error { return twoDepartures(t, f, ctx) }))
	if !stored(f, "dep-1") || !stored(f, "dep-2") || outboxCount(t, f) != 1 {
		t.Fatal("a committed unit lost a save or the event")
	}
	if claimable(t, f) {
		t.Fatal("the claim was committed with the unit")
	}
}

func uowRollback(t *testing.T, f Fixture) {
	err := f.UoW.Do(context.Background(), func(ctx context.Context) error {
		noErr(t, twoDepartures(t, f, ctx))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if stored(f, "dep-1") || stored(f, "dep-2") || outboxCount(t, f) != 0 {
		t.Fatal("a failed unit left a save or an event behind")
	}
	if !claimable(t, f) {
		t.Fatal("the claim was rolled back with the unit")
	}
}

func uowPanic(t *testing.T, f Fixture) {
	defer func() {
		if recover() == nil {
			t.Fatal("the panic was swallowed")
		}
		if stored(f, "dep-1") || outboxCount(t, f) != 0 {
			t.Fatal("a panicking unit left a save behind")
		}
	}()
	_ = f.UoW.Do(context.Background(), func(ctx context.Context) error {
		noErr(t, twoDepartures(t, f, ctx))
		panic("boom")
	})
}

func uowNested(t *testing.T, f Fixture) {
	err := f.UoW.Do(context.Background(), func(ctx context.Context) error {
		noErr(t, f.UoW.Do(ctx, func(ctx context.Context) error {
			return f.Departures.Save(ctx, NewDeparture(t, "dep-1", "n-fc", departure.KindPickup, time.Hour, 3))
		}))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if stored(f, "dep-1") {
		t.Fatal("the inner unit must roll back with the outer one")
	}
}
