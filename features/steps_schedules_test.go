package features_test

import (
	"fmt"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

func registerScheduleSteps(sc *godog.ScenarioContext, w *world) {
	const slot = `a slot on "([^"]*)" cutoff "([^"]*)" departs "([^"]*)" arrives "([^"]*)" for (\d+) packages`
	sc.Step(`^I define the schedule of "([^"]*)" with `+slot+`$`, w.defineSchedule)
	sc.Step(`^the schedule of "([^"]*)" has `+slot+`$`, w.defineSchedule)
	sc.Step(`^I define the schedule of "([^"]*)" with two slots on "([^"]*)" both cutoff "([^"]*)" departs "([^"]*)" arrives "([^"]*)" for (\d+) packages$`, w.defineTwin)
	sc.Step(`^the schedule of "([^"]*)" is retired$`, w.retireSchedule)
	sc.Step(`^departures are generated$`, w.generate)
	sc.Step(`^the lane "([^"]*)" has LINEHAUL departures with capacity (\d+)$`, w.laneHasDepartures)
	sc.Step(`^the lane "([^"]*)" has a LINEHAUL departure with capacity (\d+)$`, w.laneHasADepartureWithCapacity)
	sc.Step(`^no departure was scheduled by the last command$`, w.nothingScheduled)
}

func slotInput(day, cutoff, departs, arrives, capacity string) usecases.SlotInput {
	n, _ := strconv.Atoi(capacity)
	return usecases.SlotInput{DayOfWeek: day, CutoffLocalTime: cutoff, DepartsLocalTime: departs, ArrivesLocalTime: arrives, CapacityPackages: n}
}

func (w *world) defineSchedule(lane, day, cutoff, departs, arrives, capacity string) {
	w.run(func() error {
		_, _, err := w.svc.DefineLaneSchedule(w.ctx(), usecases.DefineLaneScheduleCommand{
			LaneID: lane, Slots: []usecases.SlotInput{slotInput(day, cutoff, departs, arrives, capacity)},
		})
		return err
	})
}

func (w *world) defineTwin(lane, day, cutoff, departs, arrives, capacity string) {
	w.run(func() error {
		s := slotInput(day, cutoff, departs, arrives, capacity)
		_, _, err := w.svc.DefineLaneSchedule(w.ctx(), usecases.DefineLaneScheduleCommand{LaneID: lane, Slots: []usecases.SlotInput{s, s}})
		return err
	})
}

func (w *world) retireSchedule(lane string) {
	w.run(func() error {
		_, err := w.svc.RetireLaneSchedule(w.ctx(), lane)
		return err
	})
}

func (w *world) generate() {
	w.run(func() error {
		report, err := w.svc.GenerateDepartures(w.ctx())
		w.scheduled = report.Scheduled
		if err == nil && report.Failed > 0 {
			err = fmt.Errorf("%d schedules or nodes failed to generate", report.Failed)
		}
		return err
	})
}

func (w *world) laneHasDepartures(laneAlias, capacity string) error {
	want, _ := strconv.Atoi(capacity)
	lane, ok := w.lanes[laneAlias]
	if !ok {
		return fmt.Errorf("the scenario never opened lane %q", laneAlias)
	}
	ds, err := w.departuresOf(lane.Origin, isKind(departure.KindLinehaul))
	if err != nil {
		return err
	}
	if len(ds) == 0 {
		return fmt.Errorf("lane %s has no LINEHAUL departure", laneAlias)
	}
	for _, d := range ds {
		if d.CapacityPackages != want || d.DestinationNodeID != lane.Destination || d.LaneID != laneAlias {
			return fmt.Errorf("departure %s = lane %s to %s capacity %d, want lane %s to %s capacity %d",
				d.DepartureID, d.LaneID, d.DestinationNodeID, d.CapacityPackages, laneAlias, lane.Destination, want)
		}
	}
	return nil
}

// laneHasADepartureWithCapacity asserts at least one departure of the lane
// has the capacity (find-by-field, never an index: the departure list is not
// ordered by insertion).
func (w *world) laneHasADepartureWithCapacity(laneAlias, capacity string) error {
	want, _ := strconv.Atoi(capacity)
	lane, ok := w.lanes[laneAlias]
	if !ok {
		return fmt.Errorf("the scenario never opened lane %q", laneAlias)
	}
	ds, err := w.departuresOf(lane.Origin, isKind(departure.KindLinehaul))
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.LaneID == laneAlias && d.CapacityPackages == want {
			return nil
		}
	}
	return fmt.Errorf("lane %s has no LINEHAUL departure with capacity %d", laneAlias, want)
}

func (w *world) nothingScheduled() error {
	if w.scheduled != 0 {
		return fmt.Errorf("the last command scheduled %d departures", w.scheduled)
	}
	for _, n := range w.publishedNames() {
		if n == "DepartureScheduled" {
			return fmt.Errorf("the last command published DepartureScheduled")
		}
	}
	return nil
}
