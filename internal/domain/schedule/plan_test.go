package schedule

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// Friday 2026-10-23 12:00 UTC is 09:00 in Sao Paulo (UTC-3, no DST).
var now = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func scNode(id, zone string) network.NodeView {
	return network.NodeView{ID: id, Type: network.SortationCenter, Operator: network.OperatorNetwork, Timezone: zone, Status: network.StatusActive}
}

func groundLane() network.LaneView {
	return network.LaneView{ID: "lane-1", Origin: "n-a", Destination: "n-b", Mode: network.ModeGround, TransitMinutes: 120, Open: true}
}

func fridaySchedule(t *testing.T) *LaneSchedule {
	t.Helper()
	s, err := Define("lane-1", []Slot{slot(time.Friday, "18:00", "18:30", "22:45", 80)})
	wantNoErr(t, "define", err)
	return s
}

func TestPlanLaneExpandsTheHorizon(t *testing.T) {
	origin, dest := scNode("n-a", "America/Sao_Paulo"), scNode("n-b", "America/Sao_Paulo")
	specs, err := PlanLane(fridaySchedule(t), groundLane(), origin, dest, now)
	wantNoErr(t, "plan", err)
	if len(specs) != 2 {
		t.Fatalf("two Fridays in 14 days, got %d", len(specs))
	}
	wantCutoff := time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC)
	assertFridaySpec(t, specs[0], wantCutoff)
	if !specs[1].CutoffAt.Equal(wantCutoff.AddDate(0, 0, 7)) || specs[0].ID == specs[1].ID {
		t.Fatalf("second spec = %+v", specs[1])
	}
	if want := DepartureID("lane-1", departure.KindLinehaul, wantCutoff, 0); specs[0].ID != want {
		t.Fatalf("id = %s, want %s", specs[0].ID, want)
	}
	again, _ := PlanLane(fridaySchedule(t), groundLane(), origin, dest, now)
	if again[0].ID != specs[0].ID {
		t.Fatal("planning twice must give the same ids")
	}
}

func assertFridaySpec(t *testing.T, first departure.Spec, wantCutoff time.Time) {
	t.Helper()
	if !first.CutoffAt.Equal(wantCutoff) || !first.DepartsAt.Equal(wantCutoff.Add(30*time.Minute)) ||
		!first.ArrivesAt.Equal(time.Date(2026, 10, 24, 1, 45, 0, 0, time.UTC)) {
		t.Fatalf("times = %v %v %v", first.CutoffAt, first.DepartsAt, first.ArrivesAt)
	}
	if first.Kind != departure.KindLinehaul || first.LaneID != "lane-1" || first.OriginNodeID != "n-a" || first.DestinationNodeID != "n-b" ||
		first.OriginType != network.SortationCenter || first.DestinationType != network.SortationCenter || first.CapacityPackages != 80 {
		t.Fatalf("spec = %+v", first)
	}
}

func TestPlanLaneSkipsCutoffsNotAfterNow(t *testing.T) {
	cutoff := time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Time
		want int
	}{{cutoff.Add(-time.Second), 2}, {cutoff, 1}, {cutoff.Add(time.Second), 1}} {
		specs, err := PlanLane(fridaySchedule(t), groundLane(), scNode("n-a", "America/Sao_Paulo"), scNode("n-b", "America/Sao_Paulo"), c.at)
		wantNoErr(t, "plan", err)
		if len(specs) != c.want {
			t.Errorf("now %v: %d specs, want %d", c.at, len(specs), c.want)
		}
	}
}

func TestPlanLaneWindowIsFourteenDays(t *testing.T) {
	s, _ := Define("lane-1", []Slot{slot(time.Friday, "18:00", "18:30", "22:45", 1), slot(time.Thursday, "18:00", "18:30", "22:45", 1)})
	n := scNode("n-a", "UTC")
	// now is Friday 12:00 UTC: Fridays Oct 23 and 30, Thursdays Oct 29 and Nov 5 (day 13) fit, Friday Nov 6 (day 14) does not.
	specs, err := PlanLane(s, groundLane(), n, scNode("n-b", "UTC"), now)
	wantNoErr(t, "plan", err)
	if len(specs) != 4 {
		t.Fatalf("specs = %d, want 4", len(specs))
	}
	last := specs[len(specs)-1].CutoffAt
	if !last.Equal(time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)) {
		t.Fatalf("last cutoff = %v", last)
	}
}

func TestPlanLaneKindFollowsTheLaneMode(t *testing.T) {
	air := groundLane()
	air.Mode = network.ModeAir
	specs, _ := PlanLane(fridaySchedule(t), air, scNode("n-a", "UTC"), scNode("n-b", "UTC"), now)
	if specs[0].Kind != departure.KindAir {
		t.Fatalf("kind = %s", specs[0].Kind)
	}
}

