---
id: observability
title: Observability
sidebar_label: Observability
description: "Metrics, traces, logs, exporter settings and the dashboards and alerts that exist for Linehaul Transportation (ADR 0010)."
---

# Observability

Linehaul Transportation follows the fleet observability standard ([ADR 0010](/docs/adr/0010-fleet-observability-standard)): the pod pushes OTLP/gRPC
to the OpenTelemetry Collector, which feeds Jaeger (traces) and Prometheus (metrics); logs are JSON lines on the container output, shipped by
Alloy to Loki. All three binaries (`cmd/api`, `cmd/mcp`, `cmd/a2a`) bring telemetry up before the router is built and flush it last.
Export never blocks: an unreachable collector costs telemetry, never availability (`internal/adapters/outbound/telemetry/telemetry.go`).

## Exporter settings

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint, `host:port` or URL (a bare `host:port` becomes `http://host:port`). |
| `OTEL_SERVICE_NAME` | `linehaul-transportation`, `linehaul-transportation-mcp`, `linehaul-transportation-a2a` | `service.name` resource attribute and the server name of the HTTP instruments. |
| `SERVICE_VERSION` | `dev` | `service.version`. |
| `ENVIRONMENT` | `local` | `deployment.environment.name`. |

Metrics are pushed every 15 s (`metricInterval`); traces go through the batch span processor. The chart renders these variables from `otel.enabled`,
`otel.endpoint`, `otel.serviceName`, `environment` and `image.tag`; with `otel.enabled=false` no `OTEL_*` variable is rendered and the binaries
fall back to `localhost:4317` (see [Configuration](/docs/operations/configuration)).

## Metrics

Source: `internal/adapters/outbound/telemetry/load_metrics.go`, `internal/adapters/inbound/http/server.go`, `cmd/mcp/router.go`, `cmd/a2a/router.go`.

| Instrument | Type | Unit | Attributes | Emitted by | Meaning |
| --- | --- | --- | --- | --- | --- |
| `linehaul.loads.dispatched` | counter (int64) | `{load}` | `outcome` (`dispatched` or `rejected`); `reason` (a problem slug such as `illegal-transition` or `not-found`) only when `outcome=rejected` | `cmd/api` (meter scope `github.com/claudioed/linehaul-transportation`) | Every `DispatchLoad` attempt. A falling `dispatched` share with rising `illegal-transition` means operators try to dispatch loads that were never closed; rising `internal-error` means the service is failing. Prometheus name: `linehaul_loads_dispatched_total`. |
| `http.server.request.duration` | histogram (float64) | `s` | `http.method`, `http.scheme`, `http.route` (the chi pattern, when matched) | all three binaries (`otelchimetric.NewServerRequestDuration`) | Duration of every HTTP request, including `/healthz`. Prometheus: `http_server_request_duration_seconds_*`. |
| Go runtime metrics | gauges and counters | various | none | all three binaries (`runtime.Start`) | Goroutines, heap and GC from the OpenTelemetry Go runtime instrumentation. |

There is no metric for the outbox backlog, consumer lag or the horizon roll: those are read from Postgres (`outbox_events`) and the logs (see
[Runbook](/docs/operations/runbook)).

## Traces

- **Server spans**: `otelchi.Middleware` opens one span per request on every binary, named from the chi route pattern (for example
  `POST /loads/{loadId}/closures`).
- **Outbox propagation**: when a use case enqueues an event, the relay-bound row stores `traceparent` (and `tracestate`/`baggage` when present) in its
  headers (`internal/adapters/outbound/postgres/outbox_repository.go`), so the trace continues across Kafka.
- **Consumer spans**: each consumed message runs in a `CONSUMER` span named `<topic> process` with `messaging.system=kafka`,
  `messaging.destination.name=<topic>` and `messaging.operation.name=process`, parented to the producer's `traceparent` header; a message without
  one starts a new trace. A handler error is recorded on the span (`internal/adapters/inbound/kafka/tracing.go`).
