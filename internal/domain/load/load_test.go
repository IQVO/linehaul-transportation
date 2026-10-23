package load

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func dep(t *testing.T, kind departure.Kind, capacity int) *departure.Departure {
	t.Helper()
	d, _, err := departure.Schedule(departure.Spec{
		ID: "dep-1", LaneID: "lane-1", Kind: kind, OriginNodeID: "n-a", DestinationNodeID: "n-b",
		OriginType: network.FulfillmentCenter, DestinationType: network.SortationCenter,
		CutoffAt: t0.Add(time.Hour), DepartsAt: t0.Add(time.Hour), ArrivesAt: t0.Add(3 * time.Hour), CapacityPackages: capacity,
	}, true, t0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func openLoad(t *testing.T, capacity int) *Load {
	t.Helper()
	l, err := Open("load-1", dep(t, departure.KindPickup, capacity), false)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func add(t *testing.T, l *Load, tn string) {
	t.Helper()
	if added, err := l.AddPackage(tn, false, departure.StateScheduled); err != nil || !added {
		t.Fatalf("AddPackage(%s) = %v, %v", tn, added, err)
	}
}

func wantErr(t *testing.T, label string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v, want %v", label, err, target)
	}
}

// inState drives a load with two packages into the wanted state.
func inState(t *testing.T, want State) *Load {
	t.Helper()
	l := openLoad(t, 5)
	add(t, l, "TN1")
	add(t, l, "TN2")
	steps := map[State][]func() error{
		StateOpen:     {},
		StateClosed:   {func() error { _, e := l.Close(departure.StateScheduled, t0); return e }},
		StateDeparted: {func() error { _, e := l.Close(departure.StateScheduled, t0); return e }, func() error { _, e := l.Dispatch(t0); return e }},
		StateArrived: {func() error { _, e := l.Close(departure.StateScheduled, t0); return e }, func() error { _, e := l.Dispatch(t0); return e },
			func() error { _, e := l.Receive(t0); return e }},
	}
	for _, s := range steps[want] {
		if err := s(); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

var allStates = []State{StateOpen, StateClosed, StateDeparted, StateArrived}

func TestOpenCopiesTheDeparture(t *testing.T) {
	d := dep(t, departure.KindLinehaul, 7)
	l, err := Open("load-1", d, false)
	if err != nil {
		t.Fatal(err)
	}
	assertOpenedSnapshot(t, l.Snapshot())
	assertOpenedAccessors(t, l)
}

func assertOpenedSnapshot(t *testing.T, s Snapshot) {
	t.Helper()
	if s.ID != "load-1" || s.DepartureID != "dep-1" || s.Kind != departure.KindLinehaul || s.OriginNodeID != "n-a" || s.DestinationNodeID != "n-b" ||
		s.OriginType != network.FulfillmentCenter || s.DestinationType != network.SortationCenter || s.CapacityPackages != 7 ||
		s.State != StateOpen || s.Version != 1 || s.TrackingNumbers == nil || len(s.TrackingNumbers) != 0 {
		t.Fatalf("snapshot = %+v", s)
	}
}

func assertOpenedAccessors(t *testing.T, l *Load) {
	t.Helper()
	if l.ID() != "load-1" || l.DepartureID() != "dep-1" || l.Kind() != departure.KindLinehaul || l.Capacity() != 7 || l.PackageCount() != 0 ||
		l.State() != StateOpen || l.Version() != 1 || !l.Active() {
		t.Fatal("accessors")
	}
}

func TestOpenRefusals(t *testing.T) {
	d := dep(t, departure.KindPickup, 3)
	if _, err := Open("bad id", d, false); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("id: %v", err)
	}
	_, err := Open("load-1", d, true)
	wantErr(t, "second load", err, ErrLoadAlreadyExists)
	_ = d.Close()
	_, err = Open("load-1", d, false)
	wantErr(t, "closed departure", err, shared.ErrIllegalTransition)
	c := dep(t, departure.KindPickup, 3)
	_, _ = c.Cancel("planner", t0)
	_, err = Open("load-1", c, false)
	wantErr(t, "cancelled departure", err, shared.ErrIllegalTransition)
	z := dep(t, departure.KindPickup, 0)
	if _, err := Open("load-1", z, false); err != nil {
		t.Fatalf("zero capacity opens (it just holds nothing): %v", err)
	}
}

func TestAddPackageRules(t *testing.T) {
	l := openLoad(t, 2)
	add(t, l, "TN1")
	added, err := l.AddPackage("TN1", false, departure.StateScheduled)
	if added || err != nil || l.PackageCount() != 1 || l.Version() != 2 {
		t.Fatalf("repeat on this load: %v %v count=%d version=%d", added, err, l.PackageCount(), l.Version())
	}
	added, err = l.AddPackage("TN1", true, departure.StateScheduled)
	if added || err != nil {
		t.Fatal("being on THIS load wins over the other-load answer")
	}
	_, err = l.AddPackage("TN2", true, departure.StateScheduled)
	wantErr(t, "on another load", err, ErrPackageAlreadyLoaded)
	add(t, l, "TN2")
	_, err = l.AddPackage("TN3", false, departure.StateScheduled)
	wantErr(t, "over capacity", err, ErrLoadOverCapacity)
	if l.PackageCount() != 2 || l.Version() != 3 {
		t.Fatalf("refusals change nothing: %d %d", l.PackageCount(), l.Version())
	}
}

func TestAddPackageChecksOtherLoadBeforeCapacity(t *testing.T) {
	l := openLoad(t, 1)
	add(t, l, "TN1")
	_, err := l.AddPackage("TN2", true, departure.StateScheduled)
	wantErr(t, "already loaded first", err, ErrPackageAlreadyLoaded)
}

func TestAddPackageValidatesTheTrackingNumber(t *testing.T) {
	for in, ok := range map[string]bool{"": false, " a": false, "a ": false, "a\tb": false, strings.Repeat("x", MaxTrackingNumber): true, strings.Repeat("x", MaxTrackingNumber+1): false, "BR1": true} {
		l := openLoad(t, 5)
		_, err := l.AddPackage(in, false, departure.StateScheduled)
		if ok && err != nil {
			t.Errorf("%q: %v", in, err)
		}
		if !ok {
			wantErr(t, in, err, shared.ErrValidation)
		}
	}
}

func TestAddPackageNeedsAnOpenLoadAndAScheduledDeparture(t *testing.T) {
	for _, st := range allStates[1:] {
		l := inState(t, st)
		_, err := l.AddPackage("TN9", false, departure.StateScheduled)
		wantErr(t, string(st), err, shared.ErrIllegalTransition)
	}
	for _, ds := range []departure.State{departure.StateClosed, departure.StateCancelled, departure.StateDeparted, departure.StateArrived} {
		l := openLoad(t, 5)
		_, err := l.AddPackage("TN9", false, ds)
		wantErr(t, string(ds), err, shared.ErrIllegalTransition)
	}
}

func TestAddPackageAtCapacityBoundary(t *testing.T) {
	l := openLoad(t, 3)
	for _, tn := range []string{"A", "B", "C"} {
		add(t, l, tn)
	}
	if l.PackageCount() != 3 {
		t.Fatal("the load fills to its capacity exactly")
	}
	zero := openLoad(t, 0)
	_, err := zero.AddPackage("A", false, departure.StateScheduled)
	wantErr(t, "zero capacity", err, ErrLoadOverCapacity)
}

func TestEveryTransition(t *testing.T) {
	type cmd struct {
		name string
		from State
		to   State
		run  func(*Load) ([]shared.Event, error)
	}
	cmds := []cmd{
		{"CloseLoad", StateOpen, StateClosed, func(l *Load) ([]shared.Event, error) { return l.Close(departure.StateScheduled, t0) }},
		{"DispatchLoad", StateClosed, StateDeparted, func(l *Load) ([]shared.Event, error) { return l.Dispatch(t0) }},
		{"ReceiveLoad", StateDeparted, StateArrived, func(l *Load) ([]shared.Event, error) { return l.Receive(t0) }},
	}
	for _, c := range cmds {
		for _, st := range allStates {
			l := inState(t, st)
			before := l.Version()
			evs, err := c.run(l)
			if st != c.from {
				wantErr(t, c.name+" from "+string(st), err, shared.ErrIllegalTransition)
				if evs != nil || l.State() != st || l.Version() != before {
					t.Errorf("%s from %s: a refused command changes nothing", c.name, st)
				}
				continue
			}
			if err != nil || len(evs) != 1 || l.State() != c.to || l.Version() != before+1 {
				t.Errorf("%s: err=%v events=%d state=%s version=%d", c.name, err, len(evs), l.State(), l.Version())
			}
		}
	}
}

func TestCloseNeedsAScheduledDeparture(t *testing.T) {
	for _, ds := range []departure.State{departure.StateClosed, departure.StateCancelled} {
		l := inState(t, StateOpen)
		_, err := l.Close(ds, t0)
		wantErr(t, string(ds), err, shared.ErrIllegalTransition)
	}
}

func TestEventsCarryTheirData(t *testing.T) {
	l := inState(t, StateOpen)
	evs, _ := l.Close(departure.StateScheduled, t0)
	assertClosedEvent(t, l, evs[0].(LoadClosed))
	at := t0.Add(time.Hour).In(time.FixedZone("x", 3600))
	evs, _ = l.Dispatch(at)
	dep := evs[0].(LoadDeparted)
	assertDepartedEvent(t, l, dep, at)
	evs, _ = l.Receive(t0.Add(2 * time.Hour))
	assertArrivedEvent(t, l, evs[0].(LoadArrived))
	dep.TrackingNumbers[0] = "mutated"
	if l.Snapshot().TrackingNumbers[0] != "TN1" {
		t.Fatal("event slices must not alias the aggregate")
	}
}

func assertClosedEvent(t *testing.T, l *Load, ev LoadClosed) {
	t.Helper()
	if ev.EventName() != "LoadClosed" || ev.Entity() != "load" || ev.SubjectID() != "load-1" || ev.LoadID != "load-1" ||
		ev.DepartureID != "dep-1" || ev.PackageCount != 2 || ev.AggregateVersion() != l.Version() || !ev.OccurredAt().Equal(t0) {
		t.Fatalf("LoadClosed = %+v", ev)
	}
}

func assertDepartedEvent(t *testing.T, l *Load, ev LoadDeparted, at time.Time) {
	t.Helper()
	if ev.EventName() != "LoadDeparted" || ev.Entity() != "load" || ev.SubjectID() != "load-1" || ev.OriginNodeID != "n-a" ||
		ev.DepartureID != "dep-1" || strings.Join(ev.TrackingNumbers, ",") != "TN1,TN2" || !ev.DepartedAt.Equal(at) || ev.DepartedAt.Location() != time.UTC ||
		ev.DepartureKind != departure.KindPickup || ev.NodeType != network.FulfillmentCenter || ev.AggregateVersion() != l.Version() {
		t.Fatalf("LoadDeparted = %+v", ev)
	}
}

func assertArrivedEvent(t *testing.T, l *Load, ev LoadArrived) {
	t.Helper()
	if ev.EventName() != "LoadArrived" || ev.Entity() != "load" || ev.SubjectID() != "load-1" || ev.DestinationNodeID != "n-b" ||
		ev.DepartureID != "dep-1" || strings.Join(ev.TrackingNumbers, ",") != "TN1,TN2" || !ev.ArrivedAt.Equal(t0.Add(2*time.Hour)) ||
		ev.DepartureKind != departure.KindPickup || ev.NodeType != network.SortationCenter || ev.AggregateVersion() != l.Version() {
		t.Fatalf("LoadArrived = %+v", ev)
	}
}

func TestEventsOfAnEmptyLoadCarryAnEmptyArray(t *testing.T) {
	l := openLoad(t, 2)
	_, _ = l.Close(departure.StateScheduled, t0)
	evs, _ := l.Dispatch(t0)
	if got := evs[0].(LoadDeparted).TrackingNumbers; got == nil || len(got) != 0 {
		t.Fatalf("tracking numbers = %#v, want a non-nil empty slice", got)
	}
}

func TestActive(t *testing.T) {
	want := map[State]bool{StateOpen: true, StateClosed: true, StateDeparted: false, StateArrived: false}
	for _, st := range allStates {
		if got := inState(t, st).Active(); got != want[st] {
			t.Errorf("%s: Active = %v", st, got)
		}
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	for _, st := range allStates {
		l := inState(t, st)
		got, err := Restore(l.Snapshot())
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		a, b := got.Snapshot(), l.Snapshot()
		if a.State != b.State || a.Version != b.Version || strings.Join(a.TrackingNumbers, ",") != strings.Join(b.TrackingNumbers, ",") ||
			!a.DepartedAt.Equal(b.DepartedAt) || !a.ArrivedAt.Equal(b.ArrivedAt) || a.CapacityPackages != b.CapacityPackages {
			t.Errorf("%s: %+v -> %+v", st, b, a)
		}
	}
	empty, err := Restore(openLoad(t, 1).Snapshot())
	if err != nil || empty.Snapshot().TrackingNumbers == nil {
		t.Fatal("an empty load restores with a non-nil slice")
	}
}

func TestRestoreBoundariesAndRefusals(t *testing.T) {
	good := func() Snapshot { return inState(t, StateOpen).Snapshot() }
	accept := map[string]func(*Snapshot){
		"count equals capacity": func(s *Snapshot) { s.CapacityPackages = 2 },
		"capacity zero":         func(s *Snapshot) { s.CapacityPackages, s.TrackingNumbers = 0, nil },
		"capacity ceiling":      func(s *Snapshot) { s.CapacityPackages = shared.MaxInt32 },
		"version one":           func(s *Snapshot) { s.Version = 1 },
	}
	for name, mut := range accept {
		s := good()
		mut(&s)
		if _, err := Restore(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	refuse := map[string]func(*Snapshot){
		"load id":             func(s *Snapshot) { s.ID = "" },
		"departure id":        func(s *Snapshot) { s.DepartureID = "" },
		"origin":              func(s *Snapshot) { s.OriginNodeID = "" },
		"destination":         func(s *Snapshot) { s.DestinationNodeID = "" },
		"kind":                func(s *Snapshot) { s.Kind = "RAIL" },
		"origin type":         func(s *Snapshot) { s.OriginType = "" },
		"destination type":    func(s *Snapshot) { s.DestinationType = "DEPOT" },
		"state":               func(s *Snapshot) { s.State = "LOST" },
		"version":             func(s *Snapshot) { s.Version = 0 },
		"capacity negative":   func(s *Snapshot) { s.CapacityPackages = -1 },
		"capacity ceiling+1":  func(s *Snapshot) { s.CapacityPackages = shared.MaxInt32 + 1 },
		"over capacity":       func(s *Snapshot) { s.CapacityPackages = 1 },
		"tracking invalid":    func(s *Snapshot) { s.TrackingNumbers = []string{"ok", " bad"} },
		"tracking repeated":   func(s *Snapshot) { s.TrackingNumbers = []string{"ok", "ok"} },
		"open with departure": func(s *Snapshot) { s.DepartedAt = t0 },
		"open with arrival":   func(s *Snapshot) { s.ArrivedAt = t0 },
	}
	for name, mut := range refuse {
		s := good()
		mut(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	dep := inState(t, StateDeparted).Snapshot()
	dep.DepartedAt = time.Time{}
	if _, err := Restore(dep); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Error("departed without departedAt")
	}
	dep = inState(t, StateDeparted).Snapshot()
	dep.ArrivedAt = t0
	if _, err := Restore(dep); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Error("departed with arrivedAt")
	}
	arr := inState(t, StateArrived).Snapshot()
	arr.ArrivedAt = time.Time{}
	if _, err := Restore(arr); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Error("arrived without arrivedAt")
	}
	closed := inState(t, StateClosed).Snapshot()
	closed.DepartedAt = t0
	if _, err := Restore(closed); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Error("closed with departedAt")
	}
}

func TestStateValid(t *testing.T) {
	for _, st := range allStates {
		if !st.Valid() {
			t.Errorf("%s", st)
		}
	}
	if State("").Valid() || State("LOST").Valid() {
		t.Fatal("unknown state accepted")
	}
}
