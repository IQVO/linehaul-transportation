package postgres

import (
	"context"
	"reflect"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/claudioed/linehaul-transportation/internal/application/outbox"
)

// The outbox row itself needs a container; these tests cover the header
// (de)serialisation and the W3C trace-context injection the relay copies onto
// Kafka messages.

func TestMarshalHeadersRoundTrip(t *testing.T) {
	hs := []outbox.Header{{Key: "content-type", Value: "application/cloudevents+json"}, {Key: "traceparent", Value: "00-abc-def-01"}}
	b, err := marshalHeaders(hs)
	if err != nil {
		t.Fatalf("marshalHeaders: %v", err)
	}
	back, err := unmarshalHeaders(b)
	if err != nil {
		t.Fatalf("unmarshalHeaders: %v", err)
	}
	if !reflect.DeepEqual(back, hs) {
		t.Fatalf("round trip = %v, want %v", back, hs)
	}
	empty, err := marshalHeaders(nil)
	if err != nil || string(empty) != "[]" {
		t.Fatalf("marshalHeaders(nil) = %s, %v, want []", empty, err)
	}
	if _, err := unmarshalHeaders([]byte("{not json")); err == nil {
		t.Fatal("unmarshalHeaders must refuse malformed JSON")
	}
}

func TestHasHeader(t *testing.T) {
	hs := []outbox.Header{{Key: "a"}, {Key: "b"}}
	if !hasHeader(hs, "a") || !hasHeader(hs, "b") || hasHeader(hs, "c") || hasHeader(nil, "a") {
		t.Fatal("hasHeader does not match the stored keys")
	}
}

func TestWithTraceHeaders(t *testing.T) {
	hs := []outbox.Header{{Key: "content-type", Value: "v"}}
	trace := []outbox.Header{{Key: "traceparent", Value: "00-1-2-01"}, {Key: "baggage", Value: "k=v"}}

	if got := withTraceHeaders(hs, nil); &got[0] != &hs[0] {
		t.Fatal("an empty trace context must return the encoder's headers unchanged")
	}
	got := withTraceHeaders(hs, trace)
	if len(got) != 3 || got[0].Key != "content-type" || got[1].Key != "traceparent" || got[2].Key != "baggage" {
		t.Fatalf("withTraceHeaders = %v, want the encoder's headers then the trace headers", got)
	}
	if len(hs) != 1 {
		t.Fatalf("the caller's slice was mutated: %v", hs)
	}
	// An encoder-set header always wins over the injected one, key by key.
	own := []outbox.Header{{Key: "traceparent", Value: "set-by-encoder"}}
	got = withTraceHeaders(own, trace)
	if len(got) != 2 || got[0].Value != "set-by-encoder" || got[1].Key != "baggage" {
		t.Fatalf("withTraceHeaders = %v, want the encoder's traceparent to win and the missing baggage appended", got)
	}
}

// traceHeaders reads the GLOBAL propagator telemetry.Setup installs; save and
// restore it around the test so the rest of the binary is unaffected.
func TestTraceHeadersCarryTheSpanContextInKeyOrder(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	defer otel.SetTextMapPropagator(previous)

	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})

	// without a span on the context nothing is injected
	hs := traceHeaders(context.Background())
	if len(hs) != 0 {
		t.Fatalf("traceHeaders of a context without a span = %v, want none", hs)
	}

	hs = traceHeaders(trace.ContextWithSpanContext(context.Background(), sc))
	if len(hs) != 1 || hs[0].Key != "traceparent" || hs[0].Value != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("traceHeaders = %v, want the W3C traceparent of the span", hs)
	}
}
