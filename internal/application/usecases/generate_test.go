package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

// network world: sort centers n-sc (destination) and n-sc2, a ground lane
// lane-1 n-sc -> n-sc2 with a Friday schedule.
func (e *env) scheduleWorld() {
	e.t.Helper()
	e.node("n-a", network.SortationCenter, network.OperatorNetwork, network.StatusActive)
	e.node("n-b", network.SortationCenter, network.OperatorNetwork, network.StatusActive)
	e.lane("lane-1", "n-a", "n-b", network.ModeGround, true)
	_, _, err := e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("FRIDAY", 40)}})
	e.must(err)
}

func (e *env) generate() usecases.GenerateReport {
	e.t.Helper()
	rep, err := e.svc.GenerateDepartures(e.ctx)
	e.must(err)
	return rep
}

func (e *env) listAll(f repository.DepartureFilter) []*departure.Departure {
	e.t.Helper()
	f.Limit = 1000
	out, err := e.store.Departures().List(e.ctx, f)
	e.must(err)
	return out
}

func TestGenerateDeparturesFromALaneSchedule(t *testing.T) {
	e := newEnv(t)
	e.scheduleWorld()
	rep := e.generate()
	if rep.Scheduled != 2 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want 2 Fridays", rep)
	}
	sameNames(t, e.eventNames(), []string{"DepartureScheduled", "DepartureScheduled"})
	first := eventsOf[departure.DepartureScheduled](e)[0]
	if first.Kind != departure.KindLinehaul || first.LaneID != "lane-1" || first.CapacityPackages != 40 ||
		!first.CutoffAt.Equal(time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("event = %+v", first)
	}
	before := e.store.Counts()
	if rep := e.generate(); rep.Scheduled != 0 || rep.Failed != 0 {
		t.Fatalf("a rerun schedules nothing: %+v", rep)
	}
	e.unchanged(before)
}

func TestGenerateDeparturesSkipsWhatItCannotPlan(t *testing.T) {
	cases := map[string]func(e *env){
		"lane closed": func(e *env) { e.lane("lane-1", "n-a", "n-b", network.ModeGround, false) },
		"origin node unknown": func(e *env) {
			e.must(e.store.Nodes().Save(e.ctx, network.NodeView{ID: "n-a", Status: network.StatusActive}))
		},
		"retired schedule": func(e *env) { _, err := e.svc.RetireLaneSchedule(e.ctx, "lane-1"); e.must(err) },
	}
	for name, mutate := range cases {
		e := newEnv(t)
		e.scheduleWorld()
		mutate(e)
		rep := e.generate()
		if rep.Scheduled != 0 || rep.Failed != 0 || len(e.outbox.Events()) != 0 {
			t.Errorf("%s: report = %+v events = %v", name, rep, e.eventNames())
		}
	}
	// A schedule whose lane view is gone, and a node view that does not exist at all, are skipped too.
	e := newEnv(t)
	e.node("n-a", network.SortationCenter, network.OperatorNetwork, network.StatusActive)
	_, _, _ = e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("FRIDAY", 1)}})
	if rep := e.generate(); rep.Scheduled != 0 || rep.Failed != 0 {
		t.Fatalf("no lane view: %+v", rep)
	}
	e.lane("lane-1", "n-a", "n-b", network.ModeGround, true)
	if rep := e.generate(); rep.Scheduled != 0 || rep.Failed != 0 {
		t.Fatalf("no destination node view: %+v", rep)
	}
}

func (e *env) pickupWorld() {
	e.t.Helper()
	e.node("n-sc", network.SortationCenter, network.OperatorNetwork, network.StatusActive)
	e.vendorFC("n-fc")
	e.lane("lane-fc", "n-fc", "n-sc", network.ModeGround, true)
}

