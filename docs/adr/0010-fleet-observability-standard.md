# ADR 0010: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26).

## Context

linehaul-transportation logged JSON with `log/slog` and propagated a trace into its outbox rows, but had no
OpenTelemetry SDK wiring: no trace or span id on a log line, no request id, no request log, no metrics, no server
spans, no consumer spans and no OpenTelemetry settings in the Helm chart. The warehouse-systems services
(inventory-storage, wes-work-planning) already share one pipeline and one convention, recorded in their ADRs
"standard metrics convention" (inventory-storage ADR 0016, wes-work-planning ADR 0013); customer-ordering ADR 0007
carries the same standard for the retail network:

- pods export OTLP/gRPC to the in-cluster Collector (`otel-collector.observability.svc.cluster.local:4317`), which
  feeds Jaeger (traces) and a Prometheus exporter (metrics);
- logs stay on stdout/stderr as JSON for Alloy and Loki.

Without the same wiring a departure could not be followed from the REST call through the Kafka event to the consumer
in another service, a log line could not be joined to its trace, and no dashboard could show dispatch rate, errors or
latency. A service-specific setup was rejected (every repo should answer the same query the same way), and so were the
OTel Go auto-instrumentation agents (they cannot follow a trace across `segmentio/kafka-go`, which has no
instrumentation, nor name spans by the chi route pattern).

## Decision

Tier 1 (identical in every repo) and Tier 2 (this context's business metric):

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup` (OTLP/gRPC traces and metrics, W3C
   TraceContext + Baggage propagator, Go runtime metrics; exporters dial lazily, so no Collector means dropped
   telemetry, never a blocked boot or request; returns a shutdown func) and `WithTraceContext` (stamps
   `trace_id`/`span_id` on a log record written with a span-carrying context). Only the composition roots (`cmd/api`,
   `cmd/mcp`) and the other adapters import it; `internal/domain` and `internal/application` never import
   OpenTelemetry (the application layer sees only the `ports.LoadMetrics` interface).
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(...)))`, `LOG_LEVEL`, uppercase levels. Code on
   a request or message path uses the `*Context` slog variants (the MCP error mapper, the horizon-roll refusals); the
   bare variants have no span.
3. **HTTP middleware order** on both chi routers (REST and MCP): `RequestID` -> `otelchi.Middleware(serviceName,
   WithChiRoutes(r))` -> `otelchimetric.NewServerRequestDuration` -> `RequestLogger` -> `Recoverer`; the existing
   `rejectNUL` and Idempotency-Key middleware stay after them. `telemetry.Setup` runs before the router is built
   (otelchimetric binds the global MeterProvider at construction) and is flushed last on shutdown. The request line
   carries `method`, `route` (the chi pattern, never the raw path, which holds ids), `status`, `duration_ms`, `bytes`
   and `request_id`; 5xx is ERROR, 4xx WARN, the rest INFO; `/healthz` and `/readyz` are not logged. `RequestLogger`
   lives in `internal/adapters/inbound/requestlog`, not in the REST adapter, because `cmd/mcp` may not import the REST
   adapter and both routers must log identically.
4. **Traces over Kafka.** `OutboxRepo.Insert` already stamped `traceparent`/`tracestate`/`baggage` from the active
   span into the stored outbox headers, inside the transaction that writes the aggregate (unchanged by this ADR; an
   integration test now pins it). The relay publishes the headers as Kafka headers; each inbound consumer extracts
   them (`internal/adapters/inbound/kafka/tracing.go`) and runs every handling attempt in a consumer span named
   `<topic> process`, so the use case's logs carry the publisher's trace id and an event enqueued while handling
   continues the trace. Only extra headers exist: the CloudEvents attributes and body, and the encoder's output, are
   unchanged (ADR 0004).
5. **Environment contract.** `cmd/api` and `cmd/mcp` read `OTEL_SERVICE_NAME` (defaults `linehaul-transportation` and
   `linehaul-transportation-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT` (default `telemetry.DefaultEndpoint`, a bare
   `host:port` or a URL), `SERVICE_VERSION`, `ENVIRONMENT` (read by `Setup`) and `LOG_LEVEL`.
6. **Chart.** `otel.enabled`, `otel.endpoint`, `otel.serviceName` render `OTEL_EXPORTER_OTLP_ENDPOINT` and
   `OTEL_SERVICE_NAME` (`<name>-mcp` for the mcp component); `SERVICE_VERSION` is the image tag (appVersion when
   empty) and `ENVIRONMENT` is the top-level `environment`, for the api and the mcp component. `extraEnv` refuses the
   four chart-owned names. The frontend component is static nginx bytes with no Go process and exports nothing.
   `charts/linehaul-transportation/tests/render_test.sh` asserts all of it and scans `cmd/` for the variables the
   binaries read.
7. **Tier 2 business metric.** One counter, `linehaul.loads.dispatched`, unit `{load}`, attribute `outcome`
   (`dispatched` | `rejected`) and, on a rejection, `reason` (the problem slug the REST adapter answers with:
   `not-found`, `validation-failed`, `illegal-transition`, `concurrent-update`, `internal-error`).
   `ports.LoadMetrics` is the application port; `Service.DispatchLoad` calls it once per attempt that had something
   to do (dispatching a load that already DEPARTED is an idempotent replay, not an attempt); the OTel implementation
   is in the telemetry adapter and is injected from `cmd/api`; a nil `Deps.Metrics` is a no-op.

### Why `linehaul.loads.dispatched`

The context turns lanes into departures and reports when loads leave and arrive (ADR 0002). Of its REST commands,
`:dispatch` is the one that moves freight: it is the moment a load leaves its origin node and `LoadDeparted` reaches
every downstream consumer. Scheduling and capacity changes are policy and planner actions, and bookings are driven by
other contexts' events. A falling dispatched share with a rising `illegal-transition` says operators try to dispatch
loads that were never closed; a rising `internal-error` says the service itself is failing. It follows the fleet
convention: dotted lowercase name, one counter with an `outcome` attribute rather than two counters, UCUM unit, a
description that says what a human should conclude when it moves.

### Assumptions

- The cmd/api logger keeps writing to stderr, as before; Alloy collects both streams, so moving it to stdout was not
  part of this change.
- The outbox trace stamping existed before this ADR, so no outbox code changed; only the consumer side and the tests
  are new.
- Ids were never added to domain events or CloudEvents attributes; trace context travels only as Kafka headers.

Not part of this ADR: dashboards and alert rules, which live in warehouse-infra and are generated per deployed context
from the instrument names above.

## Consequences

- A request, its log lines, the events it enqueued and the consumers that handle them in other services share one
  trace id; `trace_id` in Loki jumps to Jaeger.
- Request rate, errors and duration come from `http.server.request.duration` (otelchimetric) with no hand-rolled
  counter; dispatch health from `linehaul.loads.dispatched`.
- Every new log call on a request or message path must use a `*Context` variant, or its line carries no trace id.
- A consumer span is created per handling attempt, so a retried message shows as several spans under the same parent.
- The trace parent of a consumed event is the span active at the outbox write (the REST request or the upstream
  consumer span), not a separate publish span: the relay is asynchronous and adds no span of its own.
