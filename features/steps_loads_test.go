package features_test

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/linehaul-transportation/internal/domain/load"
)

func registerLoadSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I open the load "([^"]*)" on the departure "([^"]*)"$`, w.openLoad)
	sc.Step(`^the package "([^"]*)" is added to the load "([^"]*)"$`, w.addPackage)
	sc.Step(`^the load "([^"]*)" is closed$`, w.closeLoad)
	sc.Step(`^I dispatch the load "([^"]*)"$`, w.dispatchLoad)
	sc.Step(`^I receive the load "([^"]*)"$`, w.receiveLoad)
	sc.Step(`^the clock moves to "([^"]*)"$`, w.moveClock)
	sc.Step(`^the load "([^"]*)" is (OPEN|CLOSED|DEPARTED|ARRIVED)$`, w.loadIs)
	sc.Step(`^the last LoadDeparted of "([^"]*)" carries kind "([^"]*)" and node type "([^"]*)"$`, w.departedCarries)
	sc.Step(`^the last LoadArrived of "([^"]*)" carries kind "([^"]*)" and node type "([^"]*)"$`, w.arrivedCarries)
}

func (w *world) openLoad(alias, departureAlias string) {
	w.run(func() error {
		l, err := w.svc.OpenLoad(w.ctx(), w.depID(departureAlias))
		if err == nil {
			w.loads[alias] = l.LoadID
		}
		return err
	})
}

func (w *world) addPackage(tracking, alias string) {
	w.run(func() error {
		_, err := w.svc.AddPackage(w.ctx(), w.loadID(alias), tracking)
		return err
	})
}

func (w *world) closeLoad(alias string) {
	w.run(func() error {
		_, err := w.svc.CloseLoad(w.ctx(), w.loadID(alias))
		return err
	})
}

func (w *world) dispatchLoad(alias string) {
	w.run(func() error {
		_, err := w.svc.DispatchLoad(w.ctx(), w.loadID(alias))
		return err
	})
}

func (w *world) receiveLoad(alias string) {
	w.run(func() error {
		_, err := w.svc.ReceiveLoad(w.ctx(), w.loadID(alias))
		return err
	})
}

func (w *world) moveClock(instant string) error {
	t, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		return err
	}
	w.clock.Set(t)
	return nil
}

func (w *world) loadIs(alias, state string) error {
	l, err := w.store.Loads().Get(w.ctx(), load.ID(w.loadID(alias)))
	if err != nil {
		return err
	}
	if string(l.State()) != state {
		return fmt.Errorf("load %s is %s, want %s", alias, l.State(), state)
	}
	return nil
}

func (w *world) departedCarries(alias, kind, nodeType string) error {
	id := w.loadID(alias)
	for i := len(w.store.Outbox().Events()) - 1; i >= 0; i-- {
		if e, ok := w.store.Outbox().Events()[i].(load.LoadDeparted); ok && string(e.LoadID) == id {
			if string(e.DepartureKind) != kind || string(e.NodeType) != nodeType {
				return fmt.Errorf("LoadDeparted carries %s / %s, want %s / %s", e.DepartureKind, e.NodeType, kind, nodeType)
			}
			return nil
		}
	}
	return fmt.Errorf("no LoadDeparted for load %s", alias)
}

func (w *world) arrivedCarries(alias, kind, nodeType string) error {
	id := w.loadID(alias)
	for i := len(w.store.Outbox().Events()) - 1; i >= 0; i-- {
		if e, ok := w.store.Outbox().Events()[i].(load.LoadArrived); ok && string(e.LoadID) == id {
			if string(e.DepartureKind) != kind || string(e.NodeType) != nodeType {
				return fmt.Errorf("LoadArrived carries %s / %s, want %s / %s", e.DepartureKind, e.NodeType, kind, nodeType)
			}
			return nil
		}
	}
	return fmt.Errorf("no LoadArrived for load %s", alias)
}
