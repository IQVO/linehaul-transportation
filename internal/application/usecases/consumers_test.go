package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

var evSeq int

func nextID() string { evSeq++; return fmt.Sprintf("evt-%d", evSeq) }

func lockIn(asg, node, dep string) usecases.LockedInput {
	return usecases.LockedInput{AssignmentID: asg, Facts: volume.Facts{OrderID: "ord-1", GroupNo: 1, NodeID: node, DepartureID: departure.ID(dep)}}
}

func (e *env) lock(asg, node, dep string) {
	e.t.Helper()
	e.must(e.svc.HandleLocked(e.ctx, nextID(), lockIn(asg, node, dep)))
}

func (e *env) volume(asg string) *volume.ExpectedVolume {
	e.t.Helper()
	v, err := e.store.Volumes().Get(e.ctx, asg)
	e.must(err)
	return v
}

func TestLockBooksThePickupDeparture(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 2)
	e.lock("asg-1", "n-fc", "dep-1")
	if d := e.departure("dep-1"); d.Booked() != 1 {
		t.Fatalf("booked = %d", d.Booked())
	}
	if v := e.volume("asg-1"); v.State() != volume.StateBooked || v.BookedOn() != "dep-1" {
		t.Fatalf("volume = %+v", v.Snapshot())
	}
	sameNames(t, e.eventNames(), []string{"DepartureCapacityChanged"})
	if ev := eventsOf[departure.DepartureCapacityChanged](e)[0]; ev.BookedPackages != 1 || ev.RemainingPackages != 1 {
		t.Fatalf("event = %+v", ev)
	}
}

func TestLockIsOnceOnly(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	in := lockIn("asg-1", "n-fc", "dep-1")
	e.must(e.svc.HandleLocked(e.ctx, "evt-same", in))
	before := e.store.Counts()
	e.must(e.svc.HandleLocked(e.ctx, "evt-same", in))
	e.unchanged(before)
	e.must(e.svc.HandleLocked(e.ctx, "evt-other", in))
	if d := e.departure("dep-1"); d.Booked() != 1 {
		t.Fatalf("a repeated lock must not book twice: %d", d.Booked())
	}
	if got := len(e.outbox.Events()); got != 1 {
		t.Fatalf("events = %d", got)
	}
}

func TestLockRebooksWhenThePickupCannotTakeIt(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(e *env)
		target string
		next   string
	}{
		{"unknown departure: the next one after now", func(e *env) {}, "dep-ghost", "dep-1"},
		{"full departure", func(e *env) { e.book("dep-1", 1) }, "dep-1", "dep-2"},
		{"cutoff passed", func(e *env) { e.clock.Set(t0.Add(90 * time.Minute)) }, "dep-1", "dep-2"},
		{"cancelled departure", func(e *env) { _, err := e.svc.CancelDeparture(e.ctx, "dep-1", "planner"); e.must(err) }, "dep-1", "dep-2"},
	}
	for _, c := range cases {
		e := newEnv(t)
		e.departureAt("dep-1", departure.KindPickup, time.Hour, 1)
		e.departureAt("dep-2", departure.KindPickup, 6*time.Hour, 3)
		c.setup(e)
		mark := len(e.outbox.Events())
		e.lock("asg-1", "n-fc", c.target)
		v := e.volume("asg-1")
		if v.State() != volume.StateBooked || string(v.BookedOn()) != c.next {
			t.Errorf("%s: volume = %+v", c.name, v.Snapshot())
		}
		if e.departure(c.next).Booked() != 1 {
			t.Errorf("%s: the next departure holds the package", c.name)
		}
		var moved []volume.BookingMoved
		for _, ev := range e.outbox.Events()[mark:] {
			if m, ok := ev.(volume.BookingMoved); ok {
				moved = append(moved, m)
			}
		}
		if len(moved) != 1 || moved[0].FromDepartureID != departure.ID(c.target) || string(moved[0].ToDepartureID) != c.next || moved[0].AssignmentID != "asg-1" {
			t.Errorf("%s: moved = %+v", c.name, moved)
		}
	}
}

