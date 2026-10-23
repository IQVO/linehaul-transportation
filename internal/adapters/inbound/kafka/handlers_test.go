package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// rig is the three consumers' handlers over a memory-backed service. The
// handlers are driven through Consumer.HandleMessage, the exact entry point
// the run loop calls, with real CloudEvents bytes.
type rig struct {
	t         *testing.T
	store     *memory.Store
	consumers map[string]*Consumer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	store := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Departures: store.Departures(), Schedules: store.Schedules(), Loads: store.Loads(),
		Nodes: store.Nodes(), Lanes: store.Lanes(), Volumes: store.Volumes(), Inbox: store.Inbox(), Outbox: store.Outbox(),
		Clock: memory.NewClock(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)), IDs: &memory.SequentialIDs{},
	})
	mk := func(h map[string]HandlerFunc) *Consumer {
		return &Consumer{Name: "test", Handlers: h}
	}
	return &rig{t: t, store: store, consumers: map[string]*Consumer{
		"node": mk(NodeTopologyHandlers(svc)), "order": mk(OrderSourcingHandlers(svc)), "ship": mk(ShippingHandlers(svc)),
	}}
}

func (r *rig) send(consumer, typ, id string, data any) error {
	r.t.Helper()
	return r.consumers[consumer].HandleMessage(context.Background(), cloudEvent(r.t, typ, id, data))
}

func (r *rig) mustSend(consumer, typ, id string, data any) {
	r.t.Helper()
	if err := r.send(consumer, typ, id, data); err != nil {
		r.t.Fatalf("%s %s: %v", consumer, typ, err)
	}
}

func TestNodeTopologyHandlers_BuildTheNodeAndLaneViews(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.mustSend("node", TypeNodeRegistered, "e1", map[string]any{
		"nodeId": "fc-1", "nodeType": "FULFILLMENT_CENTER", "operator": "VENDOR", "timezone": "America/Sao_Paulo", "unknownField": 7,
	})
	r.mustSend("node", TypeProfileDeclared, "e2", map[string]any{
		"nodeId": "fc-1", "profileRevision": 1, "timezone": "America/Sao_Paulo",
		"operatingHoursByDay": map[string]any{"monday": map[string]string{"start": "06:00", "end": "22:00"}},
		"holidays":            []string{"2026-12-25"},
		"cutoffs":             []map[string]any{{"localTime": "18:00", "days": []string{"monday"}, "maxOrders": 40}},
		"throughputCap":       map[string]any{"value": 10, "timeUnit": "HOUR"},
	})
	n, err := r.store.Nodes().Get(ctx, "fc-1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != network.FulfillmentCenter || n.Operator != network.OperatorVendor || n.Status != network.StatusProposed {
		t.Fatalf("node = %+v", n)
	}
	assertProfile(t, n.Profile)

	r.mustSend("node", TypeNodeActivated, "e3", map[string]any{"nodeId": "fc-1"})
	r.mustSend("node", TypeLaneOpened, "e4", map[string]any{
		"laneId": "ln-1", "originNodeId": "fc-1", "destinationNodeId": "sc-1", "mode": "GROUND", "nominalTransitMinutes": 120,
	})
	r.mustSend("node", TypeLaneClosed, "e5", map[string]any{"laneId": "ln-1"})
	lane, err := r.store.Lanes().Get(ctx, "ln-1")
	if err != nil || lane.Open || lane.Origin != "fc-1" || lane.Destination != "sc-1" || lane.TransitMinutes != 120 {
		t.Fatalf("lane = %+v, %v", lane, err)
	}
	if n, _ = r.store.Nodes().Get(ctx, "fc-1"); n.Status != network.StatusActive {
		t.Fatalf("status after NodeActivated = %s", n.Status)
	}

	r.mustSend("node", TypeNodeSuspended, "e6", map[string]any{"nodeId": "fc-1"})
	if n, _ = r.store.Nodes().Get(ctx, "fc-1"); n.Status != network.StatusSuspended {
		t.Fatalf("status after NodeSuspended = %s", n.Status)
	}
	r.mustSend("node", TypeNodeRetired, "e7", map[string]any{"nodeId": "fc-1"})
	if n, _ = r.store.Nodes().Get(ctx, "fc-1"); n.Status != network.StatusRetired {
		t.Fatalf("status after NodeRetired = %s", n.Status)
	}
}

