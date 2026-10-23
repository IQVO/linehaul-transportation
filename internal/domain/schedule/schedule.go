// Package schedule is the LaneSchedule aggregate, the weekly template that
// generates a lane's departures, and the pure planning functions that turn a
// template (or the declared cutoffs of a vendor node) into departure specs.
//
// States: ACTIVE, RETIRED; transition ACTIVE -> RETIRED (RetireLaneSchedule).
// DefineLaneSchedule creates the schedule, or replaces the slots of an ACTIVE
// one (ADR 0002 A4). Invariant: the slots of one weekday do not overlap
// (ErrOverlappingSlots). The aggregate raises no event (events: []).
package schedule

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// ErrOverlappingSlots: template slots do not overlap on the same lane (422).
var ErrOverlappingSlots = errors.New("overlapping slots")

// State is the lifecycle state of a lane schedule.
type State string

// The two states.
const (
	StateActive  State = "ACTIVE"
	StateRetired State = "RETIRED"
)

// MaxSlots bounds the slots of one schedule (ADR 0002 A3).
const MaxSlots = 100

// Slot is one weekly slot (gap G29). Cutoff and Departs are wall-clock "HH:mm"
// in the zone of the lane's origin node, Arrives in the zone of its destination.
type Slot struct {
	Day              time.Weekday
	Cutoff           string
	Departs          string
	Arrives          string
	CapacityPackages int
}

// minutes returns the three wall-clock times as minutes since midnight.
func (s Slot) minutes() (cutoff, departs, arrives int, err error) {
	c, ok1 := network.ParseClock(s.Cutoff)
	d, ok2 := network.ParseClock(s.Departs)
	a, ok3 := network.ParseClock(s.Arrives)
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, shared.Invalid("slot times %q %q %q must be HH:mm", s.Cutoff, s.Departs, s.Arrives)
	}
	return c, d, a, nil
}

func (s Slot) validate() error {
	if s.Day < time.Sunday || s.Day > time.Saturday {
		return shared.Invalid("slot day %d is not a weekday", int(s.Day))
	}
	if s.CapacityPackages < 1 || s.CapacityPackages > shared.MaxInt32 {
		return shared.Invalid("slot capacityPackages must be between 1 and %d", shared.MaxInt32)
	}
	c, d, _, err := s.minutes()
	if err != nil {
		return err
	}
	if c > d {
		return fmt.Errorf("%w: slot cutoff %s is after departure %s", departure.ErrInvalidDepartureTimes, s.Cutoff, s.Departs)
	}
	return nil
}

// validateSlots checks the count, every slot and the overlap rule.
func validateSlots(slots []Slot) error {
	if len(slots) < 1 || len(slots) > MaxSlots {
		return shared.Invalid("a schedule has 1 to %d slots, got %d", MaxSlots, len(slots))
	}
	for _, s := range slots {
		if err := s.validate(); err != nil {
			return err
		}
	}
	return checkOverlap(slots)
}

type window struct{ from, to int }

func checkOverlap(slots []Slot) error {
	byDay := map[time.Weekday][]window{}
	for _, s := range slots {
		c, d, _, _ := s.minutes()
		byDay[s.Day] = append(byDay[s.Day], window{c, d})
	}
	for day, ws := range byDay {
		slices.SortFunc(ws, func(a, b window) int { return cmp.Compare(a.from, b.from) })
		for i := 1; i < len(ws); i++ {
			if ws[i].from <= ws[i-1].to {
				return fmt.Errorf("%w: two slots on %s intersect", ErrOverlappingSlots, day)
			}
		}
	}
	return nil
}

// LaneSchedule is the aggregate root.
type LaneSchedule struct {
	laneID  string
	state   State
	slots   []Slot
	version int64
}

// Snapshot is the persisted form of a lane schedule. Restore validates it.
type Snapshot struct {
	LaneID  string
	State   State
	Slots   []Slot
	Version int64
}

// Define creates an ACTIVE schedule at version 1 (DefineLaneSchedule).
func Define(laneID string, slots []Slot) (*LaneSchedule, error) {
	if !shared.ValidID(laneID) {
		return nil, shared.Invalid("laneId %q is not a valid id", laneID)
	}
	if err := validateSlots(slots); err != nil {
		return nil, err
	}
	return &LaneSchedule{laneID: laneID, state: StateActive, slots: cloneSlots(slots), version: 1}, nil
}

// Redefine replaces the slots of an ACTIVE schedule (ADR 0002 A4).
func (s *LaneSchedule) Redefine(slots []Slot) error {
	if s.state != StateActive {
		return fmt.Errorf("%w: DefineLaneSchedule on lane %s in state %s", shared.ErrIllegalTransition, s.laneID, s.state)
	}
	if err := validateSlots(slots); err != nil {
		return err
	}
	s.slots = cloneSlots(slots)
	s.version++
	return nil
}

// Retire moves ACTIVE to RETIRED (RetireLaneSchedule).
func (s *LaneSchedule) Retire() error {
	if s.state != StateActive {
		return fmt.Errorf("%w: RetireLaneSchedule on lane %s in state %s", shared.ErrIllegalTransition, s.laneID, s.state)
	}
	s.state = StateRetired
	s.version++
	return nil
}

func cloneSlots(in []Slot) []Slot { return append([]Slot(nil), in...) }

// LaneID returns the laneId.
func (s *LaneSchedule) LaneID() string { return s.laneID }

// State returns the lifecycle state.
func (s *LaneSchedule) State() State { return s.state }

// Slots returns a copy of the slots.
func (s *LaneSchedule) Slots() []Slot { return cloneSlots(s.slots) }

// Version returns the optimistic-lock version.
func (s *LaneSchedule) Version() int64 { return s.version }

// Snapshot returns the persisted form.
func (s *LaneSchedule) Snapshot() Snapshot {
	return Snapshot{LaneID: s.laneID, State: s.state, Slots: cloneSlots(s.slots), Version: s.version}
}

// Restore rebuilds a schedule from its persisted form and validates it.
func Restore(sn Snapshot) (*LaneSchedule, error) {
	if !shared.ValidID(sn.LaneID) {
		return nil, shared.Corrupt("lane schedule %q: id", sn.LaneID)
	}
	if sn.State != StateActive && sn.State != StateRetired {
		return nil, shared.Corrupt("lane schedule %s: state %q", sn.LaneID, sn.State)
	}
	if sn.Version < 1 {
		return nil, shared.Corrupt("lane schedule %s: version %d", sn.LaneID, sn.Version)
	}
	if err := validateSlots(sn.Slots); err != nil {
		return nil, shared.Corrupt("lane schedule %s: %v", sn.LaneID, err)
	}
	return &LaneSchedule{laneID: sn.LaneID, state: sn.State, slots: cloneSlots(sn.Slots), version: sn.Version}, nil
}
