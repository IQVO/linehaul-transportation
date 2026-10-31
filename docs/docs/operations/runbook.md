---
id: runbook
title: Runbook
sidebar_label: Runbook
description: "Operator's view of Linehaul Transportation: deployment, probes, migrations, Kafka topics and consumer groups, outbox relay, dead letters, horizon roll and routine procedures."
---

# Linehaul Transportation runbook

Linehaul Transportation is the retail network's middle-mile bounded context (a domain-driven design study; see the disclaimer in the
`README.md`): it turns lane schedules into a rolling 14-day horizon of scheduled departures, including a `PICKUP` departure at every cutoff of an
active vendor node, tracks each departure's booked and remaining package capacity, books locked fulfillment assignments onto pickup departures, and
builds loads of packages that are closed, dispatched and received. It calls no other service: it keeps local copies of the node-topology,
order-sourcing and shipping-and-tracking facts ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models)) and publishes every change
as a CloudEvents 1.0 event through a transactional outbox ([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)). Settings, probes and
shutdown semantics are on [Runtime](/docs/overview/runtime) and [Configuration](/docs/operations/configuration); this page is the operator's view.

## Runtime shape

Three binaries, three independent deployables from one image (`/app/api`, `/app/mcp`, `/app/a2a`):

| Binary | Chart component | Port | Role |
| --- | --- | --- | --- |
| `cmd/api` | `api` Deployment (always on) | `:8080` | REST, the three Kafka consumers, the outbox relay and the horizon roll. The only binary that writes. |
| `cmd/mcp` | `mcp.enabled` (default off) | `:8090` | Read-only MCP server (Streamable HTTP on `/mcp` and `/`). No Kafka, no relay, no writes. |
| `cmd/a2a` | `a2a.enabled` (default off) | `:8093` | Read-only A2A agent (JSON-RPC on `/` and `/a2a`, Agent Card on `/.well-known/agent-card.json`). No Kafka, no relay, no writes. |

The chart also has an optional `frontend` component (nginx serving the `linehaul_mfe` console remote, no env, default off).

`cmd/api` (`cmd/api/main.go`, `cmd/api/serve.go`) is the composition root of everything the service does:

- **REST on `:8080`** (`internal/adapters/inbound/http/server.go`): the operations of `apis/openapi.yaml` on a chi router: `POST /lane-schedules`,
  `POST /lane-schedules/{laneId}:retire`, `POST /departures/{departureId}:cancel`, `POST /departures/{departureId}:change-capacity`, `POST /loads`,
  `POST /loads/{loadId}/packages`, `POST /loads/{loadId}:close`, `:dispatch`, `:receive`, `GET /departures`, `GET /departures/{departureId}`. Every
  POST sits behind the Idempotency-Key middleware (24 h replay window, `idempotency_keys` table). No auth, by fleet decision.
- **Transactional-outbox relay** (`internal/adapters/outbound/outbox/relay.go`): drains unpublished `outbox_events` rows to Kafka every
  `OUTBOX_RELAY_INTERVAL` (batch 100, `FOR UPDATE SKIP LOCKED` claims).
- **Three Kafka consumers** (`internal/adapters/inbound/kafka/consumer.go`), each under its own mandatory consumer group.
- **Horizon roll (the generation sweep)**: `GenerateDepartures` (`internal/application/usecases/generate.go`) runs once at boot and then every
  `GENERATE_INTERVAL`; it extends the 14-day departure horizon of every ACTIVE lane schedule and pickup-eligible vendor node. A rerun schedules
  nothing new (ADR 0002 A1/A6), which is why it is safe that it runs in every replica. Each run logs `horizon roll` at INFO with `scheduled` and
  `failed` counts. There is no separate sweeper job or CronJob.

The relay, the horizon roll and the consumers are workers: if any of them returns on its own, the process exits non-zero so Kubernetes restarts it
(`cmd/api/serve.go`: `startWorkers` / `serveUntilSignal`).

## Dependencies

- **Postgres**: every write is a transaction. `DATABASE_URL` is the pooled DSN (PgBouncer transaction pooling in the fleet); `MIGRATIONS_DATABASE_URL`
  is a direct, session-mode DSN used only for the boot migrations, because golang-migrate's advisory lock cannot survive transaction pooling
  (`cmd/api/main.go`, `migrate`; retried up to 30 times, 1 s apart, while Postgres starts). Both come from the Secret `linehaul-transportation-db`
  via `secretKeyRef`; the migrations key is `optional`: absent, the binary falls back to `DATABASE_URL`. `cmd/mcp` and `cmd/a2a` also run the
  (idempotent) migrations on start and read the same database.
