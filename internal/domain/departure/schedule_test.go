package departure

import (
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func TestScheduleRaisesDepartureScheduled(t *testing.T) {
	spec := baseSpec()
	spec.CutoffAt = spec.CutoffAt.In(time.FixedZone("x", -3*3600))
	d, evs, err := Schedule(spec, true, t0)
	wantNoErr(t, "schedule", err)
	assertScheduledState(t, d)
	if len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	ev, ok := evs[0].(DepartureScheduled)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	assertScheduledHeader(t, ev)
	assertScheduledPayload(t, ev)
}

func assertScheduledState(t *testing.T, d *Departure) {
	t.Helper()
	wantCounts(t, d, 2, 0, 1)
	if d.State() != StateScheduled || d.ID() != "dep-1" || d.LaneID() != "lane-1" || d.Kind() != KindPickup ||
		d.OriginNodeID() != "n-fc" || d.DestinationNodeID() != "n-sc" ||
		d.OriginType() != network.FulfillmentCenter || d.DestinationType() != network.SortationCenter {
		t.Fatalf("departure = %+v", d.Snapshot())
	}
	if !d.CutoffAt().Equal(cutoff) || d.CutoffAt().Location() != time.UTC {
		t.Fatalf("cutoff must be stored in UTC: %v", d.CutoffAt())
	}
}

func assertScheduledHeader(t *testing.T, ev DepartureScheduled) {
	t.Helper()
	if ev.EventName() != "DepartureScheduled" || ev.Entity() != "departure" || ev.SubjectID() != "dep-1" ||
		ev.AggregateVersion() != 1 || !ev.OccurredAt().Equal(t0) {
		t.Fatalf("header = %+v", ev.Header)
	}
}

func assertScheduledPayload(t *testing.T, ev DepartureScheduled) {
	t.Helper()
	if ev.DepartureID != "dep-1" || ev.LaneID != "lane-1" || ev.Kind != KindPickup || ev.OriginNodeID != "n-fc" ||
		ev.DestinationNodeID != "n-sc" || !ev.CutoffAt.Equal(cutoff) || !ev.DepartsAt.Equal(departs) ||
		!ev.ArrivesAt.Equal(arrives) || ev.CapacityPackages != 2 {
		t.Fatalf("payload = %+v", ev)
	}
}

func TestScheduleRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mut    func(*Spec)
		lane   bool
		target error
	}{
		{"departure id", func(s *Spec) { s.ID = "" }, true, shared.ErrValidation},
		{"lane id", func(s *Spec) { s.LaneID = "bad id" }, true, shared.ErrValidation},
		{"origin id", func(s *Spec) { s.OriginNodeID = "" }, true, shared.ErrValidation},
		{"destination id", func(s *Spec) { s.DestinationNodeID = "" }, true, shared.ErrValidation},
		{"kind", func(s *Spec) { s.Kind = "RAIL" }, true, shared.ErrValidation},
		{"origin type", func(s *Spec) { s.OriginType = "" }, true, shared.ErrValidation},
		{"destination type", func(s *Spec) { s.DestinationType = "DEPOT" }, true, shared.ErrValidation},
		{"negative capacity", func(s *Spec) { s.CapacityPackages = -1 }, true, shared.ErrValidation},
		{"capacity over ceiling", func(s *Spec) { s.CapacityPackages = shared.MaxInt32 + 1 }, true, shared.ErrValidation},
		{"cutoff after departs", func(s *Spec) { s.CutoffAt = s.DepartsAt.Add(time.Nanosecond) }, true, ErrInvalidDepartureTimes},
		{"departs equals arrives", func(s *Spec) { s.ArrivesAt = s.DepartsAt }, true, ErrInvalidDepartureTimes},
		{"departs after arrives", func(s *Spec) { s.ArrivesAt = s.DepartsAt.Add(-time.Nanosecond) }, true, ErrInvalidDepartureTimes},
		{"zero cutoff", func(s *Spec) { s.CutoffAt = time.Time{} }, true, ErrInvalidDepartureTimes},
		{"lane closed", func(*Spec) {}, false, ErrLaneClosed},
	}
	for _, c := range cases {
		s := baseSpec()
		c.mut(&s)
		d, evs, err := Schedule(s, c.lane, t0)
		wantErr(t, c.name, err, c.target)
		if d != nil || evs != nil {
			t.Errorf("%s: a refused schedule must return nothing", c.name)
		}
	}
}

func TestScheduleBoundariesAreAccepted(t *testing.T) {
	cases := map[string]func(*Spec){
		"cutoff equals departs": func(s *Spec) { s.CutoffAt = s.DepartsAt },
		"arrives one ns later":  func(s *Spec) { s.ArrivesAt = s.DepartsAt.Add(time.Nanosecond) },
		"capacity zero":         func(s *Spec) { s.CapacityPackages = 0 },
		"capacity ceiling":      func(s *Spec) { s.CapacityPackages = shared.MaxInt32 },
		"every kind air":        func(s *Spec) { s.Kind = KindAir },
		"every kind linehaul":   func(s *Spec) { s.Kind = KindLinehaul },
	}
	for name, mut := range cases {
		s := baseSpec()
		mut(&s)
		if _, _, err := Schedule(s, true, t0); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestScheduleChecksReferencesBeforeTimesBeforeLane(t *testing.T) {
	s := baseSpec()
	s.Kind = "RAIL"
	s.ArrivesAt = s.DepartsAt
	_, _, err := Schedule(s, false, t0)
	wantErr(t, "references first", err, shared.ErrValidation)
	s = baseSpec()
	s.ArrivesAt = s.DepartsAt
	_, _, err = Schedule(s, false, t0)
	wantErr(t, "times before lane", err, ErrInvalidDepartureTimes)
}

func TestKindAndStateValid(t *testing.T) {
	for _, k := range []Kind{KindPickup, KindLinehaul, KindAir} {
		if !k.Valid() {
			t.Errorf("%s", k)
		}
	}
	if Kind("").Valid() || Kind("pickup").Valid() {
		t.Fatal("unknown kind accepted")
	}
	for _, s := range allStates {
		if !s.Valid() {
			t.Errorf("%s", s)
		}
	}
	if State("").Valid() || State("OPEN").Valid() {
		t.Fatal("unknown state accepted")
	}
}
