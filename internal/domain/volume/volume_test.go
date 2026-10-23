package volume

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func facts() Facts { return Facts{OrderID: "ord-1", GroupNo: 1, NodeID: "n-fc", DepartureID: "dep-1"} }

func fresh(t *testing.T) *ExpectedVolume {
	t.Helper()
	v, err := Unknown("asg-1")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func inState(t *testing.T, st State) *ExpectedVolume {
	t.Helper()
	v := fresh(t)
	switch st {
	case StateReleased:
		mustRelease(t, v, 1)
	case StateBooked:
		mustRelease(t, v, 1)
		if err := v.Book("dep-1"); err != nil {
			t.Fatal(err)
		}
	case StateFailed:
		if _, err := v.Fail("dep-1", ReasonDepartureFull, t0); err != nil {
			t.Fatal(err)
		}
	case StateCancelled:
		v.Cancel()
	}
	if v.State() != st {
		t.Fatalf("state = %s, want %s", v.State(), st)
	}
	return v
}

// reload simulates a Save followed by a Get: the row is clean at its stored version.
func reload(t *testing.T, v *ExpectedVolume) *ExpectedVolume {
	t.Helper()
	s := v.Snapshot()
	if s.Version < 1 {
		s.Version = 1
	}
	out, err := Restore(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustRelease(t *testing.T, v *ExpectedVolume, no int) {
	t.Helper()
	if ok, err := v.Release(facts(), no); !ok || err != nil {
		t.Fatalf("Release = %v, %v", ok, err)
	}
}

var allStates = []State{StateUnknown, StateReleased, StateBooked, StateFailed, StateCancelled}

func wantErr(t *testing.T, label string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v, want %v", label, err, target)
	}
}

func TestOneSaveWritesOneVersionHoweverManyCommandsRan(t *testing.T) {
	v := fresh(t)
	mustRelease(t, v, 1)
	_ = v.Book("dep-1")
	_, _ = v.AddTracking("TN1")
	if v.Version() != 1 || v.Snapshot().Version != 1 {
		t.Fatalf("a new row is written at version 1: %d", v.Version())
	}
	stored, err := Restore(v.Snapshot())
	if err != nil || stored.Version() != 1 {
		t.Fatalf("restored: %v %d", err, stored.Version())
	}
	_, _ = stored.AddTracking("TN2")
	_ = stored.Cancel()
	if stored.Version() != 2 {
		t.Fatalf("a loaded row is written at stored+1: %d", stored.Version())
	}
}

func TestUnknown(t *testing.T) {
	v := fresh(t)
	if v.State() != StateUnknown || v.AssignmentID() != "asg-1" || v.Version() != 0 || v.NodeID() != "" || v.PlannedDepartureID() != "" ||
		v.BookedOn() != "" || v.TrackingNumbers() == nil || len(v.TrackingNumbers()) != 0 {
		t.Fatalf("fresh = %+v", v.Snapshot())
	}
	if _, err := Unknown("bad id"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("id: %v", err)
	}
}

func TestReleaseAppliesByReleaseNo(t *testing.T) {
	v := fresh(t)
	mustRelease(t, v, 2)
	if v.State() != StateReleased || v.NodeID() != "n-fc" || v.PlannedDepartureID() != "dep-1" || v.Version() != 1 {
		t.Fatalf("after release: %+v", v.Snapshot())
	}
	older := facts()
	older.NodeID = "n-old"
	if ok, err := v.Release(older, 1); ok || err != nil || v.NodeID() != "n-fc" {
		t.Fatalf("an older release is ignored: %v %v", ok, err)
	}
	newer := facts()
	newer.NodeID, newer.DepartureID = "n-new", "dep-2"
	if ok, _ := v.Release(newer, 2); !ok || v.NodeID() != "n-new" || v.PlannedDepartureID() != "dep-2" {
		t.Fatal("an equal releaseNo is applied again")
	}
	if ok, _ := v.Release(newer, 3); !ok {
		t.Fatal("a newer release applies")
	}
}

func TestReleaseIsIgnoredOnceBookedFailedOrCancelled(t *testing.T) {
	for _, st := range []State{StateBooked, StateFailed, StateCancelled} {
		v := inState(t, st)
		before := v.Version()
		if ok, err := v.Release(facts(), 9); ok || err != nil || v.State() != st || v.Version() != before {
			t.Errorf("%s: release must be ignored (%v, %v)", st, ok, err)
		}
	}
}

func TestReleaseValidates(t *testing.T) {
	bad := map[string]func(*Facts){
		"order":     func(f *Facts) { f.OrderID = "" },
		"node":      func(f *Facts) { f.NodeID = "" },
		"departure": func(f *Facts) { f.DepartureID = "" },
		"group 0":   func(f *Facts) { f.GroupNo = 0 },
		"group max": func(f *Facts) { f.GroupNo = shared.MaxInt32 + 1 },
	}
	for name, mut := range bad {
		f := facts()
		mut(&f)
		if _, err := fresh(t).Release(f, 1); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, no := range []int{0, shared.MaxInt32 + 1} {
		if _, err := fresh(t).Release(facts(), no); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("releaseNo %d: %v", no, err)
		}
	}
	f := facts()
	f.GroupNo = shared.MaxInt32
	if _, err := fresh(t).Release(f, shared.MaxInt32); err != nil {
		t.Fatalf("ceilings: %v", err)
	}
}

func TestReassign(t *testing.T) {
	v := fresh(t)
	if ok, err := v.Reassign("n-2"); ok || err != nil {
		t.Fatal("reassigning an unknown assignment is ignored")
	}
	mustRelease(t, v, 1)
	if ok, err := v.Reassign("n-2"); !ok || err != nil || v.NodeID() != "n-2" || v.PlannedDepartureID() != "" {
		t.Fatalf("reassign: %v %v %+v", ok, err, v.Snapshot())
	}
	if _, err := v.Reassign("bad id"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("invalid node: %v", err)
	}
	for _, st := range []State{StateBooked, StateFailed, StateCancelled} {
		w := inState(t, st)
		if ok, _ := w.Reassign("n-2"); ok {
			t.Errorf("%s: a reassignment after the lock changes nothing", st)
		}
	}
}

func TestCancel(t *testing.T) {
	for _, st := range allStates {
		v := reload(t, inState(t, st))
		before := v.Version()
		changed := v.Cancel()
		if st == StateCancelled {
			if changed || v.Version() != before {
				t.Error("cancelling twice changes nothing")
			}
			continue
		}
		if !changed || v.State() != StateCancelled || v.Version() != before+1 {
			t.Errorf("%s: cancel -> %v %s", st, changed, v.State())
		}
	}
	b := inState(t, StateBooked)
	b.Cancel()
	if b.BookedOn() != "dep-1" {
		t.Fatal("the booking record is kept (ADR 0002 A16)")
	}
}

func TestLock(t *testing.T) {
	for _, st := range allStates {
		v := inState(t, st)
		f := facts()
		f.NodeID, f.DepartureID = "n-lock", "dep-lock"
		need, err := v.Lock(f)
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		want := st != StateBooked && st != StateCancelled
		if need != want {
			t.Errorf("%s: needsBooking = %v, want %v", st, need, want)
		}
		if want && (v.NodeID() != "n-lock" || v.PlannedDepartureID() != "dep-lock") {
			t.Errorf("%s: the lock facts replace the released ones", st)
		}
		if !want && v.NodeID() == "n-lock" {
			t.Errorf("%s: a refused lock changes nothing", st)
		}
	}
	bad := facts()
	bad.OrderID = ""
	if _, err := fresh(t).Lock(bad); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("invalid facts: %v", err)
	}
}

func TestBookIsOnceOnly(t *testing.T) {
	v := fresh(t)
	if err := v.Book("bad id"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("invalid departure: %v", err)
	}
	for _, st := range []State{StateUnknown, StateReleased, StateFailed} {
		w := inState(t, st)
		if err := w.Book("dep-9"); err != nil || w.State() != StateBooked || w.BookedOn() != "dep-9" {
			t.Errorf("%s: %v", st, err)
		}
	}
	for _, st := range []State{StateBooked, StateCancelled} {
		w := inState(t, st)
		wantErr(t, string(st), w.Book("dep-9"), shared.ErrIllegalTransition)
	}
}

func TestRebook(t *testing.T) {
	v := inState(t, StateBooked)
	evs, err := v.Rebook("dep-1", "dep-2", t0)
	if err != nil || len(evs) != 1 {
		t.Fatalf("rebook: %v %v", evs, err)
	}
	assertMoved(t, v, evs[0].(BookingMoved))
	if v.BookedOn() != "dep-2" || v.State() != StateBooked {
		t.Fatal("booking record moved")
	}
	if _, err := v.Rebook("dep-1", "dep-3", t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("from must be where it sits: %v", err)
	}
}

func assertMoved(t *testing.T, v *ExpectedVolume, ev BookingMoved) {
	t.Helper()
	if ev.EventName() != "BookingMoved" || ev.Entity() != "departure" || ev.SubjectID() != "dep-2" || ev.AssignmentID != "asg-1" ||
		ev.FromDepartureID != "dep-1" || ev.ToDepartureID != "dep-2" || ev.AggregateVersion() != v.Version() || !ev.OccurredAt().Equal(t0) {
		t.Fatalf("event = %+v", ev)
	}
}

func TestRebookFromEveryOtherState(t *testing.T) {
	for _, st := range []State{StateUnknown, StateReleased, StateFailed} {
		w := inState(t, st)
		if evs, err := w.Rebook("dep-1", "dep-2", t0); err != nil || len(evs) != 1 || w.State() != StateBooked || w.BookedOn() != "dep-2" {
			t.Errorf("%s: %v", st, err)
		}
	}
	_, err := inState(t, StateCancelled).Rebook("dep-1", "dep-2", t0)
	wantErr(t, "cancelled", err, shared.ErrIllegalTransition)
	for _, ids := range [][2]departure.ID{{"", "dep-2"}, {"dep-1", ""}} {
		_, err := inState(t, StateReleased).Rebook(ids[0], ids[1], t0)
		wantErr(t, "ids", err, shared.ErrValidation)
	}
}

func TestFail(t *testing.T) {
	for _, st := range []State{StateUnknown, StateReleased, StateBooked, StateFailed} {
		v := inState(t, st)
		evs, err := v.Fail("dep-1", ReasonDepartureCancelled, t0)
		if err != nil || len(evs) != 1 || v.State() != StateFailed || v.BookedOn() != "" {
			t.Fatalf("%s: %v", st, err)
		}
		ev := evs[0].(BookingFailed)
		if ev.EventName() != "BookingFailed" || ev.Entity() != "departure" || ev.SubjectID() != "dep-1" || ev.AssignmentID != "asg-1" ||
			ev.DepartureID != "dep-1" || ev.Reason != ReasonDepartureCancelled || ev.AggregateVersion() != v.Version() {
			t.Fatalf("event = %+v", ev)
		}
	}
	_, err := inState(t, StateCancelled).Fail("dep-1", ReasonDepartureFull, t0)
	wantErr(t, "cancelled", err, shared.ErrIllegalTransition)
	_, err = fresh(t).Fail("dep-1", "BECAUSE", t0)
	wantErr(t, "reason", err, shared.ErrValidation)
	_, err = fresh(t).Fail("", ReasonDepartureFull, t0)
	wantErr(t, "departure", err, shared.ErrValidation)
}

func TestReasonValid(t *testing.T) {
	for _, r := range []Reason{ReasonDepartureCancelled, ReasonDepartureClosed, ReasonDepartureFull, ReasonDepartureUnknown} {
		if !r.Valid() {
			t.Errorf("%s", r)
		}
	}
	if Reason("").Valid() || Reason("OTHER").Valid() {
		t.Fatal("unknown reason accepted")
	}
}

func TestTrackingNumbers(t *testing.T) {
	v := fresh(t)
	if ok, err := v.AddTracking("TN1"); !ok || err != nil {
		t.Fatal(err)
	}
	if ok, err := v.AddTracking("TN1"); ok || err != nil || v.Version() != 1 {
		t.Fatalf("known tracking: %v %v version %d", ok, err, v.Version())
	}
	if _, err := v.AddTracking(" bad"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := v.AddTracking(strings.Repeat("x", 65)); !errors.Is(err, shared.ErrValidation) {
		t.Fatal("65 characters is too long")
	}
	if _, err := v.AddTracking(strings.Repeat("x", 64)); err != nil {
		t.Fatalf("64 characters is fine: %v", err)
	}
	got := v.TrackingNumbers()
	got[0] = "mutated"
	if v.TrackingNumbers()[0] != "TN1" {
		t.Fatal("TrackingNumbers returns a copy")
	}
	if !v.RemoveTracking("TN1") || v.RemoveTracking("TN1") || len(v.TrackingNumbers()) != 1 {
		t.Fatal("RemoveTracking")
	}
}

func TestTrackingNumbersAreBounded(t *testing.T) {
	v := fresh(t)
	for i := 0; i < MaxTrackingNumbers; i++ {
		if _, err := v.AddTracking("T" + strings.Repeat("1", 1) + string(rune('a'+i%26)) + strings.Repeat("0", i/26)); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if len(v.TrackingNumbers()) != MaxTrackingNumbers {
		t.Fatalf("count = %d", len(v.TrackingNumbers()))
	}
	if _, err := v.AddTracking("one-more"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("over the bound: %v", err)
	}
	if ok, err := v.AddTracking("T1a"); ok || err != nil {
		t.Fatal("a known number at the bound is still a no-op")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	for _, st := range allStates {
		v := inState(t, st)
		_, _ = v.AddTracking("TN1")
		got, err := Restore(v.Snapshot())
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		a, b := got.Snapshot(), v.Snapshot()
		if a.State != b.State || a.BookedOn != b.BookedOn || a.NodeID != b.NodeID || a.Version != b.Version || strings.Join(a.TrackingNumbers, ",") != "TN1" ||
			a.OrderID != b.OrderID || a.GroupNo != b.GroupNo || a.DepartureID != b.DepartureID || a.ReleaseNo != b.ReleaseNo {
			t.Errorf("%s: %+v -> %+v", st, b, a)
		}
	}
	unsaved := fresh(t)
	_, _ = unsaved.AddTracking("T")
	unsaved.RemoveTracking("T")
	if got, err := Restore(unsaved.Snapshot()); err != nil || got.TrackingNumbers() == nil {
		t.Fatal("non-nil empty slice after restore")
	}
}

func TestRestoreRefusals(t *testing.T) {
	good := func() Snapshot { return inState(t, StateBooked).Snapshot() }
	accept := map[string]func(*Snapshot){
		"version one":        func(s *Snapshot) { s.Version = 1 },
		"release no zero":    func(s *Snapshot) { s.ReleaseNo = 0 },
		"release no max":     func(s *Snapshot) { s.ReleaseNo = shared.MaxInt32 },
		"cancelled booked":   func(s *Snapshot) { s.State = StateCancelled },
		"cancelled unbooked": func(s *Snapshot) { s.State, s.BookedOn = StateCancelled, "" },
	}
	for name, mut := range accept {
		s := good()
		mut(&s)
		if _, err := Restore(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	refuse := map[string]func(*Snapshot){
		"id":                func(s *Snapshot) { s.AssignmentID = "" },
		"state":             func(s *Snapshot) { s.State = "LOST" },
		"version":           func(s *Snapshot) { s.Version = 0 },
		"booked w/o record": func(s *Snapshot) { s.BookedOn = "" },
		"record w/o booked": func(s *Snapshot) { s.State = StateReleased },
		"record invalid":    func(s *Snapshot) { s.BookedOn = "bad id" },
		"release no":        func(s *Snapshot) { s.ReleaseNo = -1 },
		"release no max+1":  func(s *Snapshot) { s.ReleaseNo = shared.MaxInt32 + 1 },
		"tracking invalid":  func(s *Snapshot) { s.TrackingNumbers = []string{" x"} },
		"too many tracking": func(s *Snapshot) { s.TrackingNumbers = make([]string, MaxTrackingNumbers+1) },
	}
	for name, mut := range refuse {
		s := good()
		mut(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	s := good()
	s.TrackingNumbers = make([]string, MaxTrackingNumbers)
	for i := range s.TrackingNumbers {
		s.TrackingNumbers[i] = "t"
	}
	if _, err := Restore(s); err != nil {
		t.Fatalf("exactly the bound: %v", err)
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