- **Kafka**: one broker platform-wide. See the topic table below. Topics are auto-created by the first writer (the relay writer sets
  `AllowAutoTopicCreation`; `internal/adapters/outbound/kafka/health.go`): no topic exists until the first publish, and that is normal, not an outage.
- **No sibling REST calls**: the service depends on nothing else (ADR 0003).

## Migrations

Embedded in the image (`internal/adapters/outbound/postgres/migrations/`) and applied by golang-migrate on every boot of every binary over the direct
DSN; there is no separate migration job. Concurrent boots are serialised by golang-migrate's advisory lock. They create `departures`,
`lane_schedules`, `loads`, `load_packages`, `expected_volumes`, `node_views`, `lane_views`, `outbox_events`, `processed_events` and
`idempotency_keys` (see [Entity-relationship](/docs/ddd/entity-relationship)). Never point `MIGRATIONS_DATABASE_URL` at the pooled listener.

## Kafka topics and consumer groups

| Direction | Topic | Event types | Consumer group (chart value) |
| --- | --- | --- | --- |
| consumed | `retailnetwork.node-topology.events` | `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `LaneOpened`, `LaneClosed` | `NODE_TOPOLOGY_CONSUMER_GROUP` (`config.nodeTopologyConsumerGroup`), deployed as `linehaul-transportation-node-topology` |
| consumed | `retailnetwork.order-sourcing.events` | `FulfillmentReleasedToNode`, `FulfillmentReassigned`, `FulfillmentLocked`, `FulfillmentCancelled` | `ORDER_SOURCING_CONSUMER_GROUP`, deployed as `linehaul-transportation-order-sourcing` |
| consumed | `retailnetwork.shipping-and-tracking.events` | `LabelIssued`, `LabelVoided` | `SHIPPING_AND_TRACKING_CONSUMER_GROUP`, deployed as `linehaul-transportation-shipping-and-tracking` |
| published | `retailnetwork.linehaul-transportation.events` | `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`, `BookingMoved`, `BookingFailed`, `LoadDeparted`, `LoadArrived` | - |
| published | `retailnetwork.linehaul-transportation.analytics` | `DepartureDeparted`, `DepartureArrived`, `LoadClosed` (analytics only) | - |
| dead letters | `<consumed topic>.dlq` (for example `retailnetwork.order-sourcing.events.dlq`) | the original message plus `x-dlq-*` headers | - |

Consumer group ids must be stable and service-prefixed, never derived from the pod name: a rescheduled pod then resumes from its committed offsets.
The Kafka message key is the aggregate id and the CloudEvents `subject` equals it.

## Health

- `GET /healthz` is liveness only: `200 {"status":"ok"}` while the process serves, even mid-drain or with both dependencies down
  (`internal/adapters/inbound/http/server.go`). `cmd/mcp` and `cmd/a2a` serve only `/healthz` (they have no readiness endpoint).
- `GET /readyz` (`cmd/api` only, `internal/adapters/inbound/http/readiness.go`) is `200 {"status":"ready"}` only while the drain gate is open AND
  Postgres answers a ping AND a Kafka broker answers a topic-less metadata request (verdict cached 3 s, one round bounded 2 s; the check names no
  topics, so probing never creates one). It answers 503 `{"status":"not_ready"}` once shutdown began; otherwise 503 `application/problem+json`
  `dependency-unavailable` naming the failing dependencies.
- **Writes while a dependency is down**: with Kafka down, a REST write is still accepted: the aggregate rows and the already-encoded events commit
  together in Postgres and wait in `outbox_events` until the relay can publish. With Postgres down, no write is accepted and `/readyz` fails.
  In-cluster the 503 also pulls the pod from the Service endpoints; the handler itself only needs Postgres.

## Configuration in the chart

`cmd/api/config.go` reads and validates the settings (a missing or invalid value is a boot error that names the variable, raised before anything is
opened or bound). The chart renders each from `charts/linehaul-transportation/values.yaml`:

| Env var | Chart value |
| --- | --- |
| `HTTP_ADDR`, `LOG_LEVEL` | `config.httpAddr`, `config.logLevel` |
| `DATABASE_URL` | `database.url`, or `database.existingSecret` + `database.existingSecretKey` (secretKeyRef) |
| `MIGRATIONS_DATABASE_URL` | `database.migrationsExistingSecretKey` (optional secretKeyRef) |
| `KAFKA_BROKERS` | `kafka.brokers` |
| `NODE_TOPOLOGY_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | `config.nodeTopologyConsumerGroup`, `config.orderSourcingConsumerGroup`, `config.shippingAndTrackingConsumerGroup` |
| `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `GENERATE_INTERVAL`, `PACKAGES_PER_ORDER`, `SHUTDOWN_DRAIN_DELAY` | `config.outboxRelayInterval`, `config.outboxMaxAttempts`, `config.generateInterval`, `config.packagesPerOrder`, `config.shutdownDrainDelay` |
| `OTEL_*`, `SERVICE_VERSION`, `ENVIRONMENT` | `otel.enabled`, `otel.endpoint`, `otel.serviceName`, `image.tag`, `environment` |
| `MCP_ADDR` (`cmd/mcp`) | `mcp.httpAddr` |
| `A2A_ADDR`, `A2A_AGENT_URL` (`cmd/a2a`) | `a2a.httpAddr`, `a2a.agentUrl` (empty: the in-cluster Service DNS, `http://<release>-a2a.<ns>.svc.cluster.local:8093/`) |

