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
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// chaos fails the failAt-th port call (1-based) with errBoom; 0 never fails.
// Running one scenario once per call number drives every infrastructure-error
// branch of a use case: each must surface the error and leave the store as it was.
type chaos struct {
	calls, failAt int
}

var errBoom = errors.New("boom")

func (c *chaos) hit() error {
	c.calls++
	if c.failAt != 0 && c.calls == c.failAt {
		return errBoom
	}
	return nil
}

type cDepartures struct {
	in ports.DepartureRepository
	c  *chaos
}

func (r cDepartures) Get(ctx context.Context, id departure.ID) (*departure.Departure, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.Get(ctx, id)
}

func (r cDepartures) List(ctx context.Context, f repository.DepartureFilter) ([]*departure.Departure, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.List(ctx, f)
}

func (r cDepartures) Save(ctx context.Context, d *departure.Departure) error {
	if err := r.c.hit(); err != nil {
		return err
	}
	return r.in.Save(ctx, d)
}

type cSchedules struct {
	in ports.LaneScheduleRepository
	c  *chaos
}

func (r cSchedules) Get(ctx context.Context, id string) (*schedule.LaneSchedule, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.Get(ctx, id)
}

func (r cSchedules) ListActive(ctx context.Context) ([]*schedule.LaneSchedule, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.ListActive(ctx)
}

func (r cSchedules) Save(ctx context.Context, s *schedule.LaneSchedule) error {
	if err := r.c.hit(); err != nil {
		return err
	}
	return r.in.Save(ctx, s)
}

type cLoads struct {
	in ports.LoadRepository
	c  *chaos
}

func (r cLoads) Get(ctx context.Context, id load.ID) (*load.Load, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.Get(ctx, id)
}

func (r cLoads) GetByDeparture(ctx context.Context, id departure.ID) (*load.Load, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.GetByDeparture(ctx, id)
}

func (r cLoads) FindActiveByTracking(ctx context.Context, tn string) (load.ID, error) {
	if err := r.c.hit(); err != nil {
		return "", err
	}
	return r.in.FindActiveByTracking(ctx, tn)
}

func (r cLoads) Save(ctx context.Context, l *load.Load) error {
	if err := r.c.hit(); err != nil {
		return err
	}
	return r.in.Save(ctx, l)
}

type cNodes struct {
	in ports.NodeViewRepository
	c  *chaos
}

func (r cNodes) Get(ctx context.Context, id string) (network.NodeView, error) {
	if err := r.c.hit(); err != nil {
		return network.NodeView{}, err
	}
	return r.in.Get(ctx, id)
}

func (r cNodes) ListPickupEligible(ctx context.Context) ([]network.NodeView, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.ListPickupEligible(ctx)
}

func (r cNodes) Save(ctx context.Context, n network.NodeView) error {
	if err := r.c.hit(); err != nil {
		return err
	}
	return r.in.Save(ctx, n)
}

type cLanes struct {
	in ports.LaneViewRepository
	c  *chaos
}

func (r cLanes) Get(ctx context.Context, id string) (network.LaneView, error) {
	if err := r.c.hit(); err != nil {
		return network.LaneView{}, err
	}
	return r.in.Get(ctx, id)
}

func (r cLanes) ListByOrigin(ctx context.Context, id string) ([]network.LaneView, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.ListByOrigin(ctx, id)
}

func (r cLanes) ListByDestination(ctx context.Context, id string) ([]network.LaneView, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.ListByDestination(ctx, id)
}

func (r cLanes) Save(ctx context.Context, l network.LaneView) error {
	if err := r.c.hit(); err != nil {
		return err
	}
	return r.in.Save(ctx, l)
}

type cVolumes struct {
	in ports.VolumeRepository
	c  *chaos
}

func (r cVolumes) Get(ctx context.Context, id string) (*volume.ExpectedVolume, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.Get(ctx, id)
}

func (r cVolumes) FindByTracking(ctx context.Context, tn string) (*volume.ExpectedVolume, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.FindByTracking(ctx, tn)
}

func (r cVolumes) ListBookedOn(ctx context.Context, id departure.ID) ([]*volume.ExpectedVolume, error) {
	if err := r.c.hit(); err != nil {
		return nil, err
	}
	return r.in.ListBookedOn(ctx, id)
}

func (r cVolumes) Save(ctx context.Context, v *volume.ExpectedVolume) error {
	if err := r.c.hit(); err != nil {
		return err
	}
	return r.in.Save(ctx, v)
}

type cInbox struct {
	in ports.ProcessedEvents
	c  *chaos
}

func (r cInbox) Claim(ctx context.Context, consumer, id string, at time.Time) (bool, error) {
	if err := r.c.hit(); err != nil {
		return false, err
	}
	return r.in.Claim(ctx, consumer, id, at)
}

type cOutbox struct {
	in ports.EventOutbox
	c  *chaos
}

