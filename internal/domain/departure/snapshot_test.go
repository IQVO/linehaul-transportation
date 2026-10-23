package departure

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func TestSnapshotRestoreRoundTripsEveryState(t *testing.T) {
	for _, st := range allStates {
		d := inState(t, st)
		got, err := Restore(d.Snapshot())
		wantNoErr(t, string(st), err)
		if got.Snapshot() != d.Snapshot() {
			t.Errorf("%s: round trip changed %+v -> %+v", st, d.Snapshot(), got.Snapshot())
		}
	}
}

func TestRestoreAcceptsTheBoundaries(t *testing.T) {
	mutations := map[string]func(*Snapshot){
		"booked equals capacity": func(s *Snapshot) { s.BookedPackages = s.CapacityPackages },
		"booked zero":            func(s *Snapshot) { s.BookedPackages = 0 },
		"capacity zero":          func(s *Snapshot) { s.CapacityPackages, s.BookedPackages = 0, 0 },
		"capacity ceiling":       func(s *Snapshot) { s.CapacityPackages = shared.MaxInt32 },
		"version one":            func(s *Snapshot) { s.Version = 1 },
		"cutoff equals departs":  func(s *Snapshot) { s.CutoffAt = s.DepartsAt },
		"arrives one ns later":   func(s *Snapshot) { s.ArrivesAt = s.DepartsAt.Add(time.Nanosecond) },
	}
	for name, mut := range mutations {
		s := inState(t, StateScheduled).Snapshot()
		mut(&s)
		if _, err := Restore(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRestoreRefusesABrokenRow(t *testing.T) {
	cancelled := inState(t, StateCancelled).Snapshot
	arrived := inState(t, StateArrived).Snapshot
	cases := []struct {
		name string
		base func() Snapshot
		mut  func(*Snapshot)
	}{
		{"id", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.ID = "" }},
		{"kind", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.Kind = "RAIL" }},
		{"state", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.State = "OPEN" }},
		{"cutoff after departs", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.CutoffAt = s.DepartsAt.Add(time.Nanosecond) }},
		{"departs equals arrives", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.ArrivesAt = s.DepartsAt }},
		{"zero cutoff", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.CutoffAt = time.Time{} }},
		{"negative capacity", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.CapacityPackages, s.BookedPackages = -1, 0 }},
		{"capacity over ceiling", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.CapacityPackages = shared.MaxInt32 + 1 }},
		{"negative booked", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.BookedPackages = -1 }},
		{"booked above capacity", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.BookedPackages = s.CapacityPackages + 1 }},
		{"version zero", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.Version = 0 }},
		{"cancel reason on scheduled", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.CancelReason = "x" }},
		{"cancelledAt on scheduled", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.CancelledAt = t0 }},
		{"departedAt on scheduled", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.DepartedAt = t0 }},
		{"arrivedAt on scheduled", inState(t, StateScheduled).Snapshot, func(s *Snapshot) { s.ArrivedAt = t0 }},
		{"cancelled without reason", cancelled, func(s *Snapshot) { s.CancelReason = "" }},
		{"cancelled without instant", cancelled, func(s *Snapshot) { s.CancelledAt = time.Time{} }},
		{"cancelled with a padded reason", cancelled, func(s *Snapshot) { s.CancelReason = " x" }},
		{"arrived without departure", arrived, func(s *Snapshot) { s.DepartedAt = time.Time{} }},
		{"arrived without arrival", arrived, func(s *Snapshot) { s.ArrivedAt = time.Time{} }},
	}
	for _, c := range cases {
		s := c.base()
		c.mut(&s)
		if _, err := Restore(s); !isCorrupt(err) {
			t.Errorf("%s: err = %v, want invalid snapshot", c.name, err)
		}
	}
}

func isCorrupt(err error) bool {
	return err != nil && errors.Is(err, shared.ErrInvalidSnapshot)
}
