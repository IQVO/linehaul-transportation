//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/application/outbox"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

func probeMessage(id string) outbox.Message {
	return outbox.Message{
		EventID: id, Topic: "t", EventType: "x.Probed", Subject: "d-1", Key: []byte("d-1"), Value: []byte(`{}`),
		Headers: []outbox.Header{{Key: "content-type", Value: "application/cloudevents+json; charset=UTF-8"}},
	}
}

// An Insert made under a span stores that span's traceparent in the row's
// headers, next to the encoder's own header, and a span-less Insert stores none.
func TestOutboxInsertStoresTheActiveTraceparent(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	pool := pgtest.NewPool(t)
	repo := postgres.NewOutboxRepo(pool)

	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "POST /loads/{loadId}:close")
	defer span.End()
	if err := repo.Insert(ctx, probeMessage("evt-trace-1")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(context.Background(), probeMessage("evt-trace-2")); err != nil {
		t.Fatal(err)
	}

	stored := map[string][]struct{ Key, Value string }{}
	for _, id := range []string{"evt-trace-1", "evt-trace-2"} {
		var raw string
		if err := pool.QueryRow(context.Background(), `SELECT headers::text FROM outbox_events WHERE event_id = $1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var hs []struct{ Key, Value string }
		if err := json.Unmarshal([]byte(raw), &hs); err != nil {
			t.Fatal(err)
		}
		stored[id] = hs
	}
	want := "00-" + span.SpanContext().TraceID().String() + "-" + span.SpanContext().SpanID().String() + "-01"
	if hs := stored["evt-trace-1"]; len(hs) != 2 || hs[0].Key != "content-type" || hs[1].Key != "traceparent" || hs[1].Value != want {
		t.Errorf("row under a span has headers %v, want content-type and traceparent %s", hs, want)
	}
	if hs := stored["evt-trace-2"]; len(hs) != 1 || hs[0].Key != "content-type" {
		t.Errorf("row without a span has headers %v, want only content-type", hs)
	}
}
