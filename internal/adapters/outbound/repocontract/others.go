package repocontract

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
)

var scheduleCases = []named{
	{"a saved schedule round-trips and a miss is ErrNotFound", schedRoundTrip},
	{"versions: create once, update from the previous version", schedVersions},
	{"ListActive returns the ACTIVE schedules by lane id", schedListActive},
}

func schedRoundTrip(t *testing.T, f Fixture) {
	ctx := context.Background()
	s := NewSchedule(t, "lane-1")
	noErr(t, f.Schedules.Save(ctx, s))
	got, err := f.Schedules.Get(ctx, "lane-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), s.Snapshot()) {
		t.Fatalf("round trip: %+v != %+v", got.Snapshot(), s.Snapshot())
	}
	_, err = f.Schedules.Get(ctx, "lane-9")
	wantErr(t, err, ports.ErrNotFound)
}

func schedVersions(t *testing.T, f Fixture) {
	ctx := context.Background()
	s := NewSchedule(t, "lane-1")
	noErr(t, f.Schedules.Save(ctx, s))
	wantErr(t, f.Schedules.Save(ctx, NewSchedule(t, "lane-1")), ports.ErrConcurrentUpdate)
	noErr(t, s.Redefine([]schedule.Slot{{Day: time.Sunday, Cutoff: "01:00", Departs: "02:00", Arrives: "03:00", CapacityPackages: 1}}))
	noErr(t, f.Schedules.Save(ctx, s))
	stale := NewSchedule(t, "lane-1")
	noErr(t, stale.Retire()) // version 2 again
	wantErr(t, f.Schedules.Save(ctx, stale), ports.ErrConcurrentUpdate)
	got, _ := f.Schedules.Get(ctx, "lane-1")
	if got.Version() != 2 || len(got.Slots()) != 1 || got.Slots()[0].Day != time.Sunday {
		t.Fatalf("stored = %+v", got.Snapshot())
	}
}

func schedListActive(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, id := range []string{"lane-b", "lane-a", "lane-c"} {
		noErr(t, f.Schedules.Save(ctx, NewSchedule(t, id)))
	}
	c, _ := f.Schedules.Get(ctx, "lane-c")
	noErr(t, c.Retire())
	noErr(t, f.Schedules.Save(ctx, c))
	got, err := f.Schedules.ListActive(ctx)
	noErr(t, err)
	var order []string
	for _, s := range got {
		order = append(order, s.LaneID())
	}
	if strings.Join(order, " ") != "lane-a lane-b" {
		t.Fatalf("active = %v", order)
	}
}

var loadCases = []named{
	{"a saved load round-trips with its tracking numbers in order", loadRoundTrip},
	{"GetByDeparture and a miss", loadByDeparture},
	{"FindActiveByTracking sees OPEN and CLOSED loads only", loadFindActive},
	{"a tracking number is on one active load: ErrPackageAlreadyLoaded", loadUniquePackage},
	{"a departure has one load: ErrLoadAlreadyExists", loadUniqueDeparture},
	{"versions: create once, update from the previous version", loadVersions},
}

func saveLoadOn(t *testing.T, f Fixture, depID, loadID string, tns ...string) *load.Load {
	t.Helper()
	ctx := context.Background()
	d := NewDeparture(t, depID, "n-fc", departure.KindLinehaul, time.Hour, 10)
	noErr(t, f.Departures.Save(ctx, d))
	l := NewLoad(t, loadID, d)
	noErr(t, f.Loads.Save(ctx, l))
	for _, tn := range tns {
		_, err := l.AddPackage(tn, false, departure.StateScheduled)
		noErr(t, err)
		noErr(t, f.Loads.Save(ctx, l))
	}
	return l
}