func TestLockFailsWhenNoDepartureFits(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(e *env)
		target string
		reason volume.Reason
	}{
		{"unknown", func(e *env) { e.clock.Set(t0.Add(2 * time.Hour)) }, "dep-ghost", volume.ReasonDepartureUnknown},
		{"full", func(e *env) { e.book("dep-1", 1) }, "dep-1", volume.ReasonDepartureFull},
		{"closed by the cutoff", func(e *env) { e.clock.Set(t0.Add(2 * time.Hour)) }, "dep-1", volume.ReasonDepartureClosed},
		{"cancelled", func(e *env) { _, err := e.svc.CancelDeparture(e.ctx, "dep-1", "planner"); e.must(err) }, "dep-1", volume.ReasonDepartureCancelled},
	}
	for _, c := range cases {
		e := newEnv(t)
		e.departureAt("dep-1", departure.KindPickup, time.Hour, 1)
		c.setup(e)
		mark := len(e.outbox.Events())
		e.lock("asg-1", "n-fc", c.target)
		if v := e.volume("asg-1"); v.State() != volume.StateFailed || v.BookedOn() != "" {
			t.Errorf("%s: volume = %+v", c.name, v.Snapshot())
		}
		var failed []volume.BookingFailed
		for _, ev := range e.outbox.Events()[mark:] {
			if f, ok := ev.(volume.BookingFailed); ok {
				failed = append(failed, f)
			}
		}
		if len(failed) != 1 || failed[0].Reason != c.reason || failed[0].DepartureID != departure.ID(c.target) {
			t.Errorf("%s: failed = %+v", c.name, failed)
		}
	}
}

func TestRebookingChoosesTheNextDepartureOfTheSameOrigin(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, 4*time.Hour, 1)
	e.book("dep-1", 1)
	e.departureAt("dep-early", departure.KindPickup, time.Hour, 5)                       // earlier than the failed cutoff
	e.departureFrom("dep-other-origin", "n-other", departure.KindPickup, 5*time.Hour, 5) // other origin
	e.departureAt("dep-linehaul", departure.KindLinehaul, 5*time.Hour, 5)                // not a pickup
	e.departureAt("dep-full", departure.KindPickup, 6*time.Hour, 1)
	e.book("dep-full", 1)
	e.departureAt("dep-ok-late", departure.KindPickup, 9*time.Hour, 5)
	e.departureAt("dep-ok", departure.KindPickup, 7*time.Hour, 5)
	e.lock("asg-1", "n-fc", "dep-1")
	if v := e.volume("asg-1"); v.BookedOn() != "dep-ok" {
		t.Fatalf("booked on %s, want the first departure after the failed cutoff with room", v.BookedOn())
	}
}

func TestLockAfterCancellationBooksNothing(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.must(e.svc.HandleReleased(e.ctx, nextID(), usecases.ReleasedInput{AssignmentID: "asg-1", ReleaseNo: 1,
		Facts: volume.Facts{OrderID: "ord-1", GroupNo: 1, NodeID: "n-fc", DepartureID: "dep-1"}}))
	e.must(e.svc.HandleCancelled(e.ctx, nextID(), usecases.AssignmentInput{AssignmentID: "asg-1"}))
	before := e.store.Counts()
	e.lock("asg-1", "n-fc", "dep-1")
	if e.departure("dep-1").Booked() != 0 || e.volume("asg-1").State() != volume.StateCancelled {
		t.Fatal("a cancelled assignment books nothing")
	}
	if after := e.store.Counts(); after.VersionSum != before.VersionSum || after.OutboxEvents != before.OutboxEvents {
		t.Fatalf("counts %+v -> %+v", before, after)
	}
}

