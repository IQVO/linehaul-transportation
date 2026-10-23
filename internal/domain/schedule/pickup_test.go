package schedule

import (
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func vendorNode(p network.Profile) network.NodeView {
	return network.NodeView{
		ID: "n-fc", Type: network.FulfillmentCenter, Operator: network.OperatorVendor, Timezone: p.Timezone,
		Status: network.StatusActive, Generation: 1, Profile: &p,
	}
}

func weekHours() map[string]network.Hours {
	h := map[string]network.Hours{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday"} {
		h[d] = network.Hours{Start: "06:00", End: "22:00"}
	}
	h["saturday"] = network.Hours{Start: "08:00", End: "14:00"}
	return h
}

func baseProfile() network.Profile {
	return network.Profile{
		Revision: 1, Timezone: "America/Sao_Paulo", Hours: weekHours(), Holidays: []string{"2026-10-26"},
		Cutoffs: []network.Cutoff{
			{LocalTime: "11:00"},
			{LocalTime: "18:00", Days: []string{"monday", "tuesday", "wednesday", "thursday", "friday"}, MaxOrders: 50},
		},
		Cap: network.ThroughputCap{Value: 10, TimeUnit: network.CapHour},
	}
}

func pickupIn(p network.Profile, ppo int) PickupInput {
	return PickupInput{
		Node: vendorNode(p), Lane: groundLane(), DestinationType: network.SortationCenter, PackagesPerOrder: ppo,
	}
}

func TestPlanPickupsOverTheHorizon(t *testing.T) {
	specs, err := PlanPickups(pickupIn(baseProfile(), 2), now)
	wantNoErr(t, "plan", err)
	if len(specs) != 20 {
		t.Fatalf("specs = %d, want 20 (the holiday Monday and both Sundays are skipped)", len(specs))
	}
	assertFirstPickup(t, specs[0])
	assertDistinctOpenDays(t, specs)
}

func assertFirstPickup(t *testing.T, first departure.Spec) {
	t.Helper()
	cutoff := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	if first.Kind != departure.KindPickup || first.LaneID != "lane-1" || first.OriginNodeID != "n-fc" || first.DestinationNodeID != "n-b" ||
		first.OriginType != network.FulfillmentCenter || first.DestinationType != network.SortationCenter {
		t.Fatalf("spec = %+v", first)
	}
	if !first.CutoffAt.Equal(cutoff) || !first.DepartsAt.Equal(cutoff) || !first.ArrivesAt.Equal(cutoff.Add(2*time.Hour)) {
		t.Fatalf("times = %v %v %v", first.CutoffAt, first.DepartsAt, first.ArrivesAt)
	}
	if first.ID != DepartureID("lane-1", departure.KindPickup, cutoff, 1) {
		t.Fatal("pickup ids carry the node generation")
	}
}

func assertDistinctOpenDays(t *testing.T, specs []departure.Spec) {
	t.Helper()
	ids := map[departure.ID]bool{}
	zone := mustZone(t)
	for _, s := range specs {
		local := s.CutoffAt.In(zone)
		if ids[s.ID] || s.CutoffAt.Before(now) {
			t.Fatalf("duplicate or past spec %+v", s)
		}
		ids[s.ID] = true
		if (local.Weekday() == time.Monday && local.Day() == 26) || local.Weekday() == time.Sunday {
			t.Fatalf("pickup on a closed day: %v", local)
		}
	}
}

func mustZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	wantNoErr(t, "zone", err)
	return loc
}

func TestPickupCapacityPerCutoff(t *testing.T) {
	specs, _ := PlanPickups(pickupIn(baseProfile(), 2), now)
	byCutoff := map[int64]int{}
	for _, s := range specs {
		byCutoff[s.CutoffAt.Unix()] = s.CapacityPackages
	}
	z := mustZone(t)
	cases := []struct {
		name string
		at   time.Time
		want int
	}{
		{"friday 11:00: 06:00 to 11:00 is 300 min, 10/h = 50 orders x 2", time.Date(2026, 10, 23, 11, 0, 0, 0, z), 100},
		{"friday 18:00: 420 min = 70 orders capped at maxOrders 50 x 2", time.Date(2026, 10, 23, 18, 0, 0, 0, z), 100},
		{"saturday 11:00: 08:00 to 11:00 is 180 min = 30 x 2", time.Date(2026, 10, 24, 11, 0, 0, 0, z), 60},
	}
	for _, c := range cases {
		if got, ok := byCutoff[c.at.Unix()]; !ok || got != c.want {
			t.Errorf("%s: capacity %d (found %v), want %d", c.name, got, ok, c.want)
		}
	}
}