func loadRoundTrip(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1", "TN3", "TN1", "TN2")
	got, err := f.Loads.Get(ctx, "load-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), l.Snapshot()) {
		t.Fatalf("round trip: %+v != %+v", got.Snapshot(), l.Snapshot())
	}
	if strings.Join(got.Snapshot().TrackingNumbers, ",") != "TN3,TN1,TN2" {
		t.Fatalf("order lost: %v", got.Snapshot().TrackingNumbers)
	}
	empty := saveLoadOn(t, f, "dep-2", "load-2")
	got, err = f.Loads.Get(ctx, "load-2")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), empty.Snapshot()) || got.Snapshot().TrackingNumbers == nil {
		t.Fatalf("empty load: %+v", got.Snapshot())
	}
	_, err = f.Loads.Get(ctx, "load-9")
	wantErr(t, err, ports.ErrNotFound)
}

func loadByDeparture(t *testing.T, f Fixture) {
	ctx := context.Background()
	saveLoadOn(t, f, "dep-1", "load-1")
	got, err := f.Loads.GetByDeparture(ctx, "dep-1")
	noErr(t, err)
	if got.ID() != "load-1" {
		t.Fatalf("load = %s", got.ID())
	}
	_, err = f.Loads.GetByDeparture(ctx, "dep-9")
	wantErr(t, err, ports.ErrNotFound)
}

func loadFindActive(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1", "TN1")
	id, err := f.Loads.FindActiveByTracking(ctx, "TN1")
	noErr(t, err)
	if id != "load-1" {
		t.Fatalf("id = %s", id)
	}
	_, err = f.Loads.FindActiveByTracking(ctx, "TN-none")
	wantErr(t, err, ports.ErrNotFound)
	d, _ := f.Departures.Get(ctx, "dep-1")
	_, err = l.Close(departure.StateScheduled, at)
	noErr(t, err)
	noErr(t, f.Loads.Save(ctx, l))
	if _, err := f.Loads.FindActiveByTracking(ctx, "TN1"); err != nil {
		t.Fatalf("a CLOSED load is active: %v", err)
	}
	noErr(t, d.Close())
	_, err = l.Dispatch(at)
	noErr(t, err)
	noErr(t, f.Loads.Save(ctx, l))
	_, err = f.Loads.FindActiveByTracking(ctx, "TN1")
	wantErr(t, err, ports.ErrNotFound)
}

func loadUniquePackage(t *testing.T, f Fixture) {
	ctx := context.Background()
	saveLoadOn(t, f, "dep-1", "load-1", "TN1")
	other := saveLoadOn(t, f, "dep-2", "load-2")
	_, err := other.AddPackage("TN1", false, departure.StateScheduled) // the use case would pass true; the index is the backstop
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, other), load.ErrPackageAlreadyLoaded)
	got, _ := f.Loads.Get(ctx, "load-2")
	if got.PackageCount() != 0 {
		t.Fatal("the refused save must leave the stored load unchanged")
	}
}

func loadUniqueDeparture(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1")
	d, _ := f.Departures.Get(ctx, "dep-1")
	second, err := load.Open("load-2", d, false)
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, second), load.ErrLoadAlreadyExists)
	_ = l
}

func loadVersions(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1")
	d, _ := f.Departures.Get(ctx, "dep-1")
	dup, err := load.Open("load-1", d, false)
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, dup), ports.ErrConcurrentUpdate)
	stale, _ := f.Loads.Get(ctx, "load-1")
	_, err = l.AddPackage("TN1", false, departure.StateScheduled)
	noErr(t, err)
	noErr(t, f.Loads.Save(ctx, l))
	_, err = stale.AddPackage("TN2", false, departure.StateScheduled)
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, stale), ports.ErrConcurrentUpdate)
}

var viewCases = []named{
	{"a node view round-trips with its profile and a miss is ErrNotFound", nodeRoundTrip},
	{"saving a node view replaces it", nodeReplace},
	{"ListPickupEligible returns the eligible nodes by id", nodeEligible},
	{"a lane view round-trips, replaces and is listed by origin and destination", laneViews},
}