func (r cOutbox) Append(ctx context.Context, evs ...shared.Event) error {
	if err := r.c.hit(); err != nil {
		return err
	}
	return r.in.Append(ctx, evs...)
}

func chaosDeps(c *chaos) func(*usecases.Deps) {
	return func(d *usecases.Deps) {
		d.Departures, d.Schedules, d.Loads = cDepartures{d.Departures, c}, cSchedules{d.Schedules, c}, cLoads{d.Loads, c}
		d.Nodes, d.Lanes, d.Volumes = cNodes{d.Nodes, c}, cLanes{d.Lanes, c}, cVolumes{d.Volumes, c}
		d.Inbox, d.Outbox = cInbox{d.Inbox, c}, cOutbox{d.Outbox, c}
	}
}

// scenario prepares an env with the unwrapped store, then runs one use case.
type scenario struct {
	name string
	prep func(e *env)
	run  func(e *env) error
}

// partialScenarios commit one unit of work per item, so earlier items stay.
var partialScenarios = map[string]bool{"generate": true}

func lockRun(asg, node, dep string) func(e *env) error {
	return func(e *env) error { return e.svc.HandleLocked(e.ctx, nextID(), lockIn(asg, node, dep)) }
}

func chaosScenarios() []scenario {
	two := func(e *env) {
		e.departureAt("dep-1", departure.KindPickup, time.Hour, 1)
		e.departureAt("dep-2", departure.KindPickup, 6*time.Hour, 3)
	}
	bookedOne := func(e *env) {
		two(e)
		e.lock("asg-1", "n-fc", "dep-1")
	}
	world := func(e *env) {
		e.pickupWorld()
		e.generate()
	}
	return []scenario{
		{"define schedule", func(e *env) { e.lane("lane-1", "n-a", "n-b", network.ModeGround, true) }, func(e *env) error {
			_, _, err := e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("FRIDAY", 5)}})
			return err
		}},
		{"redefine schedule", func(e *env) {
			e.lane("lane-1", "n-a", "n-b", network.ModeGround, true)
			_, _, err := e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("FRIDAY", 5)}})
			e.must(err)
		}, func(e *env) error {
			_, _, err := e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("MONDAY", 5)}})
			return err
		}},
		{"retire schedule", func(e *env) {
			e.lane("lane-1", "n-a", "n-b", network.ModeGround, true)
			_, _, err := e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("FRIDAY", 5)}})
			e.must(err)
		}, func(e *env) error { _, err := e.svc.RetireLaneSchedule(e.ctx, "lane-1"); return err }},
		{"list", two, func(e *env) error { _, err := e.svc.ListDepartures(e.ctx, usecases.ListDeparturesQuery{}); return err }},
		{"get", two, func(e *env) error { _, err := e.svc.GetDeparture(e.ctx, "dep-1"); return err }},
		{"change capacity", two, func(e *env) error { _, err := e.svc.ChangeDepartureCapacity(e.ctx, "dep-1", 7); return err }},
		{"cancel with a booking", bookedOne, func(e *env) error { _, err := e.svc.CancelDeparture(e.ctx, "dep-1", "planner"); return err }},
		{"open load", two, func(e *env) error { _, err := e.svc.OpenLoad(e.ctx, "dep-1"); return err }},
		{"add package", func(e *env) {
			e.departureAt("dep-1", departure.KindLinehaul, time.Hour, 3)
			e.openLoad("dep-1")
		}, func(e *env) error { _, err := e.svc.AddPackage(e.ctx, "load-1", "TN1"); return err }},
		{"close load", func(e *env) { two(e); e.openLoad("dep-1") }, func(e *env) error { _, err := e.svc.CloseLoad(e.ctx, "load-1"); return err }},
		{"dispatch load", func(e *env) {
			two(e)
			e.openLoad("dep-1")
			_, err := e.svc.CloseLoad(e.ctx, "load-1")
			e.must(err)
		}, func(e *env) error { _, err := e.svc.DispatchLoad(e.ctx, "load-1"); return err }},
		{"receive load", func(e *env) {
			two(e)
			e.openLoad("dep-1")
			for _, f := range []func(context.Context, string) (any, error){
				func(c context.Context, id string) (any, error) { return e.svc.CloseLoad(c, id) },
				func(c context.Context, id string) (any, error) { return e.svc.DispatchLoad(c, id) },
			} {
				_, err := f(e.ctx, "load-1")
				e.must(err)
			}
		}, func(e *env) error { _, err := e.svc.ReceiveLoad(e.ctx, "load-1"); return err }},
		{"generate", func(e *env) {
			e.scheduleWorld()
			e.vendorFC("n-fc")
			e.lane("lane-fc", "n-fc", "n-a", network.ModeGround, true)
		}, func(e *env) error {
			rep, err := e.svc.GenerateDepartures(e.ctx)
			if err == nil && rep.Failed > 0 {
				return errBoom
			}
			return err
		}},
		{"lock", two, lockRun("asg-1", "n-fc", "dep-1")},
		{"lock rebooks", func(e *env) { two(e); e.book("dep-1", 1) }, lockRun("asg-1", "n-fc", "dep-1")},
		{"lock fails", func(e *env) { e.departureAt("dep-1", departure.KindPickup, time.Hour, 1); e.book("dep-1", 1) }, lockRun("asg-1", "n-fc", "dep-1")},
		{"released", func(e *env) {}, func(e *env) error {
			return e.svc.HandleReleased(e.ctx, nextID(), usecases.ReleasedInput{AssignmentID: "asg-1", ReleaseNo: 1, Facts: volume.Facts{OrderID: "o", GroupNo: 1, NodeID: "n", DepartureID: "d"}})
		}},
		{"reassigned", func(e *env) {}, func(e *env) error {
			return e.svc.HandleReassigned(e.ctx, nextID(), usecases.ReassignedInput{AssignmentID: "asg-1", ToNodeID: "n"})
		}},
		{"cancelled", func(e *env) {}, func(e *env) error {
			return e.svc.HandleCancelled(e.ctx, nextID(), usecases.AssignmentInput{AssignmentID: "asg-1"})
		}},
		{"label issued", func(e *env) {}, func(e *env) error {
			return e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN1"})
		}},
		{"label voided", func(e *env) {
			e.must(e.svc.HandleLabelIssued(e.ctx, nextID(), usecases.LabelIssuedInput{AssignmentID: "asg-1", TrackingNumber: "TN1"}))
		}, func(e *env) error {
			return e.svc.HandleLabelVoided(e.ctx, nextID(), usecases.LabelVoidedInput{TrackingNumber: "TN1"})
		}},
		{"node registered", func(e *env) { e.lane("lane-fc", "n-fc", "n-sc", network.ModeGround, true) }, func(e *env) error {
			return e.svc.HandleNodeRegistered(e.ctx, nextID(), usecases.NodeRegisteredInput{NodeID: "n-sc", NodeType: "SORTATION_CENTER", Operator: "NETWORK", Timezone: zone})
		}},
		{"profile declared", func(e *env) { world(e) }, func(e *env) error {
			p := standardProfile()
			p.Revision = 2
			p.Cutoffs = []network.Cutoff{{LocalTime: "19:00"}}
			return e.svc.HandleProfileDeclared(e.ctx, nextID(), usecases.ProfileDeclaredInput{NodeID: "n-fc", Profile: p})
		}},
		{"node activated", func(e *env) { e.pickupWorld() }, func(e *env) error {
			return e.svc.HandleNodeActivated(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"})
		}},
		{"node suspended", func(e *env) {
			world(e)
			e.lock("asg-1", "n-fc", string(e.listAll(repository.DepartureFilter{})[0].ID()))
		}, func(e *env) error {
			return e.svc.HandleNodeSuspended(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"})
		}},
		{"node retired", func(e *env) { world(e) }, func(e *env) error {
			return e.svc.HandleNodeRetired(e.ctx, nextID(), usecases.NodeInput{NodeID: "n-fc"})
		}},
		{"lane opened", func(e *env) {
			e.must(e.store.Lanes().Save(e.ctx, network.LaneView{ID: "lane-1", Origin: "a", Destination: "b", Mode: network.ModeGround, TransitMinutes: 5, Open: true}))
		}, func(e *env) error {
			return e.svc.HandleLaneOpened(e.ctx, nextID(), usecases.LaneOpenedInput{LaneID: "lane-1", OriginNodeID: "a", DestNodeID: "b", Mode: "GROUND", TransitMinutes: 5})
		}},
		{"lane closed", func(e *env) {
			e.lane("lane-1", "a", "b", network.ModeGround, true)
		}, func(e *env) error {
			return e.svc.HandleLaneClosed(e.ctx, nextID(), usecases.LaneInput{LaneID: "lane-1"})
		}},
	}
}

func TestEveryInfrastructureFailureSurfacesAndRollsBack(t *testing.T) {
	for _, sc := range chaosScenarios() {
		for k := 1; ; k++ {
			c := &chaos{}
			e := newEnv(t, chaosDeps(c))
			sc.prep(e)
			before := e.store.Counts()
			c.calls, c.failAt = 0, k
			err := sc.run(e)
			if c.calls < k {
				if err != nil {
					t.Fatalf("%s: ran out of failure points (%d calls) but failed: %v", sc.name, c.calls, err)
				}
				break
			}
			if !errors.Is(err, errBoom) {
				t.Fatalf("%s: failing call %d must surface, got %v", sc.name, k, err)
			}
			if after := e.store.Counts(); after != before && !partialScenarios[sc.name] {
				t.Fatalf("%s: failing call %d left changes behind: %+v -> %+v", sc.name, k, before, after)
			}
			if k > 400 {
				t.Fatalf("%s: runaway", sc.name)
			}
		}
	}
}
