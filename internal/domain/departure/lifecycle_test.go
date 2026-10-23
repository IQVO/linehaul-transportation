package departure

import (
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

type command struct {
	name string
	from State
	to   State
	run  func(*Departure, time.Time) ([]shared.Event, error)
}

var commands = []command{
	{"CloseDeparture", StateScheduled, StateClosed, func(d *Departure, _ time.Time) ([]shared.Event, error) { return nil, d.Close() }},
	{"RecordDeparture", StateClosed, StateDeparted, func(d *Departure, now time.Time) ([]shared.Event, error) { return d.RecordDeparture(now) }},
	{"RecordArrival", StateDeparted, StateArrived, func(d *Departure, now time.Time) ([]shared.Event, error) { return d.RecordArrival(now) }},
	{"CancelDeparture", StateScheduled, StateCancelled, func(d *Departure, now time.Time) ([]shared.Event, error) { return d.Cancel("planner", now) }},
}

func TestEveryLegalAndIllegalTransition(t *testing.T) {
	for _, c := range commands {
		for _, st := range allStates {
			d := inState(t, st)
			before := d.Version()
			_, err := c.run(d, t0)
			if st != c.from {
				wantErr(t, c.name+" from "+string(st), err, shared.ErrIllegalTransition)
				if d.State() != st || d.Version() != before {
					t.Errorf("%s from %s: a refused command must change nothing", c.name, st)
				}
				continue
			}
			wantNoErr(t, c.name, err)
			if d.State() != c.to || d.Version() != before+1 {
				t.Errorf("%s: state %s version %d; want %s %d", c.name, d.State(), d.Version(), c.to, before+1)
			}
		}
	}
}

func TestRecordDepartureRaisesDepartureDeparted(t *testing.T) {
	d := inState(t, StateClosed)
	at := t0.Add(time.Hour).In(time.FixedZone("x", 3600))
	evs, err := d.RecordDeparture(at)
	wantNoErr(t, "record", err)
	ev, ok := evs[0].(DepartureDeparted)
	if !ok || len(evs) != 1 {
		t.Fatalf("events = %#v", evs)
	}
	if ev.EventName() != "DepartureDeparted" || ev.Entity() != "departure" || ev.SubjectID() != "dep-1" ||
		!ev.DepartedAt.Equal(at) || ev.DepartedAt.Location() != time.UTC || ev.AggregateVersion() != d.Version() || !ev.OccurredAt().Equal(at) {
		t.Fatalf("event = %+v", ev)
	}
	if !d.Snapshot().DepartedAt.Equal(at) {
		t.Fatal("departedAt must be stored")
	}
}

func TestRecordArrivalRaisesDepartureArrived(t *testing.T) {
	d := inState(t, StateDeparted)
	at := t0.Add(2 * time.Hour)
	evs, err := d.RecordArrival(at)
	wantNoErr(t, "record", err)
	ev, ok := evs[0].(DepartureArrived)
	if !ok || len(evs) != 1 {
		t.Fatalf("events = %#v", evs)
	}
	if ev.EventName() != "DepartureArrived" || ev.Entity() != "departure" || ev.SubjectID() != "dep-1" ||
		!ev.ArrivedAt.Equal(at) || ev.AggregateVersion() != d.Version() {
		t.Fatalf("event = %+v", ev)
	}
}

func TestCancelRaisesDepartureCancelled(t *testing.T) {
	d := mustSchedule(t, nil)
	evs, err := d.Cancel("NODE_SUSPENDED", t0)
	wantNoErr(t, "cancel", err)
	ev, ok := evs[0].(DepartureCancelled)
	if !ok || len(evs) != 1 {
		t.Fatalf("events = %#v", evs)
	}
	if ev.EventName() != "DepartureCancelled" || ev.Entity() != "departure" || ev.SubjectID() != "dep-1" ||
		ev.Reason != "NODE_SUSPENDED" || !ev.CancelledAt.Equal(t0) || ev.AggregateVersion() != 2 {
		t.Fatalf("event = %+v", ev)
	}
	snap := d.Snapshot()
	if snap.CancelReason != "NODE_SUSPENDED" || !snap.CancelledAt.Equal(t0) {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestCancelReasonRules(t *testing.T) {
	cases := []struct {
		reason string
		ok     bool
	}{
		{"", false}, {" padded", false}, {"padded ", false}, {"tab\there", false}, {longText(shared.MaxText), true},
		{longText(shared.MaxText + 1), false}, {"x", true},
	}
	for _, c := range cases {
		d := mustSchedule(t, nil)
		_, err := d.Cancel(c.reason, t0)
		if c.ok {
			wantNoErr(t, c.reason, err)
		} else {
			wantErr(t, c.reason, err, shared.ErrValidation)
			if d.State() != StateScheduled || d.Version() != 1 {
				t.Errorf("%q: a refused cancel must change nothing", c.reason)
			}
		}
	}
}

func TestCancelStateIsCheckedBeforeTheReason(t *testing.T) {
	d := inState(t, StateClosed)
	_, err := d.Cancel("", t0)
	wantErr(t, "state first", err, shared.ErrIllegalTransition)
}