func TestPickupCapacityFormula(t *testing.T) {
	cases := []struct {
		name      string
		cap       network.ThroughputCap
		cutoffs   []network.Cutoff
		hours     network.Hours
		cutoffAt  string
		maxOrders int
		want      int
	}{
		{"hour cap, first cutoff", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"}, nil, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 0, 50},
		{"max orders lower wins", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"}, nil, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 20, 20},
		{"max orders equal", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"}, nil, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 50, 50},
		{"max orders higher is ignored", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"}, nil, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 51, 50},
		{"day cap pro rata", network.ThroughputCap{Value: 400, TimeUnit: "DAY"}, nil, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 0, 125},
		{"day cap, equal opening and closing use 24h", network.ThroughputCap{Value: 1440, TimeUnit: "DAY"}, nil, network.Hours{Start: "06:00", End: "06:00"}, "09:00", 0, 180},
		{"day cap, non-increasing hours use 24h", network.ThroughputCap{Value: 1440, TimeUnit: "DAY"}, nil, network.Hours{Start: "22:00", End: "06:00"}, "23:00", 0, 60},
		{"window below an hour is an hour", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"}, nil, network.Hours{Start: "06:00", End: "22:00"}, "06:30", 0, 10},
		{"cutoff before opening is an hour", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"}, nil, network.Hours{Start: "08:00", End: "22:00"}, "07:00", 0, 10},
		{"at least one order", network.ThroughputCap{Value: 1, TimeUnit: "DAY"}, nil, network.Hours{Start: "06:00", End: "22:00"}, "07:00", 0, 1},
		{"previous cutoff shortens the window", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"},
			[]network.Cutoff{{LocalTime: "09:00"}}, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 0, 20},
		{"a later cutoff is not previous", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"},
			[]network.Cutoff{{LocalTime: "15:00"}}, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 0, 50},
		{"the nearest previous cutoff wins", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"},
			[]network.Cutoff{{LocalTime: "08:00"}, {LocalTime: "10:00"}, {LocalTime: "09:00"}}, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 0, 10},
		{"a cutoff of another weekday is not previous", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"},
			[]network.Cutoff{{LocalTime: "10:00", Days: []string{"saturday"}}}, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 0, 50},
		{"an equal cutoff is not previous", network.ThroughputCap{Value: 10, TimeUnit: "HOUR"},
			[]network.Cutoff{{LocalTime: "11:00"}}, network.Hours{Start: "06:00", End: "22:00"}, "11:00", 0, 50},
	}
	for _, c := range cases {
		p := network.Profile{Revision: 1, Timezone: "UTC", Hours: map[string]network.Hours{"friday": c.hours}, Cap: c.cap}
		p.Cutoffs = append(append([]network.Cutoff{}, c.cutoffs...), network.Cutoff{LocalTime: c.cutoffAt, MaxOrders: c.maxOrders})
		specs, err := PlanPickups(pickupIn(p, 1), now.Add(-12*time.Hour))
		wantNoErr(t, c.name, err)
		var got int
		for _, s := range specs {
			if s.CutoffAt.Format("15:04") == c.cutoffAt && s.CutoffAt.Weekday() == time.Friday && s.CutoffAt.Day() == 23 {
				got = s.CapacityPackages
			}
		}
		if got != c.want {
			t.Errorf("%s: capacity %d, want %d", c.name, got, c.want)
		}
	}
}

func TestPickupCapacityClamps(t *testing.T) {
	p := network.Profile{Revision: 1, Timezone: "UTC", Hours: map[string]network.Hours{"friday": {Start: "00:00", End: "23:59"}},
		Cutoffs: []network.Cutoff{{LocalTime: "23:00"}}, Cap: network.ThroughputCap{Value: shared.MaxInt32, TimeUnit: "HOUR"}}
	specs, err := PlanPickups(pickupIn(p, 1), now)
	wantNoErr(t, "plan", err)
	if specs[0].CapacityPackages != shared.MaxInt32 {
		t.Fatalf("orders clamp: %d", specs[0].CapacityPackages)
	}
	p.Cap.Value = 2000000
	specs, _ = PlanPickups(pickupIn(p, MaxPackagesPerOrder), now)
	if specs[0].CapacityPackages != shared.MaxInt32 {
		t.Fatalf("packages clamp: %d", specs[0].CapacityPackages)
	}
}

func TestPlanPickupsFollowsTheNodeState(t *testing.T) {
	in := pickupIn(baseProfile(), 1)
	for name, mut := range map[string]func(*PickupInput){
		"suspended":    func(i *PickupInput) { i.Node.Status = network.StatusSuspended },
		"no profile":   func(i *PickupInput) { i.Node.Profile = nil },
		"network node": func(i *PickupInput) { i.Node.Operator = network.OperatorNetwork },
		"not a center": func(i *PickupInput) { i.Node.Type = network.DeliveryStation },
	} {
		c := in
		mut(&c)
		specs, err := PlanPickups(c, now)
		if err != nil || specs != nil {
			t.Errorf("%s: %v %v", name, specs, err)
		}
	}
	other := in
	other.Node.Generation = 2
	a, _ := PlanPickups(in, now)
	b, _ := PlanPickups(other, now)
	if a[0].ID == b[0].ID {
		t.Fatal("a new generation gets new ids")
	}
}

func TestPlanPickupsValidatesItsInput(t *testing.T) {
	for _, ppo := range []int{0, -1, MaxPackagesPerOrder + 1} {
		if _, err := PlanPickups(pickupIn(baseProfile(), ppo), now); !errorsIsValidation(err) {
			t.Errorf("packagesPerOrder %d: %v", ppo, err)
		}
	}
	for _, ppo := range []int{1, MaxPackagesPerOrder} {
		if _, err := PlanPickups(pickupIn(baseProfile(), ppo), now); err != nil {
			t.Errorf("packagesPerOrder %d: %v", ppo, err)
		}
	}
	p := baseProfile()
	p.Timezone = "Mars/Base"
	if _, err := PlanPickups(pickupIn(p, 1), now); !errorsIsValidation(err) {
		t.Errorf("bad zone: %v", err)
	}
}

func errorsIsValidation(err error) bool { return err != nil && isErr(err, shared.ErrValidation) }

func TestPlanPickupsSkipsPastCutoffsAndDuplicates(t *testing.T) {
	p := baseProfile()
	p.Holidays = nil
	p.Cutoffs = []network.Cutoff{{LocalTime: "11:00"}, {LocalTime: "11:00"}}
	at := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC) // exactly the first cutoff (11:00 local)
	specs, _ := PlanPickups(pickupIn(p, 1), at)
	if specs[0].CutoffAt.Equal(at) {
		t.Fatal("a cutoff equal to now is past")
	}
	seen := map[departure.ID]bool{}
	for _, s := range specs {
		if seen[s.ID] {
			t.Fatalf("duplicate cutoff produced a duplicate spec %s", s.ID)
		}
		seen[s.ID] = true
	}
	before, _ := PlanPickups(pickupIn(p, 1), at.Add(-time.Second))
	if len(before) != len(specs)+1 {
		t.Fatalf("one second earlier the same-day cutoff is future: %d vs %d", len(before), len(specs))
	}
}

func TestPlanPickupsCutoffDays(t *testing.T) {
	p := baseProfile()
	p.Holidays = nil
	p.Cutoffs = []network.Cutoff{{LocalTime: "11:00", Days: []string{"saturday"}}}
	specs, _ := PlanPickups(pickupIn(p, 1), now)
	if len(specs) != 2 {
		t.Fatalf("two Saturdays, got %d", len(specs))
	}
	for _, s := range specs {
		if s.CutoffAt.In(mustZone(t)).Weekday() != time.Saturday {
			t.Fatalf("not a Saturday: %v", s.CutoffAt)
		}
	}
}
