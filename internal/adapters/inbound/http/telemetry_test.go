package http_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	inbound "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
)

// recordSpans installs a recording TracerProvider for the test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return recorder
}

// logLines decodes every JSON line the logger wrote.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// serve sends one GET through the real router (memory-backed use cases) and
// returns the status.
func serve(t *testing.T, logger *slog.Logger, path string) int {
	t.Helper()
	store := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Departures: store.Departures(), Schedules: store.Schedules(), Loads: store.Loads(),
		Nodes: store.Nodes(), Lanes: store.Lanes(), Volumes: store.Volumes(), Inbox: store.Inbox(), Outbox: store.Outbox(),
		Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{},
	})
	rec := httptest.NewRecorder()
	inbound.NewRouter(&inbound.Server{Service: svc}, logger, "").ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, path, nethttp.NoBody))
	return rec.Code
}

func TestRouterServerSpanIsNamedAfterTheRoutePattern(t *testing.T) {
	recorder := recordSpans(t)

	serve(t, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), "/departures/a-distinct-departure-id")

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if spans[0].SpanKind() != trace.SpanKindServer {
		t.Errorf("span kind = %v, want server", spans[0].SpanKind())
	}
	if got := spans[0].Name(); !strings.HasSuffix(got, "/departures/{departureId}") {
		t.Errorf("span name = %q, want the route pattern /departures/{departureId} (one name per route, not per id)", got)
	}
}

// The trace_id stamping itself is covered in the telemetry package and, end to
// end, by cmd/api's TestRouterLogCarriesTheServerSpanTraceID; this test pins the
// shape of the request line.
func TestRouterRequestLogShape(t *testing.T) {
	var buf bytes.Buffer

	if got := serve(t, slog.New(slog.NewJSONHandler(&buf, nil)), "/departures/missing"); got != 404 {
		t.Fatalf("status = %d, want 404 for an unknown departure", got)
	}

	lines := logLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want exactly the request line: %v", len(lines), lines)
	}
	line := lines[0]
	if line["msg"] != "http request" || line["method"] != "GET" || line["status"] != float64(404) {
		t.Errorf("request line = %v", line)
	}
	if line["level"] != "WARN" {
		t.Errorf("level = %v, want WARN for a 4xx", line["level"])
	}
	if line["route"] != "/departures/{departureId}" {
		t.Errorf("route = %v, want the pattern, never the raw path", line["route"])
	}
	if id, _ := line["request_id"].(string); id == "" {
		t.Error("request_id is empty: RequestID must run before RequestLogger")
	}
	for _, key := range []string{"duration_ms", "bytes"} {
		if _, ok := line[key]; !ok {
			t.Errorf("request line has no %s", key)
		}
	}
}

func TestRouterDoesNotLogProbes(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	serve(t, logger, "/healthz")
	serve(t, logger, "/readyz")

	if buf.Len() != 0 {
		t.Errorf("probes were logged: %s", buf.String())
	}
}
