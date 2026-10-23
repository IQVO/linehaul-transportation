//go:build integration

package outbox_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/wire"
	outboundkafka "github.com/claudioed/linehaul-transportation/internal/adapters/outbound/kafka"
	relay "github.com/claudioed/linehaul-transportation/internal/adapters/outbound/outbox"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/application/outbox"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/testing/kafkatest"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

// TestRelay_RealPostgresAndKafka_PublishesCloudEventsKeyedByAggregate writes
// events through the real EventOutbox inside a real UnitOfWork, lets the real
// relay drain them to a real broker, and reads the exact CloudEvents back.
func TestRelay_RealPostgresAndKafka_PublishesCloudEventsKeyedByAggregate(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	hdr := shared.Header{Version: 2, At: at}

	// Events of this stream go to the fixed topics of the service; the test reads
	// them with a unique prefix by pointing the outbox rows at a private topic.
	topic := kafkatest.Topic(t, "linehaul-relay")
	repo := postgres.NewOutboxRepo(pool)
	out := postgres.NewEventOutbox(repo, retopic{topic: topic, inner: wire.Encoder{NewID: uuid.NewString}})
	err := postgres.NewUnitOfWork(pool).Do(ctx, func(ctx context.Context) error {
		return out.Append(ctx,
			departure.DepartureCancelled{Header: hdr, DepartureID: "dep-1", Reason: "NODE_SUSPENDED", CancelledAt: at},
			departure.DepartureCapacityChanged{Header: hdr, DepartureID: "dep-1", CapacityPackages: 5, BookedPackages: 1, RemainingPackages: 4},
		)
	})
	if err != nil {
		t.Fatal(err)
	}

	sink := outboundkafka.NewRelaySink(kafkatest.Brokers(t))
	t.Cleanup(func() { _ = sink.Close() })
	r := relay.NewOutboxRelay(repo, sink, slog.New(slog.DiscardHandler), relay.WithInterval(20*time.Millisecond), relay.WithMaxAttempts(5))
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = r.Run(runCtx) }()
	pgtest.WaitAllPublished(t, pool)

	got := kafkatest.ReadN(t, topic, 2, 30*time.Second)
	for i, want := range []string{"DepartureCancelled", "DepartureCapacityChanged"} {
		ev, err := cloudevents.Decode(got[i].Value)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type() != cloudevents.Type("departure", want) || got[i].Key != "dep-1" || ev.Subject() != "dep-1" {
			t.Errorf("message %d: type %s key %s subject %s", i, ev.Type(), got[i].Key, ev.Subject())
		}
		if got[i].Headers["content-type"] != cloudevents.MediaType {
			t.Errorf("content-type = %q", got[i].Headers["content-type"])
		}
		var data map[string]any
		if err := json.Unmarshal(ev.Data(), &data); err != nil || data["departureId"] != "dep-1" {
			t.Errorf("data = %v (%v)", data, err)
		}
	}
}

// retopic sends the messages of an encoder to another topic.
type retopic struct {
	topic string
	inner wire.Encoder
}

func (r retopic) Encode(ev shared.Event) (m outbox.Message, err error) {
	m, err = r.inner.Encode(ev)
	m.Topic = r.topic
	return m, err
}
