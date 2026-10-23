package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// SlotInput is one slot of DefineLaneScheduleCommand, with the REST names.
type SlotInput struct {
	DayOfWeek        string
	CutoffLocalTime  string
	DepartsLocalTime string
	ArrivesLocalTime string
	CapacityPackages int
}

// DefineLaneScheduleCommand is the input of DefineLaneSchedule.
type DefineLaneScheduleCommand struct {
	LaneID string
	Slots  []SlotInput
}

func toSlots(in []SlotInput) ([]schedule.Slot, error) {
	out := make([]schedule.Slot, 0, len(in))
	for _, s := range in {
		day, ok := network.ParseWeekday(s.DayOfWeek)
		if !ok {
			return nil, shared.Invalid("dayOfWeek %q is not a weekday", s.DayOfWeek)
		}
		out = append(out, schedule.Slot{
			Day: day, Cutoff: s.CutoffLocalTime, Departs: s.DepartsLocalTime, Arrives: s.ArrivesLocalTime, CapacityPackages: s.CapacityPackages,
		})
	}
	return out, nil
}

// DefineLaneSchedule creates the schedule of a known lane or replaces the slots
// of an ACTIVE one (ADR 0002 A4). It reports whether the schedule was created.
func (s *Service) DefineLaneSchedule(ctx context.Context, cmd DefineLaneScheduleCommand) (views.LaneSchedule, bool, error) {
	slots, err := toSlots(cmd.Slots)
	if err != nil {
		return views.LaneSchedule{}, false, err
	}
	var out views.LaneSchedule
	created := false
	err = s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		if _, lerr := s.Lanes.Get(ctx, cmd.LaneID); lerr != nil {
			return nil, fmt.Errorf("lane %s: %w", cmd.LaneID, lerr)
		}
		sc, gerr := s.Schedules.Get(ctx, cmd.LaneID)
		switch {
		case isNotFound(gerr):
			sc, gerr = schedule.Define(cmd.LaneID, slots)
			created = true
		case gerr == nil:
			gerr = sc.Redefine(slots)
		}
		if gerr != nil {
			return nil, gerr
		}
		out = views.FromSchedule(sc)
		return nil, s.Schedules.Save(ctx, sc)
	})
	return out, created && err == nil, err
}

// RetireLaneSchedule moves the schedule of a lane to RETIRED; a RETIRED one is
// returned unchanged.
func (s *Service) RetireLaneSchedule(ctx context.Context, laneID string) (views.LaneSchedule, error) {
	var out views.LaneSchedule
	err := s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		sc, err := s.Schedules.Get(ctx, laneID)
		if err != nil {
			return nil, err
		}
		if sc.State() != schedule.StateRetired {
			if err := sc.Retire(); err != nil {
				return nil, err
			}
			if err := s.Schedules.Save(ctx, sc); err != nil {
				return nil, err
			}
		}
		out = views.FromSchedule(sc)
		return nil, nil
	})
	return out, err
}
