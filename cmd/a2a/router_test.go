package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	inbounda2a "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/a2a"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/telemetry"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

const testServiceName = "linehaul-transportation-a2a"

// newTestAgent builds the router exactly as run() does (newAgent), over an
// in-memory store holding one departure. The memory adapter is a test double:
// it is imported by this _test file only, never by the binary.
func newTestAgent(t *testing.T, logger *slog.Logger) http.Handler {
	t.Helper()
	store := memory.NewStore()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	d, _, err := departure.Schedule(departure.Spec{
		ID: "dep-1", LaneID: "lane-1", Kind: departure.KindLinehaul, OriginNodeID: "n-a", DestinationNodeID: "n-b",
		OriginType: network.SortationCenter, DestinationType: network.SortationCenter,
		CutoffAt: now.Add(time.Hour), DepartsAt: now.Add(time.Hour), ArrivesAt: now.Add(2 * time.Hour), CapacityPackages: 120,
	}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Departures().Save(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	deps := inbounda2a.Deps{Departures: usecases.New(usecases.Deps{Departures: store.Departures()})}
	return newAgent(deps, testAgentURL, "test", logger, testServiceName)
}

func quietAgent(t *testing.T) http.Handler { return newTestAgent(t, quietLogger()) }

func TestRouter_HealthzServesOKAndIsNotLogged(t *testing.T) {
	var buf bytes.Buffer
	h := newTestAgent(t, slog.New(slog.NewJSONHandler(&buf, nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("GET /healthz: %d %q, want 200 {\"status\":\"ok\"}", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /healthz Content-Type = %q, want application/json", ct)
	}
	if buf.Len() != 0 {
		t.Fatalf("/healthz was logged: %s", buf.String())
	}
}

func TestRouter_ServesTheAgentCardAtTheWellKnownPath(t *testing.T) {
	rec := httptest.NewRecorder()
	quietAgent(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent card: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"linehaul-transportation"`, testAgentURL, `"list_departures"`, `"get_departure"`} {
		if !strings.Contains(body, want) {
			t.Errorf("agent card lacks %s: %s", want, body)
		}
	}
}

// TestRouter_BothMountsReachTheJSONRPCHandler pins the fleet's
// <CTX>_A2A_ENDPOINT convention: the endpoint answers at "/" and at "/a2a",
// without credentials (or with any), and the use cases behind it are the wired
// ones (the seeded departure is read).
func TestRouter_BothMountsReachTheJSONRPCHandler(t *testing.T) {
	h := quietAgent(t)
	for _, path := range []string{"/", "/a2a"} {
		for _, auth := range []string{"", "Bearer nonsense"} {
			t.Run(path+" auth="+auth, func(t *testing.T) {
				body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"get_departure","arguments":{"departureId":"dep-1"}}}]}}}`
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("A2A-Version", "1.0")
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				raw, _ := io.ReadAll(rec.Body)
				if rec.Code != http.StatusOK || !strings.Contains(string(raw), `"jsonrpc"`) {
					t.Fatalf("POST %s: %d %q does not look like a JSON-RPC reply", path, rec.Code, raw)
				}
				if !strings.Contains(string(raw), `"capacityPackages":120`) {
					t.Fatalf("POST %s: reply %q lacks the seeded departure read through the wired use case", path, raw)
				}
			})
		}
	}
}

// TestRouter_ACardFetchedFromTheBinaryIsDialable runs the discovery flow a
// delegating agent runs against the real router: fetch the card, build a
// client from it, call a skill.
func TestRouter_ACardFetchedFromTheBinaryIsDialable(t *testing.T) {
	srv := httptest.NewServer(quietAgent(t))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	// The card advertises the configured URL, not the test server's: point the
	// client at the test server the way a gateway-rewritten route would.
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": "list_departures", "arguments": map[string]any{}}))})
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T", resp)
	}
	if text, _ := msg.Parts[1].Content.(a2a.Text); !strings.Contains(string(text), "dep-1") {
		t.Fatalf("summary = %q, want the seeded departure listed", text)
	}
}

func TestLoadTelemetryEnvDefaultsToTheA2AServiceName(t *testing.T) {
	got := loadTelemetryEnv(func(string) string { return "" })
	if got.serviceName != testServiceName || got.otlpEndpoint != telemetry.DefaultEndpoint || got.serviceVersion != telemetry.DefaultServiceVersion {
		t.Fatalf("defaults = %+v", got)
	}
}

func TestLoadTelemetryEnvReadsTheOTelContract(t *testing.T) {
	env := map[string]string{"OTEL_SERVICE_NAME": "svc", "SERVICE_VERSION": "1.2.3", "OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4317"}
	got := loadTelemetryEnv(func(k string) string { return env[k] })
	want := telemetryEnv{serviceName: "svc", serviceVersion: "1.2.3", otlpEndpoint: "collector:4317"}
	if got != want {
		t.Fatalf("env = %+v, want %+v", got, want)
	}
}

// One A2A request yields a server span and a request log line whose
// trace_id/span_id are that span's.
func TestRouterLogCarriesTheServerSpanTraceID(t *testing.T) {
	tp, mp := otel.GetTracerProvider(), otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetTracerProvider(tp); otel.SetMeterProvider(mp) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	var buf bytes.Buffer
	h := newTestAgent(t, slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(&buf, nil))))

	// An unknown skill is answered in-band by the adapter, so the request
	// reaches the executor without any dependency.
	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"nope","arguments":{}}}]}}}`
	req := httptest.NewRequest(http.MethodPost, "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var server, skill int
	var serverSpan sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		switch {
		case s.SpanKind() == trace.SpanKindServer:
			server++
			serverSpan = s
		case s.Name() == "a2a.skill nope":
			skill++
		}
	}
	if server != 1 || skill != 1 {
		t.Fatalf("server spans = %d, a2a.skill spans = %d, want 1 and 1", server, skill)
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not one JSON line: %q (%v)", buf.String(), err)
	}
	sc := serverSpan.SpanContext()
	if line[telemetry.TraceIDKey] != sc.TraceID().String() || line[telemetry.SpanIDKey] != sc.SpanID().String() {
		t.Fatalf("log ids = %v/%v, want the server span's %s/%s", line[telemetry.TraceIDKey], line[telemetry.SpanIDKey], sc.TraceID(), sc.SpanID())
	}
}