The chart refuses to render with an empty consumer group, broker list or database URL; durations accept a Go duration (`"500ms"`) or plain seconds
(`"5"`); `terminationGracePeriodSeconds` (40) must exceed drain delay + 10 s HTTP drain + 10 s workers drain.

## Alerts and dashboards

See [Observability](/docs/operations/observability) for the full list. In short:

- **Grafana dashboard** uid `warehouse-linehaul-transportation` (warehouse-infra): Kong rate/errors/latency, service HTTP RED, the
  `linehaul_loads_dispatched_total` business row, Go runtime and Loki panels.
- **Alerts**: `WarehouseKong5xxRatioHigh`, `WarehousePodRestarting` and the four SLO burn-rate alerts (availability and latency, fast and slow).
- **Honest gap**: the only business instrument is `linehaul.loads.dispatched`. Outbox backlog, consumer lag and dead letters are not metered and not
  alerted: use the SQL and the log patterns below.

## Failure modes

### Pod crash-looping

- **Symptom**: `CrashLoopBackOff` on the `api` Deployment.
- **Diagnosis**: `kubectl -n warehouse-systems logs deploy/linehaul-transportation --previous`. The common causes are boot errors (every missing or
  invalid env var is named in the first log line, `cmd/api/config.go`), migrations failing the ~30 s retry window (Postgres not up, wrong
  `MIGRATIONS_DATABASE_URL`) or a consumer that cannot dial Kafka at start (`worker stopped on its own`). The startup probe on `/healthz` allows 2 s x 45
  for the migration window (`values.yaml`).
- **Action**: fix the chart value or the Secret, then restart (below).

### Postgres unreachable

- **Symptom**: `/readyz` 503 `dependency-unavailable` naming `postgres`; REST writes fail; ERROR `outbox relay pass failed`; ERROR
  `horizon roll failed`; consumers retry handling.
- **Action**: nothing to drain service-side: the relay, the horizon roll and the consumers retry on their own intervals once Postgres answers; the
  outbox lives in Postgres, so nothing accepted could be lost.

### Kafka unreachable

- **Behaviour**: REST writes are still accepted and queued; rows accumulate in `outbox_events` with `published_at IS NULL` and publish in id order once
  the broker returns; `/readyz` 503 names `kafka`. The relay logs `outbox relay pass failed` each pass and retries every `OUTBOX_RELAY_INTERVAL`.
  Consumers tolerate a stalled or reset connection (WARN `broker connection stalled or lost`, capped backoff, up to 10 consecutive failures) but
  a refused connection is fatal for that consumer: the worker returns, the process logs `worker stopped on its own` and exits, and Kubernetes
  restarts it until the broker is back.
- **Action**: restore the broker and watch the outbox tail drain (SQL below). Ordinary outage rows need no re-arm; only rows that hit the attempt cap do.

### Outbox relay stuck / a row parked at max attempts