- There are no spans around Postgres calls or the relay publish.

## Logs

JSON via `log/slog` (`cmd/api` on stderr, `cmd/mcp` and `cmd/a2a` on stdout), level from `LOG_LEVEL`. Every record written with a request or message context carries
`trace_id` and `span_id` (`telemetry.WithTraceContext`). The HTTP middleware (`internal/adapters/inbound/requestlog`) writes one `http request` line per
request with these fields, skipping `/healthz` and `/readyz`:

| Field | Meaning |
| --- | --- |
| `method`, `route` | HTTP method and the matched chi pattern (never the raw path, which holds ids); `route` is a placeholder for unmatched requests |
| `status`, `duration_ms`, `bytes` | status code, duration in milliseconds, response bytes |
| `request_id` | chi request id |
| `trace_id`, `span_id` | from the server span |

Level by status: 5xx `ERROR`, 4xx `WARN`, otherwise `INFO`.

Messages worth alerting or searching on (Loki `app="linehaul-transportation"`):

| Message | Level | Meaning |
| --- | --- | --- |
| `horizon roll` (`scheduled`, `failed`) | INFO | One pass of departure generation |
| `horizon roll failed` | ERROR | The pass returned an error |
| `outbox relay pass failed` | ERROR | The relay could not read or publish a batch |
| `outbox relay: row reached the attempt cap and is excluded from future claims; ...` | ERROR | A poison outbox row, logged once with `outbox_id`, `topic`, `event_type`, `key`, `attempts` |
| `<consumer> handling failed; retrying the same message` | ERROR | Transient handler failure, with `attempt`, `max_attempts`, `retry_in`, `partition`, `offset` |
| `<consumer> handling failed after the maximum attempts; dead-lettering the message` / `message can never be applied; dead-lettering it without retry` | ERROR | The message goes to `<topic>.dlq` |
| `skipping an event that can never be applied`, `dead-lettering an event that can never be applied`, `skipping a message that is not a valid CloudEvent` | WARN | Deterministic rejections of a consumed event |
| `<consumer> broker connection stalled or lost; fetching again after the backoff` | WARN | Kafka unreachable while consuming |
| `worker stopped on its own` | ERROR | A worker returned; the process exits non-zero and Kubernetes restarts it |
| `telemetry flush failed on shutdown` | WARN | The collector was unreachable during the final flush |
| `readiness: dependency is not ready` (`dependency`, `error`) | WARN | `/readyz` found Postgres or Kafka down |

## Dashboards and alerts

Only what exists in `warehouse-infra` on `develop`:

- **Grafana dashboard** `warehouse-linehaul-transportation` ("Warehouse - Linehaul Transportation", `terraform/dashboards/contexts/linehaul-transportation.json`):
  Kong request rate, 5xx ratio and p95 latency for the context's routes; service HTTP RED panels from `http_server_request_duration_seconds_*`
  (`service_name=~"linehaul-transportation.*"`); a business-metrics row with `sum by (outcome) (rate(linehaul_loads_dispatched_total[5m]))`;
  Go runtime panels (goroutines, memory); Loki panels (live stream, volume by level, errors and warnings).
- **Alert rules** (`terraform/alerting/warehouse-alert-rules.json`): group `warehouse-linehaul-transportation` has `WarehouseKong5xxRatioHigh` (5 m) and
  `WarehousePodRestarting`; group `warehouse-linehaul-transportation-slo` has the multiwindow burn-rate alerts `WarehouseApiAvailabilityBurnFast`,
  `WarehouseApiLatencyBurnFast` (2 m) and `WarehouseApiAvailabilityBurnSlow`, `WarehouseApiLatencyBurnSlow` (15 m) on the Kong routes.
- **Not present**: no alert on `linehaul_loads_dispatched_total`, on outbox backlog, on consumer lag or on dead-letter topics.