func assertProfile(t *testing.T, p *network.Profile) {
	t.Helper()
	if p == nil {
		t.Fatal("profile not stored")
	}
	if p.Revision != 1 || p.Timezone != "America/Sao_Paulo" || p.Hours["monday"] != (network.Hours{Start: "06:00", End: "22:00"}) {
		t.Fatalf("profile = %+v", p)
	}
	if len(p.Holidays) != 1 || len(p.Cutoffs) != 1 || p.Cutoffs[0].LocalTime != "18:00" || p.Cutoffs[0].MaxOrders != 40 {
		t.Fatalf("profile cutoffs/holidays = %+v", p)
	}
	if p.Cap.Value != 10 || p.Cap.TimeUnit != network.CapHour {
		t.Fatalf("profile cap = %+v", p.Cap)
	}
}

func TestOrderSourcingAndShippingHandlers_ProjectTheExpectedVolume(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	facts := map[string]any{"assignmentId": "as-1", "releaseNo": 1, "orderId": "or-1", "groupNo": 1, "nodeId": "fc-1", "pickupDepartureId": "dep-1"}
	r.mustSend("order", TypeReleased, "o1", facts)
	r.mustSend("ship", TypeLabelIssued, "s1", map[string]any{"assignmentId": "as-1", "trackingNumber": "TRK1", "packageNo": 1})
	vol, err := r.store.Volumes().Get(ctx, "as-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := vol.Snapshot(); got.OrderID != "or-1" || got.NodeID != "fc-1" || got.DepartureID != "dep-1" || len(got.TrackingNumbers) != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
	if _, err := r.store.Volumes().FindByTracking(ctx, "TRK1"); err != nil {
		t.Fatalf("FindByTracking: %v", err)
	}

	r.mustSend("order", TypeReassigned, "o2", map[string]any{"assignmentId": "as-1", "toNodeId": "fc-2"})
	if vol, _ = r.store.Volumes().Get(ctx, "as-1"); vol.Snapshot().NodeID != "fc-2" {
		t.Fatalf("node after reassignment = %s", vol.Snapshot().NodeID)
	}
	r.mustSend("ship", TypeLabelVoided, "s2", map[string]any{"trackingNumber": "TRK1"})
	if _, err := r.store.Volumes().FindByTracking(ctx, "TRK1"); err == nil {
		t.Fatal("tracking number survived LabelVoided")
	}
	r.mustSend("order", TypeCancelled, "o3", map[string]any{"assignmentId": "as-1"})
	if vol, _ = r.store.Volumes().Get(ctx, "as-1"); vol.Snapshot().State != volume.StateCancelled {
		t.Fatalf("state after FulfillmentCancelled = %s", vol.Snapshot().State)
	}
}

func TestOrderSourcingHandlers_LockedWithoutDepartureReportsBookingFailed(t *testing.T) {
	r := newRig(t)
	r.mustSend("order", TypeLocked, "l1", map[string]any{
		"assignmentId": "as-9", "orderId": "or-9", "groupNo": 1, "promiseId": "pr-9", "nodeId": "fc-1", "pickupDepartureId": "dep-missing",
	})
	if c := r.store.Counts(); c.Volumes != 1 || c.OutboxEvents != 1 || c.Processed != 1 {
		t.Fatalf("counts after a lock on an unknown departure = %+v (want the row, one BookingFailed, one inbox row)", c)
	}
}

func TestHandlers_AreIdempotentOnTheCloudEventsID(t *testing.T) {
	r := newRig(t)
	data := map[string]any{"nodeId": "fc-1", "nodeType": "FULFILLMENT_CENTER", "operator": "VENDOR", "timezone": "UTC"}
	r.mustSend("node", TypeNodeRegistered, "same-id", data)
	before := r.store.Counts()
	r.mustSend("node", TypeNodeRegistered, "same-id", data)
	if after := r.store.Counts(); after != before {
		t.Fatalf("redelivery changed the store: %+v -> %+v", before, after)
	}
}

func TestHandleMessage_DeterministicProblems(t *testing.T) {
	r := newRig(t)
	c := r.consumers["node"]
	ctx := context.Background()
	if err := c.HandleMessage(ctx, []byte(`{"not":"a cloud event"}`)); err != nil {
		t.Fatalf("a non-CloudEvent must be skipped, got %v", err)
	}
	if err := r.send("node", "com.retailnetwork.logistics.node-topology.node.SomethingNew", "x1", map[string]any{}); err != nil {
		t.Fatalf("an unhandled type must be ignored, got %v", err)
	}
	if err := r.send("node", TypeNodeActivated, "x2", "a string, not an object"); err != nil {
		t.Fatalf("a malformed payload without a DLQ is skipped, got %v", err)
	}
	if err := r.send("node", TypeNodeRegistered, "x3", map[string]any{"nodeId": "bad id!", "nodeType": "FULFILLMENT_CENTER", "operator": "VENDOR"}); err != nil {
		t.Fatalf("an invalid value without a DLQ is skipped, got %v", err)
	}
	if n := r.store.Counts().Nodes; n != 0 {
		t.Fatalf("a refused event stored %d node rows", n)
	}
}

func TestHandleMessage_DeadLetterInvalidEscalatesPoison(t *testing.T) {
	r := newRig(t)
	c := r.consumers["node"]
	c.DLQ = noopDLQ{}
	c.DeadLetterInvalid = true
	for name, data := range map[string]any{
		"malformed payload": "a string, not an object",
		"invalid value":     map[string]any{"nodeId": "bad id!", "nodeType": "FULFILLMENT_CENTER", "operator": "VENDOR"},
	} {
		err := c.HandleMessage(context.Background(), cloudEvent(t, TypeNodeRegistered, "p-"+name, data))
		if err == nil || !errors.Is(err, errPoison) {
			t.Fatalf("%s: err = %v, want a poison error", name, err)
		}
	}
}

func TestHandleMessage_TransientFailureIsReturned(t *testing.T) {
	boom := errors.New("database down")
	c := &Consumer{Name: "t", Handlers: map[string]HandlerFunc{
		TypeNodeActivated: func(context.Context, ce.Event) error { return boom },
	}}
	if err := c.HandleMessage(context.Background(), cloudEvent(t, TypeNodeActivated, "t1", map[string]any{"nodeId": "n1"})); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the transient error", err)
	}
}