func TestLockRetriesAFailedBooking(t *testing.T) {
	e := newEnv(t)
	e.lock("asg-1", "n-fc", "dep-ghost")
	if e.volume("asg-1").State() != volume.StateFailed {
		t.Fatal("setup: failed")
	}
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.lock("asg-1", "n-fc", "dep-1")
	if v := e.volume("asg-1"); v.State() != volume.StateBooked || v.BookedOn() != "dep-1" {
		t.Fatalf("a new lock of a FAILED assignment tries again: %+v", v.Snapshot())
	}
}

func TestLockValidatesItsFacts(t *testing.T) {
	e := newEnv(t)
	bad := lockIn("asg-1", "n-fc", "dep-1")
	bad.Facts.OrderID = ""
	wantErr(t, "facts", e.svc.HandleLocked(e.ctx, nextID(), bad), shared.ErrValidation)
	bad = lockIn("bad id", "n-fc", "dep-1")
	wantErr(t, "assignment id", e.svc.HandleLocked(e.ctx, nextID(), bad), shared.ErrValidation)
	if e.store.Counts().Processed != 0 {
		t.Fatal("a poison message is not claimed: its transaction rolled back")
	}
}

func TestReleasedReassignedCancelled(t *testing.T) {
	e := newEnv(t)
	rel := func(no int, node, dep string) usecases.ReleasedInput {
		return usecases.ReleasedInput{AssignmentID: "asg-1", ReleaseNo: no, Facts: volume.Facts{OrderID: "ord-1", GroupNo: 2, NodeID: node, DepartureID: departure.ID(dep)}}
	}
	e.must(e.svc.HandleReleased(e.ctx, nextID(), rel(2, "n-a", "dep-a")))
	e.must(e.svc.HandleReleased(e.ctx, nextID(), rel(1, "n-old", "dep-old")))
	if v := e.volume("asg-1"); v.NodeID() != "n-a" || v.PlannedDepartureID() != "dep-a" || v.State() != volume.StateReleased {
		t.Fatalf("an older release is ignored: %+v", v.Snapshot())
	}
	e.must(e.svc.HandleReassigned(e.ctx, nextID(), usecases.ReassignedInput{AssignmentID: "asg-1", ToNodeID: "n-b"}))
	if v := e.volume("asg-1"); v.NodeID() != "n-b" || v.PlannedDepartureID() != "" {
		t.Fatalf("reassigned: %+v", v.Snapshot())
	}
	e.must(e.svc.HandleReleased(e.ctx, nextID(), rel(3, "n-b", "dep-b")))
	e.must(e.svc.HandleCancelled(e.ctx, nextID(), usecases.AssignmentInput{AssignmentID: "asg-1"}))
	if e.volume("asg-1").State() != volume.StateCancelled {
		t.Fatal("cancelled")
	}
	versions := e.store.Counts().VersionSum
	e.must(e.svc.HandleCancelled(e.ctx, nextID(), usecases.AssignmentInput{AssignmentID: "asg-1"}))
	if e.store.Counts().VersionSum != versions {
		t.Fatal("cancelling twice changes nothing")
	}
	if len(e.outbox.Events()) != 0 {
		t.Fatal("none of the three publishes anything")
	}
}

func TestAssignmentEventsForAnUnknownAssignmentCreateTheRow(t *testing.T) {
	e := newEnv(t)
	e.must(e.svc.HandleReassigned(e.ctx, nextID(), usecases.ReassignedInput{AssignmentID: "asg-9", ToNodeID: "n-b"}))
	e.must(e.svc.HandleCancelled(e.ctx, nextID(), usecases.AssignmentInput{AssignmentID: "asg-8"}))
	if e.volume("asg-8").State() != volume.StateCancelled {
		t.Fatal("a cancellation is recorded even if the release was missed")
	}
	wantErr(t, "invalid node", e.svc.HandleReassigned(e.ctx, nextID(), usecases.ReassignedInput{AssignmentID: "asg-9", ToNodeID: "bad id"}), shared.ErrValidation)
}

