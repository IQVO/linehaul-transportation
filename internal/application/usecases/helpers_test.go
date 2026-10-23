package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// t0 is Friday 2026-10-23 12:00 UTC, 09:00 in Sao Paulo (UTC-3, no DST).
var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

const zone = "America/Sao_Paulo"

type env struct {
	t      *testing.T
	ctx    context.Context
	store  *memory.Store
	clock  *memory.Clock
	svc    *usecases.Service
	outbox *memory.Outbox
}

func newEnv(t *testing.T, mutate ...func(*usecases.Deps)) *env {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewClock(t0)
	deps := usecases.Deps{
		UoW: store.UnitOfWork(), Departures: store.Departures(), Schedules: store.Schedules(), Loads: store.Loads(),
		Nodes: store.Nodes(), Lanes: store.Lanes(), Volumes: store.Volumes(), Inbox: store.Inbox(), Outbox: store.Outbox(),
		Clock: clock, IDs: &memory.SequentialIDs{},
	}
	for _, m := range mutate {
		m(&deps)
	}
	return &env{t: t, ctx: context.Background(), store: store, clock: clock, svc: usecases.New(deps), outbox: store.Outbox()}
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, label string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v, want %v", label, err, target)
	}
}

// ---- seeding ---------------------------------------------------------------

func (e *env) node(id string, typ network.NodeType, op network.Operator, status network.Status) network.NodeView {
	e.t.Helper()
	n := network.NodeView{ID: id, Type: typ, Operator: op, Timezone: zone, Status: status, Generation: 1}
	e.must(e.store.Nodes().Save(e.ctx, n))
	return n
}

func standardProfile() network.Profile {
	h := map[string]network.Hours{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday"} {
		h[d] = network.Hours{Start: "06:00", End: "22:00"}
	}
	return network.Profile{
		Revision: 1, Timezone: zone, Hours: h,
		Cutoffs: []network.Cutoff{{LocalTime: "18:00"}}, Cap: network.ThroughputCap{Value: 10, TimeUnit: network.CapHour},
	}
}

// vendorFC saves an ACTIVE vendor fulfillment center with a profile.
func (e *env) vendorFC(id string) network.NodeView {
	e.t.Helper()
	p := standardProfile()
	n := network.NodeView{ID: id, Type: network.FulfillmentCenter, Operator: network.OperatorVendor, Timezone: zone, Status: network.StatusActive, Generation: 1, Profile: &p}
	e.must(e.store.Nodes().Save(e.ctx, n))
	return n
}

func (e *env) lane(id, origin, dest string, mode network.Mode, open bool) {
	e.t.Helper()
	e.must(e.store.Lanes().Save(e.ctx, network.LaneView{ID: id, Origin: origin, Destination: dest, Mode: mode, TransitMinutes: 120, Open: open}))
}

// departureAt saves a SCHEDULED departure whose cutoff is `in` after t0.
func (e *env) departureAt(id string, kind departure.Kind, in time.Duration, capacity int) *departure.Departure {
	e.t.Helper()
	return e.departureFrom(id, "n-fc", kind, in, capacity)
}

func (e *env) departureFrom(id, origin string, kind departure.Kind, in time.Duration, capacity int) *departure.Departure {
	e.t.Helper()
	d, _, err := departure.Schedule(departure.Spec{
		ID: departure.ID(id), LaneID: "lane-1", Kind: kind, OriginNodeID: origin, DestinationNodeID: "n-sc",
		OriginType: network.FulfillmentCenter, DestinationType: network.SortationCenter,
		CutoffAt: t0.Add(in), DepartsAt: t0.Add(in), ArrivesAt: t0.Add(in + 2*time.Hour), CapacityPackages: capacity,
	}, true, t0)
	e.must(err)
	e.must(e.store.Departures().Save(e.ctx, d))
	return d
}

func (e *env) departure(id string) *departure.Departure {
	e.t.Helper()
	d, err := e.store.Departures().Get(e.ctx, departure.ID(id))
	e.must(err)
	return d
}

// ---- observing -------------------------------------------------------------

func (e *env) eventNames() []string {
	var out []string
	for _, ev := range e.outbox.Events() {
		out = append(out, ev.EventName())
	}
	return out
}

func sameNames(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

func eventsOf[T shared.Event](e *env) []T {
	var out []T
	for _, ev := range e.outbox.Events() {
		if v, ok := ev.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// unchanged proves that a failed use case stored and changed nothing.
func (e *env) unchanged(before memory.Counts) {
	e.t.Helper()
	if after := e.store.Counts(); after != before {
		e.t.Fatalf("store changed: %+v -> %+v", before, after)
	}
}

// book books n packages on a stored departure.
func (e *env) book(id string, n int) {
	e.t.Helper()
	d := e.departure(id)
	for i := 0; i < n; i++ {
		_, err := d.Book(e.clock.Now())
		e.must(err)
		e.must(e.store.Departures().Save(e.ctx, d))
	}
}