func TestPlanLaneArrivalUsesTheDestinationZone(t *testing.T) {
	s, _ := Define("lane-1", []Slot{slot(time.Friday, "18:00", "18:30", "22:45", 1)})
	// destination in UTC: 22:45 UTC the same evening is the first instant after the 21:30 UTC departure.
	specs, _ := PlanLane(s, groundLane(), scNode("n-a", "America/Sao_Paulo"), scNode("n-b", "UTC"), now)
	if !specs[0].ArrivesAt.Equal(time.Date(2026, 10, 23, 22, 45, 0, 0, time.UTC)) {
		t.Fatalf("arrives = %v", specs[0].ArrivesAt)
	}
	// an arrival wall time equal to the departure instant means the next day.
	s2, _ := Define("lane-1", []Slot{slot(time.Friday, "18:00", "18:30", "18:30", 1)})
	specs, _ = PlanLane(s2, groundLane(), scNode("n-a", "UTC"), scNode("n-b", "UTC"), now)
	if !specs[0].ArrivesAt.Equal(time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC)) {
		t.Fatalf("arrives = %v", specs[0].ArrivesAt)
	}
}

func TestPlanLaneYieldsNothingWhenItShould(t *testing.T) {
	retired := fridaySchedule(t)
	_ = retired.Retire()
	vendorFC := scNode("n-a", "UTC")
	vendorFC.Type, vendorFC.Operator = network.FulfillmentCenter, network.OperatorVendor
	networkFC := vendorFC
	networkFC.Operator = network.OperatorNetwork
	cases := []struct {
		name         string
		s            *LaneSchedule
		origin, dest network.NodeView
		want         int
	}{
		{"retired", retired, scNode("n-a", "UTC"), scNode("n-b", "UTC"), 0},
		{"vendor fulfillment center origin", fridaySchedule(t), vendorFC, scNode("n-b", "UTC"), 0},
		{"network-operated fulfillment center origin", fridaySchedule(t), networkFC, scNode("n-b", "UTC"), 2},
		{"slot on another day", mustDefine(t, slot(time.Sunday, "10:00", "11:00", "12:00", 1)), scNode("n-a", "UTC"), scNode("n-b", "UTC"), 2},
	}
	for _, c := range cases {
		specs, err := PlanLane(c.s, groundLane(), c.origin, c.dest, now)
		wantNoErr(t, c.name, err)
		if len(specs) != c.want {
			t.Errorf("%s: %d specs, want %d", c.name, len(specs), c.want)
		}
	}
}

func mustDefine(t *testing.T, sl ...Slot) *LaneSchedule {
	t.Helper()
	s, err := Define("lane-1", sl)
	wantNoErr(t, "define", err)
	return s
}

func TestPlanLaneNeedsKnownNodes(t *testing.T) {
	unknownZone := scNode("n-a", "")
	unknownType := scNode("n-a", "UTC")
	unknownType.Type = ""
	for name, tc := range map[string][2]network.NodeView{
		"origin zone": {unknownZone, scNode("n-b", "UTC")}, "origin type": {unknownType, scNode("n-b", "UTC")},
		"destination zone": {scNode("n-a", "UTC"), unknownZone},
	} {
		_, err := PlanLane(fridaySchedule(t), groundLane(), tc[0], tc[1], now)
		if !errors.Is(err, ErrNodeUnknown) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	_, err := PlanLane(fridaySchedule(t), groundLane(), scNode("n-a", "Mars/Base"), scNode("n-b", "UTC"), now)
	wantErr(t, "bad origin zone", err, shared.ErrValidation)
	_, err = PlanLane(fridaySchedule(t), groundLane(), scNode("n-a", "UTC"), scNode("n-b", "Mars/Base"), now)
	wantErr(t, "bad destination zone", err, shared.ErrValidation)
}

func TestDepartureIDIsStableAndDistinct(t *testing.T) {
	c := time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC)
	a := DepartureID("l", departure.KindPickup, c, 0)
	if a != DepartureID("l", departure.KindPickup, c.In(time.FixedZone("x", 3600)), 0) {
		t.Fatal("the id depends on the instant, not on its zone")
	}
	for name, other := range map[string]departure.ID{
		"lane": DepartureID("m", departure.KindPickup, c, 0), "kind": DepartureID("l", departure.KindAir, c, 0),
		"cutoff": DepartureID("l", departure.KindPickup, c.Add(time.Minute), 0), "generation": DepartureID("l", departure.KindPickup, c, 1),
	} {
		if other == a {
			t.Errorf("a different %s must give a different id", name)
		}
	}
}

func isErr(err, target error) bool { return errors.Is(err, target) }
