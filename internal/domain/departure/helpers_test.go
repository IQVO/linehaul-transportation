package departure

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

var (
	t0      = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cutoff  = t0.Add(6 * time.Hour)
	departs = cutoff.Add(30 * time.Minute)
	arrives = departs.Add(3 * time.Hour)
)

func baseSpec() Spec {
	return Spec{
		ID: "dep-1", LaneID: "lane-1", Kind: KindPickup, OriginNodeID: "n-fc", DestinationNodeID: "n-sc",
		OriginType: network.FulfillmentCenter, DestinationType: network.SortationCenter,
		CutoffAt: cutoff, DepartsAt: departs, ArrivesAt: arrives, CapacityPackages: 2,
	}
}

func mustSchedule(t *testing.T, mut func(*Spec)) *Departure {
	t.Helper()
	s := baseSpec()
	if mut != nil {
		mut(&s)
	}
	d, _, err := Schedule(s, true, t0)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	return d
}

// inState drives a fresh departure into the wanted state through legal commands.
func inState(t *testing.T, want State) *Departure {
	t.Helper()
	d := mustSchedule(t, nil)
	steps := map[State][]func(*Departure) error{
		StateScheduled: {},
		StateClosed:    {func(d *Departure) error { return d.Close() }},
		StateDeparted: {
			func(d *Departure) error { return d.Close() },
			func(d *Departure) error { _, err := d.RecordDeparture(t0); return err },
		},
		StateArrived: {
			func(d *Departure) error { return d.Close() },
			func(d *Departure) error { _, err := d.RecordDeparture(t0); return err },
			func(d *Departure) error { _, err := d.RecordArrival(t0); return err },
		},
		StateCancelled: {func(d *Departure) error { _, err := d.Cancel("planner", t0); return err }},
	}
	for _, step := range steps[want] {
		if err := step(d); err != nil {
			t.Fatalf("driving to %s: %v", want, err)
		}
	}
	if d.State() != want {
		t.Fatalf("state = %s, want %s", d.State(), want)
	}
	return d
}

var allStates = []State{StateScheduled, StateClosed, StateDeparted, StateArrived, StateCancelled}

func wantErr(t *testing.T, label string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v, want %v", label, err, target)
	}
}

func wantNoErr(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", label, err)
	}
}

func wantCounts(t *testing.T, d *Departure, capacity, booked int, version int64) {
	t.Helper()
	if d.Capacity() != capacity || d.Booked() != booked || d.Remaining() != capacity-booked || d.Version() != version {
		t.Fatalf("capacity=%d booked=%d remaining=%d version=%d; want %d %d %d %d",
			d.Capacity(), d.Booked(), d.Remaining(), d.Version(), capacity, booked, capacity-booked, version)
	}
}

func capEvent(t *testing.T, evs []shared.Event) DepartureCapacityChanged {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	e, ok := evs[0].(DepartureCapacityChanged)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	return e
}

func longText(n int) string { return strings.Repeat("x", n) }
