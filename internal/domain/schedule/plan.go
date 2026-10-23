package schedule

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// HorizonDays is the number of operating days the roll keeps ahead (ADR 0002 A6).
const HorizonDays = 14

// ErrNodeUnknown: a node's zone or type is not known yet (its NodeRegistered
// has not arrived), so nothing can be planned for it.
var ErrNodeUnknown = errors.New("node not known yet")

// DepartureID returns the deterministic departureId of a departure (ADR 0002 A1).
func DepartureID(laneID string, kind departure.Kind, cutoffAt time.Time, generation int) departure.ID {
	return departure.ID(shared.DeterministicID("dep-", laneID, string(kind), cutoffAt.UTC().Format(time.RFC3339), fmt.Sprint(generation)))
}

// at builds the instant of a local wall-clock time on the date of day.
func at(day time.Time, minutes int, loc *time.Location) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d, minutes/60, minutes%60, 0, 0, loc)
}

// firstAfter returns the first instant after t whose wall-clock time in loc is minutes.
func firstAfter(t time.Time, minutes int, loc *time.Location) time.Time {
	local := t.In(loc)
	c := at(local, minutes, loc)
	if !c.After(t) {
		c = at(local.AddDate(0, 0, 1), minutes, loc)
	}
	return c
}

// nodeZone returns the time zone of a node view, or ErrNodeUnknown.
func nodeZone(n network.NodeView) (*time.Location, error) {
	if n.Timezone == "" || !n.Type.Valid() {
		return nil, fmt.Errorf("%w: %s", ErrNodeUnknown, n.ID)
	}
	loc, err := time.LoadLocation(n.Timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: node %s zone %q", shared.ErrValidation, n.ID, n.Timezone)
	}
	return loc, nil
}

// kindOf maps a lane mode to the departure kind of its schedule (ADR 0002 A5).
func kindOf(m network.Mode) departure.Kind {
	if m == network.ModeAir {
		return departure.KindAir
	}
	return departure.KindLinehaul
}

// PlanLane expands the slots of an ACTIVE schedule over the horizon into
// departure specs for one lane (GenerateDepartures). A RETIRED schedule, and a
// lane that leaves a vendor fulfillment center, yield nothing. Slots whose
// cutoff is not after now are skipped.
func PlanLane(s *LaneSchedule, lane network.LaneView, origin, dest network.NodeView, now time.Time) ([]departure.Spec, error) {
	if s.state != StateActive || (origin.Type == network.FulfillmentCenter && origin.Operator == network.OperatorVendor) {
		return nil, nil
	}
	oloc, err := nodeZone(origin)
	if err != nil {
		return nil, err
	}
	dloc, err := nodeZone(dest)
	if err != nil {
		return nil, err
	}
	var out []departure.Spec
	base := now.In(oloc)
	for i := 0; i < HorizonDays; i++ {
		day := base.AddDate(0, 0, i)
		for _, sl := range s.slots {
			if sl.Day != day.Weekday() {
				continue
			}
			if spec, ok := slotSpec(sl, day, lane, origin, dest, oloc, dloc, now); ok {
				out = append(out, spec)
			}
		}
	}
	return out, nil
}

func slotSpec(sl Slot, day time.Time, lane network.LaneView, origin, dest network.NodeView, oloc, dloc *time.Location, now time.Time) (departure.Spec, bool) {
	c, d, a, _ := sl.minutes()
	cutoffAt, departsAt := at(day, c, oloc), at(day, d, oloc)
	if !cutoffAt.After(now) {
		return departure.Spec{}, false
	}
	kind := kindOf(lane.Mode)
	return departure.Spec{
		ID: DepartureID(lane.ID, kind, cutoffAt, 0), LaneID: lane.ID, Kind: kind,
		OriginNodeID: origin.ID, DestinationNodeID: dest.ID, OriginType: origin.Type, DestinationType: dest.Type,
		CutoffAt: cutoffAt, DepartsAt: departsAt, ArrivesAt: firstAfter(departsAt, a, dloc), CapacityPackages: sl.CapacityPackages,
	}, true
}