func TestGenerateDeparturesPlansPickups(t *testing.T) {
	e := newEnv(t)
	e.pickupWorld()
	rep := e.generate()
	if rep.Scheduled != 10 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want 10 weekday cutoffs in 14 days", rep)
	}
	first := eventsOf[departure.DepartureScheduled](e)[0]
	if first.Kind != departure.KindPickup || first.OriginNodeID != "n-fc" || first.DestinationNodeID != "n-sc" || first.LaneID != "lane-fc" ||
		!first.CutoffAt.Equal(time.Date(2026, 10, 23, 21, 0, 0, 0, time.UTC)) || !first.ArrivesAt.Equal(first.DepartsAt.Add(2*time.Hour)) {
		t.Fatalf("event = %+v", first)
	}
	// 18:00 cutoff, previous cutoff = opening at 06:00: 12 h x 10 orders/h, packagesPerOrder 1.
	if first.CapacityPackages != 120 {
		t.Fatalf("capacity = %d", first.CapacityPackages)
	}
	before := e.store.Counts()
	e.generate()
	e.unchanged(before)
}

func TestPackagesPerOrderScalesPickupCapacity(t *testing.T) {
	e := newEnv(t, func(d *usecases.Deps) { d.PackagesPerOrder = 3 })
	e.pickupWorld()
	e.generate()
	if got := eventsOf[departure.DepartureScheduled](e)[0].CapacityPackages; got != 360 {
		t.Fatalf("capacity = %d, want 360", got)
	}
}

func TestPickupsNeedAnOutboundLaneAndAKnownDestination(t *testing.T) {
	e := newEnv(t)
	e.vendorFC("n-fc")
	if rep := e.generate(); rep.Scheduled != 0 {
		t.Fatalf("no lane: %+v", rep)
	}
	e.lane("lane-fc", "n-fc", "n-sc", network.ModeGround, true)
	if rep := e.generate(); rep.Scheduled != 0 {
		t.Fatalf("destination node unknown: %+v", rep)
	}
	e.node("n-sc", network.SortationCenter, network.OperatorNetwork, network.StatusActive)
	if rep := e.generate(); rep.Scheduled != 10 {
		t.Fatalf("complete: %+v", rep)
	}
}

func TestAPlannerCancelledPickupIsNotResurrected(t *testing.T) {
	e := newEnv(t)
	e.pickupWorld()
	e.generate()
	list := e.listAll(repository.DepartureFilter{State: departure.StateScheduled})
	_, err := e.svc.CancelDeparture(e.ctx, string(list[0].ID()), "planner")
	e.must(err)
	if rep := e.generate(); rep.Scheduled != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if got := len(e.listAll(repository.DepartureFilter{State: departure.StateCancelled})); got != 1 {
		t.Fatalf("cancelled = %d", got)
	}
}

func TestGenerateDeparturesCountsAFailingUnitAndContinues(t *testing.T) {
	failing := &saveFails{}
	e := newEnv(t, func(d *usecases.Deps) { failing.DepartureRepository = d.Departures; d.Departures = failing })
	e.pickupWorld()
	failing.on = true
	rep, err := e.svc.GenerateDepartures(e.ctx)
	e.must(err)
	if rep.Failed != 1 || rep.Scheduled != 0 {
		t.Fatalf("report = %+v", rep)
	}
	failing.on = false
	if rep := e.generate(); rep.Scheduled != 10 || rep.Failed != 0 {
		t.Fatalf("after recovery: %+v", rep)
	}
}

type saveFails struct {
	ports.DepartureRepository
	on bool
}

func (s *saveFails) Save(ctx context.Context, d *departure.Departure) error {
	if s.on {
		return errors.New("disk on fire")
	}
	return s.DepartureRepository.Save(ctx, d)
}

func TestGenerateDeparturesStopsWhenTheContextEnds(t *testing.T) {
	e := newEnv(t)
	e.pickupWorld()
	ctx, cancel := context.WithCancel(e.ctx)
	cancel()
	if _, err := e.svc.GenerateDepartures(ctx); err == nil {
		t.Fatal("a cancelled context is reported")
	}
}