func nodeRoundTrip(t *testing.T, f Fixture) {
	ctx := context.Background()
	n := NewNodeView("n-fc")
	noErr(t, f.Nodes.Save(ctx, n))
	got, err := f.Nodes.Get(ctx, "n-fc")
	noErr(t, err)
	if !reflect.DeepEqual(got, n) {
		t.Fatalf("round trip:\n%+v\n%+v", got, n)
	}
	bare := network.NewNodeView("n-new")
	noErr(t, f.Nodes.Save(ctx, bare))
	got, err = f.Nodes.Get(ctx, "n-new")
	noErr(t, err)
	if got.Profile != nil || got.Status != network.StatusProposed || got.Type != "" {
		t.Fatalf("bare view: %+v", got)
	}
	_, err = f.Nodes.Get(ctx, "n-9")
	wantErr(t, err, ports.ErrNotFound)
}

func nodeReplace(t *testing.T, f Fixture) {
	ctx := context.Background()
	n := NewNodeView("n-fc")
	noErr(t, f.Nodes.Save(ctx, n))
	n.Status = network.StatusSuspended
	n.Generation = 3
	n.Profile = nil
	noErr(t, f.Nodes.Save(ctx, n))
	got, _ := f.Nodes.Get(ctx, "n-fc")
	if got.Status != network.StatusSuspended || got.Generation != 3 || got.Profile != nil {
		t.Fatalf("replaced: %+v", got)
	}
}

func nodeEligible(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, id := range []string{"n-b", "n-a"} {
		noErr(t, f.Nodes.Save(ctx, NewNodeView(id)))
	}
	suspended := NewNodeView("n-s")
	suspended.Status = network.StatusSuspended
	noErr(t, f.Nodes.Save(ctx, suspended))
	noErr(t, f.Nodes.Save(ctx, network.NodeView{ID: "n-sc", Type: network.SortationCenter, Operator: network.OperatorNetwork, Timezone: "UTC", Status: network.StatusActive}))
	got, err := f.Nodes.ListPickupEligible(ctx)
	noErr(t, err)
	var order []string
	for _, n := range got {
		order = append(order, n.ID)
	}
	if strings.Join(order, " ") != "n-a n-b" {
		t.Fatalf("eligible = %v", order)
	}
}

func laneViews(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, l := range []network.LaneView{
		{ID: "l-2", Origin: "a", Destination: "c", Mode: network.ModeAir, TransitMinutes: 90, Open: false},
		{ID: "l-1", Origin: "a", Destination: "b", Mode: network.ModeGround, TransitMinutes: 60, Open: true},
		{ID: "l-3", Origin: "b", Destination: "c", Mode: network.ModeGround, TransitMinutes: 30, Open: true},
	} {
		noErr(t, f.Lanes.Save(ctx, l))
	}
	got, err := f.Lanes.Get(ctx, "l-2")
	noErr(t, err)
	if got != (network.LaneView{ID: "l-2", Origin: "a", Destination: "c", Mode: network.ModeAir, TransitMinutes: 90, Open: false}) {
		t.Fatalf("lane = %+v", got)
	}
	_, err = f.Lanes.Get(ctx, "l-9")
	wantErr(t, err, ports.ErrNotFound)
	ids := func(ls []network.LaneView) string {
		var out []string
		for _, l := range ls {
			out = append(out, l.ID)
		}
		return strings.Join(out, " ")
	}
	byOrigin, _ := f.Lanes.ListByOrigin(ctx, "a")
	byDest, _ := f.Lanes.ListByDestination(ctx, "c")
	none, err := f.Lanes.ListByOrigin(ctx, "zzz")
	noErr(t, err)
	if ids(byOrigin) != "l-1 l-2" || ids(byDest) != "l-2 l-3" || len(none) != 0 || none == nil {
		t.Fatalf("lists: %q %q %v", ids(byOrigin), ids(byDest), none)
	}
	closed := got
	closed.Open = true
	noErr(t, f.Lanes.Save(ctx, closed))
	again, _ := f.Lanes.Get(ctx, "l-2")
	if !again.Open {
		t.Fatal("replace")
	}
}

var volumeCases = []named{
	{"a saved row round-trips and a miss is ErrNotFound", volRoundTrip},
	{"versions: one save per unit of work, from the previous version", volVersions},
	{"FindByTracking and ListBookedOn", volQueries},
}

