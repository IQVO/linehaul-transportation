package departure

import (
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func TestBookUntilFullThenRefuse(t *testing.T) {
	d := mustSchedule(t, nil)
	for i := 1; i <= 2; i++ {
		evs, err := d.Book(t0)
		wantNoErr(t, "book", err)
		ev := capEvent(t, evs)
		if ev.CapacityPackages != 2 || ev.BookedPackages != i || ev.RemainingPackages != 2-i || ev.DepartureID != "dep-1" ||
			ev.EventName() != "DepartureCapacityChanged" || ev.Entity() != "departure" || ev.SubjectID() != "dep-1" ||
			ev.AggregateVersion() != int64(1+i) {
			t.Fatalf("event = %+v", ev)
		}
		wantCounts(t, d, 2, i, int64(1+i))
	}
	evs, err := d.Book(t0)
	wantErr(t, "third booking", err, ErrDepartureFull)
	if evs != nil {
		t.Fatal("a refused booking raises nothing")
	}
	wantCounts(t, d, 2, 2, 3)
}

func TestBookOnAZeroCapacityDepartureIsFull(t *testing.T) {
	d := mustSchedule(t, func(s *Spec) { s.CapacityPackages = 0 })
	_, err := d.Book(t0)
	wantErr(t, "zero capacity", err, ErrDepartureFull)
}

func TestBookCutoffBoundary(t *testing.T) {
	d := mustSchedule(t, nil)
	_, err := d.Book(cutoff)
	wantNoErr(t, "at the cutoff", err)
	_, err = d.Book(cutoff.Add(time.Nanosecond))
	wantErr(t, "just after the cutoff", err, ErrDepartureClosed)
	wantCounts(t, d, 2, 1, 2)
}

func TestBookNeedsAScheduledDeparture(t *testing.T) {
	for _, st := range allStates {
		d := inState(t, st)
		_, err := d.Book(t0)
		if st == StateScheduled {
			wantNoErr(t, "scheduled", err)
			continue
		}
		wantErr(t, string(st), err, ErrDepartureClosed)
	}
}

func TestReleaseStates(t *testing.T) {
	for _, st := range allStates {
		d := inState(t, StateScheduled)
		_, err := d.Book(t0)
		wantNoErr(t, "book", err)
		d = withState(t, d, st)
		before := d.Version()
		evs, err := d.Release(t0)
		switch st {
		case StateScheduled:
			wantNoErr(t, "scheduled", err)
			if ev := capEvent(t, evs); ev.BookedPackages != 0 || ev.RemainingPackages != 2 {
				t.Fatalf("event = %+v", ev)
			}
		case StateCancelled:
			wantNoErr(t, "cancelled", err)
			if evs != nil {
				t.Fatal("a cancelled departure announces no capacity change")
			}
			wantCounts(t, d, 2, 0, before+1)
		default:
			wantErr(t, string(st), err, shared.ErrIllegalTransition)
		}
	}
}

// withState walks a departure that already holds a booking into st.
func withState(t *testing.T, d *Departure, st State) *Departure {
	t.Helper()
	snap := d.Snapshot()
	snap.State = st
	switch st {
	case StateCancelled:
		snap.CancelReason, snap.CancelledAt = "planner", t0
	case StateDeparted:
		snap.DepartedAt = t0
	case StateArrived:
		snap.DepartedAt, snap.ArrivedAt = t0, t0
	}
	out, err := Restore(snap)
	wantNoErr(t, "restore", err)
	return out
}

func TestReleaseWithNothingBookedIsIllegal(t *testing.T) {
	for _, st := range []State{StateScheduled, StateCancelled} {
		d := inState(t, st)
		_, err := d.Release(t0)
		wantErr(t, string(st), err, shared.ErrIllegalTransition)
		wantCounts(t, d, 2, 0, d.Version())
	}
}

func TestChangeCapacity(t *testing.T) {
	d := mustSchedule(t, nil)
	_, _ = d.Book(t0)
	cases := []struct {
		name     string
		capacity int
		target   error
		version  int64
	}{
		{"below booked", 0, ErrDepartureFull, 2},
		{"negative", -1, shared.ErrValidation, 2},
		{"over ceiling", shared.MaxInt32 + 1, shared.ErrValidation, 2},
		{"equal to booked", 1, nil, 3},
		{"grow", 10, nil, 4},
		{"ceiling", shared.MaxInt32, nil, 5},
	}
	for _, c := range cases {
		evs, err := d.ChangeCapacity(c.capacity, t0)
		if c.target != nil {
			wantErr(t, c.name, err, c.target)
			if evs != nil {
				t.Errorf("%s: refused change raised events", c.name)
			}
		} else {
			wantNoErr(t, c.name, err)
			if ev := capEvent(t, evs); ev.CapacityPackages != c.capacity || ev.BookedPackages != 1 || ev.RemainingPackages != c.capacity-1 {
				t.Errorf("%s: event = %+v", c.name, ev)
			}
		}
		if d.Version() != c.version {
			t.Errorf("%s: version = %d, want %d", c.name, d.Version(), c.version)
		}
	}
}

func TestChangeCapacityToTheSameValueIsANoOp(t *testing.T) {
	d := mustSchedule(t, nil)
	evs, err := d.ChangeCapacity(2, t0)
	wantNoErr(t, "same", err)
	if evs != nil {
		t.Fatal("no event for an unchanged capacity")
	}
	wantCounts(t, d, 2, 0, 1)
}

func TestChangeCapacityNeedsAScheduledDeparture(t *testing.T) {
	for _, st := range allStates[1:] {
		d := inState(t, st)
		_, err := d.ChangeCapacity(5, t0)
		wantErr(t, string(st), err, shared.ErrIllegalTransition)
	}
}
