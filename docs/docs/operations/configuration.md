---
id: configuration
title: Configuration reference
sidebar_label: Configuration
description: "Every environment variable read by cmd/api, cmd/mcp and cmd/a2a: default, required or not, and meaning."
---

# Configuration reference

Linehaul Transportation ships three binaries and each one reads its own environment. There are no configuration files and no flags. Every
variable is read once at boot; a bad value stops the process before anything is opened or bound. Empty and unset are the same thing
(`getenv`/`orDefault` fall back to the default for an empty string).

The Helm chart renders every variable below from `values.yaml`; see [Runbook](/docs/operations/runbook) and [Runtime](/docs/overview/runtime).

## `cmd/api` (REST, Kafka consumers, outbox relay, horizon roll)

Source: `cmd/api/config.go`, `cmd/api/telemetry.go`, `internal/adapters/outbound/telemetry/telemetry.go`.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service pool. Boot error `DATABASE_URL is required: ... no in-memory mode` when empty. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN used only for the embedded boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy such as PgBouncer). Retried for up to 30 s at boot. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated `host:port` list used by the outbox relay, the three consumers and the `/readyz` broker check. Blank entries are dropped; an empty list is a boot error. Locally `localhost:9092`. |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | none | **yes** | Consumer group of the consumer of the node-topology events. Trimmed; blank is a boot error. Use a stable, service-prefixed id, for example `linehaul-transportation-node-topology`. |
| `ORDER_SOURCING_CONSUMER_GROUP` | none | **yes** | Consumer group of the order-sourcing consumer (order locks and releases). |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | none | **yes** | Consumer group of the shipping-and-tracking consumer. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Poll interval of the outbox relay. A Go duration (`500ms`) or a number of seconds (`2`); negative values are refused. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures after which the relay skips an `outbox_events` row; `0` means unlimited. Must be an integer >= 0 (see ADR 0005). |
| `GENERATE_INTERVAL` | `1h` | no | How often the horizon roll (`GenerateDepartures`) expands lane schedules and pickup departures. It also runs once at boot. Duration or seconds. |
| `PACKAGES_PER_ORDER` | `1` | no | Packages one order adds to a pickup departure's capacity (ADR 0002 A9). Integer >= 1. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables the wait. Duration or seconds. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` or `error` (parsed by `slog.Level.UnmarshalText`; an unknown value falls back to `info`). JSON logs go to stderr. |
| `OTEL_SERVICE_NAME` | `linehaul-transportation` | no | `service.name` of spans and metrics and the router's server name. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint, `host:port` or URL. Export never blocks: an unreachable collector costs telemetry only. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute (read by `telemetry.Setup`). |

## `cmd/mcp` (read-only MCP server)

Source: `cmd/mcp/main.go`, `cmd/mcp/telemetry.go`. It never dials Kafka, so it has no `KAFKA_BROKERS` and no consumer groups.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address of the Streamable HTTP MCP endpoint. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database that `cmd/api` owns. Boot error when empty. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the idempotent boot migrations (the MCP binary can boot against a fresh database before `cmd/api`). Retried for about 30 s. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`) or `error`, case-insensitive; anything else is `info`. JSON logs go to stdout. |
| `OTEL_SERVICE_NAME` | `linehaul-transportation-mcp` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. |
| `SERVICE_VERSION` | `dev` | no | `service.version`. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`. |

## `cmd/a2a` (read-only A2A agent)

Source: `cmd/a2a/main.go`, `cmd/a2a/telemetry.go`. Like `cmd/mcp` it never dials Kafka.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address of the JSON-RPC server (`/`, `/a2a`) and the Agent Card. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL with a host that the Agent Card advertises for the JSON-RPC endpoint, for example `http://<release>-a2a.<namespace>.svc.cluster.local:8093/`. Missing or malformed, the process refuses to boot (reported together with a missing `DATABASE_URL`). |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`) or `error`, case-insensitive. JSON logs go to stdout. |
| `OTEL_SERVICE_NAME` | `linehaul-transportation-a2a` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. |
| `SERVICE_VERSION` | `dev` | no | `service.version`. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`. |

## Notes

- Defaults for the telemetry variables come from `telemetry.DefaultEndpoint` (`localhost:4317`) and `telemetry.DefaultServiceVersion` (`dev`).
  The service name default differs per binary (`defaultServiceName` in each `cmd/*/telemetry.go`).
- Test-only variables are not listed (the integration tests set `DATABASE_URL` themselves; `cmd/a2a/bootconfig_test.go` clears
  `KAFKA_BROKERS` and `EVENT_PUBLISHER` to prove that the A2A binary ignores them).
- There is no authentication setting: REST, MCP and A2A are unauthenticated; access control is the cluster network boundary.
- Duration variables accept a Go duration (`1s`, `250ms`, `1h`) or plain seconds; anything else is a boot error naming the variable.
