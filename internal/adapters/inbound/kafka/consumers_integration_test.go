//go:build integration

package kafka_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/google/uuid"

	"github.com/claudioed/linehaul-transportation/internal/adapters/inbound/kafka"
	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/wire"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/clock"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/ids"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/testing/kafkatest"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

func producerEvent(t *testing.T, typ, id string, data any) kafkatest.Message {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource("/retailnetwork/node-topology")
	e.SetType(typ)
	e.SetSubject("subject")
	e.SetTime(time.Now().UTC())
	if err := e.SetData("application/json", data); err != nil {
		t.Fatal(err)
	}
	b, err := e.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return kafkatest.Message{Key: "subject", Value: b, Headers: map[string]string{"content-type": "application/cloudevents+json; charset=UTF-8"}}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestNodeTopologyConsumer_RealKafkaAndPostgres drives the node-topology
// consumer through a real broker into real Postgres: the effect lands, a
// redelivered CloudEvents id is applied once, and a payload that can never be
// applied is dead-lettered to <topic>.dlq instead of wedging the partition.
func TestNodeTopologyConsumer_RealKafkaAndPostgres(t *testing.T) {
	brokers := kafkatest.Brokers(t)
	kafkatest.CreateTopic(t, kafka.NodeTopologyTopic)
	kafkatest.CreateTopic(t, kafka.NodeTopologyTopic+kafka.DLQSuffix)
	pool := pgtest.NewPool(t)
	svc := usecases.New(usecases.Deps{
		UoW: postgres.NewUnitOfWork(pool), Departures: postgres.NewDepartureRepo(pool), Schedules: postgres.NewScheduleRepo(pool),
		Loads: postgres.NewLoadRepo(pool), Nodes: postgres.NewNodeViewRepo(pool), Lanes: postgres.NewLaneViewRepo(pool),
		Volumes: postgres.NewVolumeRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), wire.Encoder{NewID: uuid.NewString}),
		Clock:  clock.System{}, IDs: ids.UUID{},
	})
	consumers := kafka.NewConsumers(brokers, kafka.Groups{NodeTopology: "itest-node", OrderSourcing: "itest-order", Shipping: "itest-ship"}, svc,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = consumers[0].Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		for _, c := range consumers {
			_ = c.Close()
		}
	})

	registered := producerEvent(t, kafka.TypeNodeRegistered, "ev-registered", map[string]any{
		"nodeId": "fc-1", "nodeType": "FULFILLMENT_CENTER", "operator": "VENDOR", "timezone": "UTC",
	})
	kafkatest.Produce(t, kafka.NodeTopologyTopic, registered, registered,
		producerEvent(t, kafka.TypeNodeActivated, "ev-poison", "not an object"),
		producerEvent(t, kafka.TypeNodeActivated, "ev-activated", map[string]any{"nodeId": "fc-1"}),
		producerEvent(t, kafka.TypeLaneOpened, "ev-lane", map[string]any{
			"laneId": "ln-1", "originNodeId": "fc-1", "destinationNodeId": "sc-1", "mode": "GROUND", "nominalTransitMinutes": 90,
		}))

	count := func(query string) int {
		var n int
		if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	eventually(t, "the node ACTIVE and the lane stored", func() bool {
		return count(`SELECT count(*) FROM node_views WHERE id = 'fc-1' AND status = 'ACTIVE'`) == 1 &&
			count(`SELECT count(*) FROM lane_views WHERE id = 'ln-1'`) == 1
	})
	if n := count(`SELECT count(*) FROM processed_events`); n != 3 {
		t.Fatalf("processed_events = %d, want 3 (registered once, activated, lane)", n)
	}
	dead := kafkatest.ReadN(t, kafka.NodeTopologyTopic+kafka.DLQSuffix, 1, 60*time.Second)
	if len(dead) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dead))
	}
}
