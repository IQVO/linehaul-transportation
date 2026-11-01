package repocontract

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
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