func volRoundTrip(t *testing.T, f Fixture) {
	ctx := context.Background()
	v := NewVolume(t, "asg-1", "n-fc", "dep-1")
	_, _ = v.AddTracking("TN1")
	_, _ = v.AddTracking("TN2")
	noErr(t, v.Book("dep-1"))
	noErr(t, f.Volumes.Save(ctx, v))
	got, err := f.Volumes.Get(ctx, "asg-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), v.Snapshot()) {
		t.Fatalf("round trip: %+v != %+v", got.Snapshot(), v.Snapshot())
	}
	_, err = f.Volumes.Get(ctx, "asg-9")
	wantErr(t, err, ports.ErrNotFound)
}

func volVersions(t *testing.T, f Fixture) {
	ctx := context.Background()
	v := NewVolume(t, "asg-1", "n-fc", "dep-1")
	noErr(t, f.Volumes.Save(ctx, v))
	wantErr(t, f.Volumes.Save(ctx, NewVolume(t, "asg-1", "n-fc", "dep-1")), ports.ErrConcurrentUpdate)
	a, _ := f.Volumes.Get(ctx, "asg-1")
	b, _ := f.Volumes.Get(ctx, "asg-1")
	_, _ = a.AddTracking("TN1")
	noErr(t, f.Volumes.Save(ctx, a))
	_, _ = b.AddTracking("TN2")
	wantErr(t, f.Volumes.Save(ctx, b), ports.ErrConcurrentUpdate)
	got, _ := f.Volumes.Get(ctx, "asg-1")
	if got.Version() != 2 || strings.Join(got.TrackingNumbers(), ",") != "TN1" {
		t.Fatalf("stored = %+v", got.Snapshot())
	}
}

func volQueries(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, c := range []struct{ asg, dep, tn string }{{"asg-b", "dep-1", "TN-b"}, {"asg-a", "dep-1", "TN-a"}, {"asg-c", "dep-2", "TN-c"}} {
		v := NewVolume(t, c.asg, "n-fc", c.dep)
		_, _ = v.AddTracking(c.tn)
		noErr(t, v.Book(departure.ID(c.dep)))
		noErr(t, f.Volumes.Save(ctx, v))
	}
	cancelled, _ := f.Volumes.Get(ctx, "asg-b")
	cancelled.Cancel()
	noErr(t, f.Volumes.Save(ctx, cancelled))
	got, err := f.Volumes.FindByTracking(ctx, "TN-c")
	noErr(t, err)
	if got.AssignmentID() != "asg-c" {
		t.Fatalf("found %s", got.AssignmentID())
	}
	_, err = f.Volumes.FindByTracking(ctx, "TN-none")
	wantErr(t, err, ports.ErrNotFound)
	booked, err := f.Volumes.ListBookedOn(ctx, "dep-1")
	noErr(t, err)
	if len(booked) != 1 || booked[0].AssignmentID() != "asg-a" {
		t.Fatalf("a cancelled row is not BOOKED: %d rows", len(booked))
	}
	none, err := f.Volumes.ListBookedOn(ctx, "dep-9")
	noErr(t, err)
	if none == nil || len(none) != 0 {
		t.Fatalf("no rows is an empty list: %v", none)
	}
}

var inboxCases = []named{
	{"Claim is true once per consumer and event id", inboxClaim},
}

func inboxClaim(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, c := range []struct {
		consumer, id string
		want         bool
	}{
		{"node-topology", "evt-1", true}, {"node-topology", "evt-1", false}, {"order-sourcing", "evt-1", true},
		{"node-topology", "evt-2", true}, {"order-sourcing", "evt-1", false},
	} {
		got, err := f.Inbox.Claim(ctx, c.consumer, c.id, at)
		noErr(t, err)
		if got != c.want {
			t.Errorf("Claim(%s, %s) = %v, want %v", c.consumer, c.id, got, c.want)
		}
	}
}

var errBoom = errors.New("boom")
