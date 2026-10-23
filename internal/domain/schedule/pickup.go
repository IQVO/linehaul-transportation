package schedule

import (
	"slices"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// MaxPackagesPerOrder bounds the packagesPerOrder setting (ADR 0002 A9).
const MaxPackagesPerOrder = 1000

// minWindowMinutes is the shortest window a pickup capacity is computed over.
const minWindowMinutes = 60

// PickupInput is what PlanPickups needs: the vendor node, its default outbound
// lane, the type of that lane's destination and the packagesPerOrder setting.
type PickupInput struct {
	Node             network.NodeView
	Lane             network.LaneView
	DestinationType  network.NodeType
	PackagesPerOrder int
}

// PlanPickups returns the PICKUP departure specs of a node over the horizon
// (ADR 0002 A7 to A9): one per applicable cutoff of every operating day that is
// not a holiday, whose cutoff is after now. The truck leaves at the cutoff and
// arrives after the lane's nominal transit. A node that is not eligible yields
// nothing.
func PlanPickups(in PickupInput, now time.Time) ([]departure.Spec, error) {
	if !in.Node.PickupEligible() {
		return nil, nil
	}
	if in.PackagesPerOrder < 1 || in.PackagesPerOrder > MaxPackagesPerOrder {
		return nil, shared.Invalid("packagesPerOrder %d must be between 1 and %d", in.PackagesPerOrder, MaxPackagesPerOrder)
	}
	p := *in.Node.Profile
	loc, err := p.Location()
	if err != nil {
		return nil, err
	}
	var out []departure.Spec
	seen := map[departure.ID]bool{}
	base := now.In(loc)
	for i := 0; i < HorizonDays; i++ {
		day := base.AddDate(0, 0, i)
		start, end, open := p.OperatingHours(day.Weekday())
		if !open || p.IsHoliday(day) {
			continue
		}
		for _, c := range p.Cutoffs {
			spec, ok := pickupSpec(in, c, day, start, end, loc, now)
			if ok && !seen[spec.ID] {
				seen[spec.ID] = true
				out = append(out, spec)
			}
		}
	}
	return out, nil
}

func pickupSpec(in PickupInput, c network.Cutoff, day time.Time, start, end int, loc *time.Location, now time.Time) (departure.Spec, bool) {
	if !c.AppliesOn(day.Weekday()) {
		return departure.Spec{}, false
	}
	minutes, _ := network.ParseClock(c.LocalTime)
	cutoffAt := at(day, minutes, loc)
	if !cutoffAt.After(now) {
		return departure.Spec{}, false
	}
	p := *in.Node.Profile
	packages := pickupCapacity(p, c, day.Weekday(), minutes, start, end) * in.PackagesPerOrder
	packages = min(packages, shared.MaxInt32)
	return departure.Spec{
		ID: DepartureID(in.Lane.ID, departure.KindPickup, cutoffAt, in.Node.Generation), LaneID: in.Lane.ID,
		Kind: departure.KindPickup, OriginNodeID: in.Node.ID, DestinationNodeID: in.Lane.Destination,
		OriginType: in.Node.Type, DestinationType: in.DestinationType,
		CutoffAt: cutoffAt, DepartsAt: cutoffAt, ArrivesAt: cutoffAt.Add(time.Duration(in.Lane.TransitMinutes) * time.Minute),
		CapacityPackages: packages,
	}, true
}

// pickupCapacity is the capacity in orders of one cutoff (ADR 0002 A9).
func pickupCapacity(p network.Profile, c network.Cutoff, day time.Weekday, minutes, start, end int) int {
	window := max(minutes-previousCutoff(p, day, minutes, start), minWindowMinutes)
	orders := int64(p.Cap.Value) * int64(window) / 60
	if p.Cap.TimeUnit == network.CapDay {
		operating := end - start
		if operating <= 0 {
			operating = 24 * 60
		}
		orders = int64(p.Cap.Value) * int64(window) / int64(operating)
	}
	if c.MaxOrders > 0 {
		orders = min(orders, int64(c.MaxOrders))
	}
	return int(min(max(orders, 1), shared.MaxInt32))
}

// previousCutoff returns the latest applicable cutoff of the day strictly before
// minutes, or the opening time when there is none.
func previousCutoff(p network.Profile, day time.Weekday, minutes, opening int) int {
	var earlier []int
	for _, o := range p.Cutoffs {
		m, ok := network.ParseClock(o.LocalTime)
		if ok && o.AppliesOn(day) && m < minutes {
			earlier = append(earlier, m)
		}
	}
	if len(earlier) == 0 {
		return opening
	}
	return slices.Max(earlier)
}
