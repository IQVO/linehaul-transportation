package features_test

import (
	"fmt"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

func registerDepartureSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the pickup departure "([^"]*)" is the (1st|2nd|3rd) from "([^"]*)"$`, w.aliasPickup)
	sc.Step(`^the line-haul departure "([^"]*)" is the (1st|2nd|3rd) on lane "([^"]*)"$`, w.aliasLinehaul)
	sc.Step(`^the capacity of "([^"]*)" is changed to (\d+)$`, w.changeCapacity)
	sc.Step(`^the capacity of every pickup departure of "([^"]*)" is changed to (\d+)$`, w.changeAllCapacities)
	sc.Step(`^the assignment "([^"]*)" is locked on "([^"]*)"$`, w.lock)
	sc.Step(`^the lock of "([^"]*)" on "([^"]*)" is delivered again as a new event$`, w.lock)
	sc.Step(`^the departure "([^"]*)" is cancelled because "([^"]*)"$`, w.cancelDeparture)
	sc.Step(`^the departure "([^"]*)" holds (\d+) booked packages?$`, w.holdsBooked)
	sc.Step(`^the departure "([^"]*)" is (SCHEDULED|CLOSED|DEPARTED|ARRIVED|CANCELLED)$`, w.departureIs)
	sc.Step(`^the assignment "([^"]*)" is booked on "([^"]*)"$`, w.bookedOn)
	sc.Step(`^the assignment "([^"]*)" is in state "([^"]*)"$`, w.assignmentState)
}

var ordinals = map[string]int{"1st": 0, "2nd": 1, "3rd": 2}

func (w *world) alias(alias, origin, ord string, kind departure.Kind) error {
	ds, err := w.departuresOf(origin, func(d views.Departure) bool {
		return d.Kind == string(kind) && d.State == string(departure.StateScheduled)
	})
	if err != nil {
		return err
	}
	i := ordinals[ord]
	if i >= len(ds) {
		return fmt.Errorf("origin %s has %d scheduled %s departures, want a %s", origin, len(ds), kind, ord)
	}
	w.departures[alias] = ds[i].DepartureID
	return nil
}

func (w *world) aliasPickup(alias, ord, origin string) error {
	return w.alias(alias, origin, ord, departure.KindPickup)
}

func (w *world) aliasLinehaul(alias, ord, laneAlias string) error {
	lane, ok := w.lanes[laneAlias]
	if !ok {
		return fmt.Errorf("the scenario never opened lane %q", laneAlias)
	}
	return w.alias(alias, lane.Origin, ord, departure.KindLinehaul)
}

func (w *world) changeCapacity(alias, capacity string) {
	n, _ := strconv.Atoi(capacity)
	w.run(func() error {
		_, err := w.svc.ChangeDepartureCapacity(w.ctx(), w.depID(alias), n)
		return err
	})
}

func (w *world) changeAllCapacities(origin, capacity string) {
	n, _ := strconv.Atoi(capacity)
	w.run(func() error {
		ds, err := w.departuresOf(origin, isKind(departure.KindPickup))
		for _, d := range ds {
			if err != nil {
				break
			}
			_, err = w.svc.ChangeDepartureCapacity(w.ctx(), d.DepartureID, n)
		}
		return err
	})
}

func (w *world) lock(assignment, alias string) {
	id := w.depID(alias)
	w.run(func() error {
		d, err := w.svc.GetDeparture(w.ctx(), id)
		if err != nil {
			return err
		}
		return w.svc.HandleLocked(w.ctx(), w.eventID(), usecases.LockedInput{
			AssignmentID: assignment,
			Facts:        volume.Facts{OrderID: "or-" + assignment, GroupNo: 1, NodeID: d.OriginNodeID, DepartureID: departure.ID(id)},
		})
	})
}

func (w *world) cancelDeparture(alias, reason string) {
	w.run(func() error {
		_, err := w.svc.CancelDeparture(w.ctx(), w.depID(alias), reason)
		return err
	})
}

func (w *world) holdsBooked(alias, n string) error {
	want, _ := strconv.Atoi(n)
	d, err := w.svc.GetDeparture(w.ctx(), w.depID(alias))
	if err != nil {
		return err
	}
	if d.BookedPackages != want || d.RemainingPackages != d.CapacityPackages-want {
		return fmt.Errorf("departure %s: booked %d remaining %d of %d, want %d booked", alias, d.BookedPackages, d.RemainingPackages, d.CapacityPackages, want)
	}
	return nil
}

func (w *world) departureIs(alias, state string) error {
	d, err := w.svc.GetDeparture(w.ctx(), w.depID(alias))
	if err != nil {
		return err
	}
	if d.State != state {
		return fmt.Errorf("departure %s is %s, want %s", alias, d.State, state)
	}
	return nil
}

func (w *world) volumeOf(assignment string) (volume.Snapshot, error) {
	v, err := w.store.Volumes().Get(w.ctx(), assignment)
	if err != nil {
		return volume.Snapshot{}, fmt.Errorf("assignment %s: %w", assignment, err)
	}
	return v.Snapshot(), nil
}

func (w *world) bookedOn(assignment, alias string) error {
	s, err := w.volumeOf(assignment)
	if err != nil {
		return err
	}
	if s.State != volume.StateBooked || string(s.BookedOn) != w.depID(alias) {
		return fmt.Errorf("assignment %s is %s on %q, want BOOKED on %s (%s)", assignment, s.State, s.BookedOn, alias, w.depID(alias))
	}
	return nil
}

func (w *world) assignmentState(assignment, state string) error {
	s, err := w.volumeOf(assignment)
	if err != nil {
		return err
	}
	if string(s.State) != state {
		return fmt.Errorf("assignment %s is %s, want %s", assignment, s.State, state)
	}
	return nil
}