Table `outbox_events` (columns that matter: `published_at TIMESTAMPTZ`, `attempts INTEGER`, `last_error TEXT`).

- **Symptom**: a missing event downstream while later events of the same aggregate arrived. A row whose send failed `OUTBOX_MAX_ATTEMPTS` (20) times
  is excluded from future claims and logged ONCE at ERROR with `outbox_id`, `topic`, `event_type`, `key`, `attempts` and the last error. Later
  rows, same key included, overtake it: a gap, not a stall.
- **Diagnosis**: `SELECT id, topic, event_type, key, attempts, last_error, created_at FROM outbox_events WHERE published_at IS NULL ORDER BY id;`
- **Action**: fix the cause (usually a broker blip or a topic still auto-creating), then re-arm: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`.
  A relay that logs nothing at all is a dead worker, which exits the pod; the pod-restart alert catches it.

### Dead letters, consumer retries and lag

- ERROR `<consumer> handling failed; retrying the same message` (with `attempt`, `max_attempts` 5, `retry_in`, `partition`, `offset`) is a transient
  handler failure retried on the same message with capped backoff 200 ms to 5 s; after 5 attempts the message is dead-lettered to
  `<consumed topic>.dlq` with `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at` headers and the
  offset is committed. A message that can never be applied (undecodable data, a value the domain refuses) is dead-lettered at once (ERROR
  `message can never be applied; dead-lettering it without retry`). The dead-letter publish itself is retried forever, so a message is never
  dropped silently and the partition stays blocked while it cannot be dead-lettered.
- **Replay**: inspect the `.dlq` topic, fix the cause, then produce the message value back to the source topic. It is idempotent: `processed_events`
  claims on the CloudEvents id, in the same transaction as the effect.
- WARN `skipping an event that can never be applied` and `skipping a message that is not a valid CloudEvent` are deterministic and already committed past.
- A consumed topic whose producer has not written yet is an empty topic (topics are auto-created by the first writer): zero lag on it is a normal
  early state. Per-consumer progress is observable only as the group's committed offsets plus these log lines.
- A lock whose pickup departure is unknown, closed, cancelled or full is not a failure: the booking moves to the next pickup from the same origin
  (`BookingMoved`) or fails (`BookingFailed`); the node never changes after lock (ADR 0002 A14).

### Generation sweep not producing

- **Symptom**: departures stop appearing although lane schedules or vendor nodes exist.
- **Diagnosis**: every run logs `horizon roll` with `scheduled`/`failed`. `failed > 0` names the broken schedule or node in the following log lines.
  `scheduled = 0` with `failed = 0` means the horizon is already full (a rerun schedules nothing new by design), no ACTIVE lane schedule
  (`lane_schedules.state`), no OPEN lane or known node (`lane_views`, `node_views`), or no pickup-eligible node; confirm `GENERATE_INTERVAL` was not stretched.
- **Action**: fix the schedule or node named in the logs; the next roll picks it up. A restart forces an immediate pass (the roll also runs at boot).

## Routine procedures

- **Restart** (picks up new env/Secret values and runs the boot horizon roll): `kubectl -n warehouse-systems rollout restart deploy/linehaul-transportation`
  (and `-mcp`, `-a2a` when enabled).
- **Logs**: Grafana Loki, `app=linehaul-transportation` (JSON slog; `LOG_LEVEL=debug` raises verbosity). Patterns: `outbox relay pass failed`,
  `reached the attempt cap`, `handling failed; retrying`, `horizon roll`, `worker stopped on its own`.
- **REST through the fleet gateway**: Kong route `/api/linehaul-transportation` (prefix stripped), for example `GET /api/linehaul-transportation/healthz`
  and `GET /api/linehaul-transportation/departures`. Optional routes: `/api/mcp/linehaul-transportation` (rewritten to `/mcp`) when
  `mcp.route.enabled`, `/api/a2a/linehaul-transportation` (rewritten to `/`) when `a2a.route.enabled`.
- **Outbox backlog**: `SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL;` (a small, recent tail is normal).
- **Idempotency keys** older than 24 h are purged on the next keyed write; nothing to schedule.
- **Rolling the chart**: `helm lint charts/linehaul-transportation -f charts/linehaul-transportation/ci/default-values.yaml`, then
  `bash charts/linehaul-transportation/tests/render_test.sh`. Packaging and first-release checks: [Before the first release](/docs/operations/before-first-release).