func TestNewConsumers_OnePerProducerWithItsOwnGroup(t *testing.T) {
	cs := NewConsumers([]string{"localhost:1"}, Groups{NodeTopology: "g1", OrderSourcing: "g2", Shipping: "g3"}, nil, nil)
	topics := map[string]int{}
	for _, c := range cs {
		topics[c.Topic] = len(c.Handlers)
		if !c.DeadLetterInvalid || c.DLQ == nil {
			t.Errorf("%s: DLQ not wired", c.Name)
		}
		_ = c.Close()
	}
	want := map[string]int{NodeTopologyTopic: 7, OrderSourcingTopic: 4, ShippingTopic: 2}
	for topic, n := range want {
		if topics[topic] != n {
			t.Errorf("topic %s has %d handlers, want %d", topic, topics[topic], n)
		}
	}
}

// noopDLQ accepts every dead letter.
type noopDLQ struct{}

func (noopDLQ) WriteMessages(context.Context, ...kafkago.Message) error { return nil }
func (noopDLQ) Close() error                                            { return nil }

// cloudEvent encodes a structured-mode CloudEvents message of the given full
// type (the producers' types, which are not this service's own).
func cloudEvent(t *testing.T, typ, id string, data any) []byte {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource("/retailnetwork/test-producer")
	e.SetType(typ)
	e.SetSubject("subject-1")
	e.SetTime(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	if err := e.SetData(cloudevents.DataContentType, data); err != nil {
		t.Fatal(err)
	}
	b, err := e.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