func TestLabels(t *testing.T) {
	e := newEnv(t)
	e.must(e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN1"}))
	e.must(e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN2"}))
	e.must(e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN1"}))
	v := e.volume("asg-1")
	if v.State() != volume.StateUnknown || len(v.TrackingNumbers()) != 2 {
		t.Fatalf("labels: %+v", v.Snapshot())
	}
	e.must(e.svc.HandleLabelVoided(e.ctx, nextID(), usecases.LabelVoidedInput{TrackingNumber: "TN1"}))
	if got := e.volume("asg-1").TrackingNumbers(); len(got) != 1 || got[0] != "TN2" {
		t.Fatalf("after void: %v", got)
	}
	e.must(e.svc.HandleLabelVoided(e.ctx, nextID(), usecases.LabelVoidedInput{TrackingNumber: "TN-unknown"}))
	wantErr(t, "invalid tracking number", e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: " "}), shared.ErrValidation)
	wantErr(t, "invalid assignment", e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "", TrackingNumber: "TN3"}), shared.ErrValidation)
}

func TestVoidedLabelKeepsTheBooking(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.lock("asg-1", "n-fc", "dep-1")
	e.must(e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN1"}))
	e.must(e.svc.HandleLabelVoided(e.ctx, nextID(), usecases.LabelVoidedInput{TrackingNumber: "TN1"}))
	if v := e.volume("asg-1"); v.State() != volume.StateBooked || len(v.TrackingNumbers()) != 0 || e.departure("dep-1").Booked() != 1 {
		t.Fatalf("volume = %+v", v.Snapshot())
	}
}

// ---- cancelling a departure with bookings --------------------------------

func TestCancelDepartureRebooksItsBookings(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.departureAt("dep-2", departure.KindPickup, 6*time.Hour, 2)
	for _, a := range []string{"asg-1", "asg-2", "asg-3"} {
		e.lock(a, "n-fc", "dep-1")
	}
	mark := len(e.outbox.Events())
	_, err := e.svc.CancelDeparture(e.ctx, "dep-1", "planner")
	e.must(err)
	if d := e.departure("dep-1"); d.Booked() != 0 || d.State() != departure.StateCancelled {
		t.Fatalf("the cancelled departure releases its bookings: %+v", d.Snapshot())
	}
	if e.departure("dep-2").Booked() != 2 {
		t.Fatal("dep-2 takes two (its capacity)")
	}
	states := map[volume.State]int{}
	for _, a := range []string{"asg-1", "asg-2", "asg-3"} {
		states[e.volume(a).State()]++
	}
	if states[volume.StateBooked] != 2 || states[volume.StateFailed] != 1 {
		t.Fatalf("states = %v", states)
	}
	var names []string
	for _, ev := range e.outbox.Events()[mark:] {
		names = append(names, ev.EventName())
	}
	sameNames(t, names, []string{"DepartureCancelled", "DepartureCapacityChanged", "BookingMoved", "DepartureCapacityChanged", "BookingMoved", "BookingFailed"})
	failed := eventsOf[volume.BookingFailed](e)[0]
	if failed.Reason != volume.ReasonDepartureCancelled || failed.DepartureID != "dep-1" || failed.AssignmentID != "asg-3" {
		t.Fatalf("failed = %+v", failed)
	}
}

// ---- node events -----------------------------------------------------------

func registerFC(e *env, id string) {
	e.t.Helper()
	e.must(e.svc.HandleNodeRegistered(e.ctx, nextID(), usecases.NodeRegisteredInput{NodeID: id, NodeType: "FULFILLMENT_CENTER", Operator: "VENDOR", Timezone: zone}))
}

func TestPickupsAppearWhateverTheArrivalOrder(t *testing.T) {
	steps := map[string]func(e *env){
		"sc": func(e *env) {
			e.must(e.svc.HandleNodeRegistered(e.ctx, nextID(), usecases.NodeRegisteredInput{NodeID: "n-sc", NodeType: "SORTATION_CENTER", Operator: "NETWORK", Timezone: zone}))
		},
		"fc": func(e *env) { registerFC(e, "n-fc") },
		"profile": func(e *env) {
			e.must(e.svc.HandleProfileDeclared(e.ctx, nextID(), usecases.ProfileDeclaredInput{NodeID: "n-fc", Profile: standardProfile()}))
		},
		"activate": func(e *env) { e.must(e.svc.HandleNodeActivated(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"})) },
		"lane": func(e *env) {
			e.must(e.svc.HandleLaneOpened(e.ctx, nextID(), usecases.LaneOpenedInput{LaneID: "lane-fc", OriginNodeID: "n-fc", DestNodeID: "n-sc", Mode: "GROUND", TransitMinutes: 120}))
		},
	}
	orders := [][]string{
		{"sc", "fc", "profile", "activate", "lane"},
		{"lane", "activate", "profile", "fc", "sc"},
		{"activate", "lane", "sc", "profile", "fc"},
		{"profile", "sc", "lane", "fc", "activate"},
	}
	for _, order := range orders {
		e := newEnv(t)
		for _, s := range order {
			steps[s](e)
		}
		if got := len(e.listAll(repository.DepartureFilter{Kind: departure.KindPickup, State: departure.StateScheduled})); got != 10 {
			t.Errorf("%v: %d pickups, want 10", order, got)
		}
		sameNames(t, e.eventNames(), repeat("DepartureScheduled", 10))
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestNodeEventsRefuseInvalidData(t *testing.T) {
	e := newEnv(t)
	wantErr(t, "type", e.svc.HandleNodeRegistered(e.ctx, nextID(), usecases.NodeRegisteredInput{NodeID: "n-1", NodeType: "DEPOT", Operator: "VENDOR", Timezone: zone}), shared.ErrValidation)
	wantErr(t, "node id", e.svc.HandleNodeActivated(e.ctx, nextID(), usecases.NodeInput{NodeID: "bad id"}), shared.ErrValidation)
	bad := standardProfile()
	bad.Timezone = "Mars/Base"
	wantErr(t, "profile", e.svc.HandleProfileDeclared(e.ctx, nextID(), usecases.ProfileDeclaredInput{NodeID: "n-1", Profile: bad}), shared.ErrValidation)
	wantErr(t, "lane", e.svc.HandleLaneOpened(e.ctx, nextID(), usecases.LaneOpenedInput{LaneID: "l", OriginNodeID: "a", DestNodeID: "b", Mode: "RAIL", TransitMinutes: 5}), shared.ErrValidation)
	if e.store.Counts().Nodes != 0 || e.store.Counts().Processed != 0 {
		t.Fatalf("poison stores nothing: %+v", e.store.Counts())
	}
}

func TestAnOldProfileRevisionIsIgnored(t *testing.T) {
	e := newEnv(t)
	e.pickupWorld()
	e.generate()
	before := e.store.Counts()
	old := standardProfile() // revision 1, the stored one
	e.must(e.svc.HandleProfileDeclared(e.ctx, nextID(), usecases.ProfileDeclaredInput{NodeID: "n-fc", Profile: old}))
	if after := e.store.Counts(); after.OutboxEvents != before.OutboxEvents || after.Departures != before.Departures {
		t.Fatalf("counts %+v -> %+v", before, after)
	}
}

func TestProfileRevisionReplacesUnbookedPickups(t *testing.T) {
	e := newEnv(t)
	e.pickupWorld()
	e.generate()
	pickups := e.listAll(repository.DepartureFilter{Kind: departure.KindPickup, State: departure.StateScheduled})
	e.lock("asg-1", "n-fc", string(pickups[0].ID())) // the first pickup holds a booking
	mark := len(e.outbox.Events())
	next := standardProfile()
	next.Revision = 2
	next.Cutoffs = []network.Cutoff{{LocalTime: "19:00"}}
	e.must(e.svc.HandleProfileDeclared(e.ctx, nextID(), usecases.ProfileDeclaredInput{NodeID: "n-fc", Profile: next}))
	cancelled := e.listAll(repository.DepartureFilter{State: departure.StateCancelled})
	if len(cancelled) != 9 {
		t.Fatalf("cancelled = %d, want the 9 unbooked old pickups", len(cancelled))
	}
	for _, c := range cancelled {
		if c.ID() == pickups[0].ID() {
			t.Fatal("the booked pickup stays")
		}
	}
	scheduled := 0
	for _, ev := range e.outbox.Events()[mark:] {
		switch v := ev.(type) {
		case departure.DepartureCancelled:
			if v.Reason != usecases.ReasonProfileRevised {
				t.Fatalf("reason = %s", v.Reason)
			}
		case departure.DepartureScheduled:
			scheduled++
		}
	}
	if scheduled != 10 {
		t.Fatalf("new pickups = %d", scheduled)
	}
}

func TestSuspendedNodeCancelsItsFuturePickupsAndRebooks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		handle func(e *env) error
		reason string
		status network.Status
	}{
		{"suspended", func(e *env) error {
			return e.svc.HandleNodeSuspended(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"})
		}, usecases.ReasonNodeSuspended, network.StatusSuspended},
		{"retired", func(e *env) error {
			return e.svc.HandleNodeRetired(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"})
		}, usecases.ReasonNodeRetired, network.StatusRetired},
	} {
		e := newEnv(t)
		e.pickupWorld()
		e.generate()
		first := e.listAll(repository.DepartureFilter{State: departure.StateScheduled})[0]
		e.lock("asg-1", "n-fc", string(first.ID()))
		e.must(tc.handle(e))
		if got := len(e.listAll(repository.DepartureFilter{State: departure.StateScheduled})); got != 0 {
			t.Errorf("%s: %d pickups left", tc.name, got)
		}
		if got := len(e.listAll(repository.DepartureFilter{State: departure.StateCancelled})); got != 10 {
			t.Errorf("%s: cancelled = %d", tc.name, got)
		}
		failed := eventsOf[volume.BookingFailed](e)
		if len(failed) != 1 || failed[0].Reason != volume.ReasonDepartureCancelled || failed[0].AssignmentID != "asg-1" {
			t.Errorf("%s: failed = %+v", tc.name, failed)
		}
		for _, ev := range eventsOf[departure.DepartureCancelled](e) {
			if ev.Reason != tc.reason {
				t.Errorf("%s: reason = %s", tc.name, ev.Reason)
			}
		}
		n, _ := e.store.Nodes().Get(e.ctx, "n-fc")
		if n.Status != tc.status {
			t.Errorf("%s: status = %s", tc.name, n.Status)
		}
		before := e.store.Counts()
		e.must(tc.handle(e))
		if e.store.Counts().OutboxEvents != before.OutboxEvents {
			t.Errorf("%s: repeating changes nothing", tc.name)
		}
	}
}

func TestReactivatedNodeGetsFreshPickups(t *testing.T) {
	e := newEnv(t)
	e.pickupWorld()
	e.generate()
	e.must(e.svc.HandleNodeSuspended(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"}))
	e.must(e.svc.HandleNodeActivated(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"}))
	if got := len(e.listAll(repository.DepartureFilter{State: departure.StateScheduled})); got != 10 {
		t.Fatalf("scheduled = %d, want 10 new ones (new generation, new ids)", got)
	}
	if got := len(e.listAll(repository.DepartureFilter{State: departure.StateCancelled})); got != 10 {
		t.Fatalf("cancelled = %d", got)
	}
}

func TestLaneEvents(t *testing.T) {
	e := newEnv(t)
	open := usecases.LaneOpenedInput{LaneID: "lane-1", OriginNodeID: "n-a", DestNodeID: "n-b", Mode: "GROUND", TransitMinutes: 90}
	e.must(e.svc.HandleLaneOpened(e.ctx, nextID(), open))
	if l, err := e.store.Lanes().Get(e.ctx, "lane-1"); err != nil || !l.Open || l.TransitMinutes != 90 {
		t.Fatalf("lane = %+v %v", l, err)
	}
	e.must(e.svc.HandleLaneClosed(e.ctx, nextID(), usecases.LaneInput{LaneID: "lane-1"}))
	if l, _ := e.store.Lanes().Get(e.ctx, "lane-1"); l.Open {
		t.Fatal("closed")
	}
	e.must(e.svc.HandleLaneOpened(e.ctx, nextID(), open))
	if l, _ := e.store.Lanes().Get(e.ctx, "lane-1"); l.Open {
		t.Fatal("a closed lane is not reopened by a replayed LaneOpened")
	}
	before := e.store.Counts()
	e.must(e.svc.HandleLaneClosed(e.ctx, nextID(), usecases.LaneInput{LaneID: "lane-1"}))
	e.must(e.svc.HandleLaneClosed(e.ctx, nextID(), usecases.LaneInput{LaneID: "lane-ghost"}))
	if e.store.Counts().Lanes != before.Lanes {
		t.Fatal("closing a closed or unknown lane stores nothing")
	}
}

// ---- infrastructure behaviour ----------------------------------------------

type failingOutbox struct{}

func (failingOutbox) Append(context.Context, ...shared.Event) error {
	return errors.New("kafka outbox down")
}

func TestAFailedOutboxRollsTheWholeUnitBack(t *testing.T) {
	e := newEnv(t, func(d *usecases.Deps) { d.Outbox = failingOutbox{} })
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	before := e.store.Counts()
	err := e.svc.HandleLocked(e.ctx, "evt-1", lockIn("asg-1", "n-fc", "dep-1"))
	if err == nil {
		t.Fatal("the outbox error must surface")
	}
	e.unchanged(before)
	if e.departure("dep-1").Booked() != 0 {
		t.Fatal("the booking rolled back")
	}
}

type conflictingVolumes struct {
	ports.VolumeRepository
	failures int
	calls    int
}

func (c *conflictingVolumes) Save(ctx context.Context, v *volume.ExpectedVolume) error {
	c.calls++
	if c.calls <= c.failures {
		return ports.ErrConcurrentUpdate
	}
	return c.VolumeRepository.Save(ctx, v)
}

func TestConsumersRetryOptimisticConflicts(t *testing.T) {
	flaky := &conflictingVolumes{failures: 2}
	e := newEnv(t, func(d *usecases.Deps) { flaky.VolumeRepository = d.Volumes; d.Volumes = flaky })
	e.must(e.svc.HandleLabelIssued(e.ctx, "evt-1", usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN1"}))
	if flaky.calls != 3 || len(e.volume("asg-1").TrackingNumbers()) != 1 {
		t.Fatalf("calls = %d", flaky.calls)
	}
	stuck := &conflictingVolumes{failures: 99}
	e2 := newEnv(t, func(d *usecases.Deps) { stuck.VolumeRepository = d.Volumes; d.Volumes = stuck })
	wantErr(t, "persistent conflict", e2.svc.HandleLabelIssued(e2.ctx, "evt-2", usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN1"}), ports.ErrConcurrentUpdate)
	if stuck.calls != usecases.MaxConflictRetries {
		t.Fatalf("attempts = %d, want %d", stuck.calls, usecases.MaxConflictRetries)
	}
	if e2.store.Counts().Processed != 0 {
		t.Fatal("the failed attempts must not claim the event")
	}
}
