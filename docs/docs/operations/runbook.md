# Linehaul Transportation runbook

Linehaul Transportation is the retail network's middle-mile bounded context (a domain-driven design study; see the
disclaimer in `README.md`): it turns lane schedules into a rolling 14-day horizon of scheduled departures — including
a `PICKUP` departure at every cutoff of an active vendor node — tracks each departure's booked and remaining package
capacity, books locked fulfillment assignments onto pickup departures, and builds loads of packages that are closed,
dispatched and received. It calls no other service: it keeps local copies of the node-topology, order-sourcing and
shipping-and-tracking facts (`docs/adr/0003-consumed-contracts-and-local-read-models.md`) and publishes every change
as a CloudEvents 1.0 event through a transactional outbox
(`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`). Developer-side configuration, probes and shutdown
semantics live in `docs/runtime.md`; this page is the operator's view.

## Runtime shape

One binary, one Deployment. `cmd/api` (`cmd/api/main.go`, `cmd/api/serve.go`) is the composition root of everything
the service does:

- **REST on `:8080`** (`internal/adapters/inbound/http/server.go`): the operations of `apis/openapi.yaml` on a chi
  router — `POST /lane-schedules`, `POST /lane-schedules/{laneId}:retire`, `POST /departures/{departureId}:cancel`,
  `POST /departures/{departureId}:change-capacity`, `POST /loads`, `POST /loads/{loadId}/packages`, `POST
  /loads/{loadId}:close|:dispatch|:receive`, `GET /departures`, `GET /departures/{departureId}`. Every POST sits
  behind the Idempotency-Key middleware (24 h replay window, `idempotency_keys` table). No auth, by fleet decision.
- **Transactional-outbox relay**: a background loop (`internal/adapters/outbound/outbox/relay.go`) that drains
  unpublished `outbox_events` rows to Kafka every `OUTBOX_RELAY_INTERVAL` (batch 100, `FOR UPDATE SKIP LOCKED`
  claims).
- **Three Kafka consumers** (`internal/adapters/inbound/kafka/consumer.go`): the local-copy consumers of
  `retailnetwork.node-topology.events`, `retailnetwork.order-sourcing.events` and
  `retailnetwork.shipping-and-tracking.events`, each under its own mandatory consumer group.
- **Horizon roll (the generation sweep)**: `GenerateDepartures` (`internal/application/usecases/generate.go`) runs
  once at boot and then every `GENERATE_INTERVAL`; it extends the 14-day departure horizon of every ACTIVE lane
  schedule and pickup-eligible vendor node. A rerun schedules nothing new (ADR 0002 A1/A6), which is why it is safe
  that it runs in every replica. Each run logs `horizon roll` at INFO with `scheduled` and `failed` counts.

The relay, the horizon roll and the consumers are workers: if any of them returns on its own, the process exits
non-zero so Kubernetes restarts it (`cmd/api/serve.go`: `startWorkers` / `serveUntilSignal`).

## Dependencies

- **Postgres** — every write is a transaction. `DATABASE_URL` is the pooled DSN (PgBouncer transaction pooling in the
  fleet); `MIGRATIONS_DATABASE_URL` is a direct, session-mode DSN used only for the boot migrations, because
  golang-migrate's advisory lock cannot survive transaction pooling (`cmd/api/main.go`, `migrate`; retried up to 30 ×
  1 s while Postgres starts). Both come from the Secret `linehaul-transportation-db` via `secretKeyRef`; the
  migrations key is `optional` — absent, the binary falls back to `DATABASE_URL`.
- **Kafka** — one cluster for everything outbound and inbound. The relay publishes integration events to
  `retailnetwork.linehaul-transportation.events` and the analytics-only events (`DepartureDeparted`,
  `DepartureArrived`, `LoadClosed`) to `retailnetwork.linehaul-transportation.analytics`
  (`internal/adapters/kafka/wire/wire.go`). Topics are auto-created by the first writer (the relay writer sets
  `AllowAutoTopicCreation`; `internal/adapters/outbound/kafka/health.go`): no topic exists until the first publish,
  and that is normal, not an outage.
- **No sibling REST calls** — the service depends on nothing else (ADR 0003).

## Health

- `GET /healthz` — liveness only: `200 {"status":"ok"}` while the process serves, even mid-drain or with both
  dependencies down (`internal/adapters/inbound/http/server.go`).
- `GET /readyz` — readiness (`internal/adapters/inbound/http/readiness.go`): `200 {"status":"ready"}` only while the
  drain gate is open AND Postgres answers a ping AND a Kafka broker answers a topic-less metadata request (verdict
  cached 3 s, one round bounded 2 s; the check deliberately names no topics, so probing never creates one). 503
  `{"status":"not_ready"}` once shutdown began; otherwise 503 `application/problem+json` `dependency-unavailable`
  naming the failing dependencies.
- **Writes while a dependency is down, per code**: with Kafka down, a REST write is still accepted — the aggregate
  rows and the already-encoded events commit together in Postgres and wait in `outbox_events` until the relay can
  publish. With Postgres down, no write is accepted (every POST is a transaction) and `/readyz` fails. In-cluster the
  503 also pulls the pod from the Service endpoints; the handler itself only needs Postgres.

## Configuration

`cmd/api/config.go` reads and validates these (a missing or invalid value is a boot error that names the variable,
raised before anything is opened or bound):

| Env var | Default | Chart value (`charts/linehaul-transportation/values.yaml`) |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | `config.httpAddr` |
| `LOG_LEVEL` | `info` | `config.logLevel` |
| `DATABASE_URL` | required | `database.url`, or `database.existingSecret` + `database.existingSecretKey` (secretKeyRef) |
| `MIGRATIONS_DATABASE_URL` | = `DATABASE_URL` | `database.migrationsExistingSecretKey` (optional secretKeyRef) |
| `KAFKA_BROKERS` | required | `kafka.brokers` |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | required | `config.nodeTopologyConsumerGroup` |
| `ORDER_SOURCING_CONSUMER_GROUP` | required | `config.orderSourcingConsumerGroup` |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | required | `config.shippingAndTrackingConsumerGroup` |
| `OUTBOX_RELAY_INTERVAL` | `1s` | `config.outboxRelayInterval` |
| `OUTBOX_MAX_ATTEMPTS` | `20` (`0` = unlimited) | `config.outboxMaxAttempts` |
| `GENERATE_INTERVAL` | `1h` | `config.generateInterval` |
| `PACKAGES_PER_ORDER` | `1` (ADR 0002 A9) | `config.packagesPerOrder` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` (`0` disables) | `config.shutdownDrainDelay` |

Deployed consumer groups on the cluster: `linehaul-transportation-node-topology`,
`linehaul-transportation-order-sourcing`, `linehaul-transportation-shipping-and-tracking`. The chart refuses to render
with an empty group, broker list or database URL; durations accept a Go duration (`"500ms"`) or plain seconds (`"5"`);
`terminationGracePeriodSeconds` (40) must exceed drain delay + 10 s HTTP drain + 10 s workers drain.

## Alerts and dashboards

- **Grafana dashboard** uid `warehouse-linehaul-transportation` (warehouse-infra). Pod-level panels (restarts, CPU,
  memory) work today.
- **Alerts**: pod-restart alerts exist in warehouse-infra's alert rules. There is no service-specific alert yet.
- **Honest gap**: the service instruments no business metrics — `cmd/api` registers no Prometheus collector for domain
  counters or histograms — so business-metric panels stay empty until the service instruments itself; until then the
  only signals are pod health and logs (Loki, below).

## Failure modes

### Pod crash-looping

- **Symptom**: `CrashLoopBackOff` on the `api` Deployment.
- **Diagnosis**: `kubectl -n warehouse-systems logs deploy/linehaul-transportation --previous`. The common causes are
  boot errors — every missing or invalid env var is named in the first log line (`cmd/api/config.go`) — or migrations
  failing the ~30 s retry window (Postgres not up, wrong `MIGRATIONS_DATABASE_URL`); the startup probe on `/healthz`
  allows 2 s × 45 for exactly that window (`values.yaml`).
- **Action**: fix the chart value or the Secret, then restart (below).

### Postgres unreachable

- **Symptom**: `/readyz` 503 `dependency-unavailable` naming `postgres`; REST writes fail; ERROR `outbox relay pass
  failed`; ERROR `horizon roll failed`; consumers retry handling.
- **Diagnosis**: the readiness log `readiness: dependency is not ready` names the dependency; check PgBouncer and the
  Postgres behind it.
- **Action**: nothing to drain service-side — the relay, the sweep and the consumers retry on their own intervals once
  Postgres answers; the outbox lives in Postgres, so nothing accepted could be lost.

### Kafka unreachable

- **Verified behaviour**: REST writes are still accepted and queued — rows accumulate in `outbox_events` with
  `published_at IS NULL` and publish in id order once the broker returns; `/readyz` 503 names `kafka`. The relay logs
  `outbox relay pass failed` each pass and retries every `OUTBOX_RELAY_INTERVAL`; consumers retry their offsets;
  neither exits (only a worker *returning* kills the pod).
- **Action**: restore the broker and watch the outbox tail drain (SQL below). Ordinary outage rows need no re-arm;
  only rows that hit the attempt cap do.

### Outbox relay stuck / a row parked at max attempts

The table is `outbox_events` (migration `internal/adapters/outbound/postgres/migrations/0001_linehaul_schema.up.sql`;
the columns that matter are `published_at TIMESTAMPTZ`, `attempts INTEGER`, `last_error TEXT`).

- **Symptom**: a missing event downstream while later events of the same aggregate arrived. A row whose send failed
  `OUTBOX_MAX_ATTEMPTS` (20) times is excluded from future claims and logged ONCE at ERROR — `outbox relay: row
  reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` —
  with `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `last error`. Later rows, same key included, overtake
  it: a gap, not a stall.
- **Diagnosis**: `SELECT id, topic, event_type, key, attempts, last_error, created_at FROM outbox_events WHERE
  published_at IS NULL ORDER BY id;` and read `last_error`.
- **Action**: fix the cause (usually a broker blip or a topic still auto-creating), then re-arm exactly as the log
  says: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. A relay that logs nothing at all is a dead worker,
  which exits the pod — the pod-restart alert catches it.

### Consumer lag or retry logs

- ERROR `<consumer> handling failed; retrying the same message` (with `attempt`, `max_attempts` 5, `retry_in`,
  `partition`, `offset`) is a transient handler failure retried on the same message with capped backoff 200 ms → 5 s;
  after 5 attempts the message is dead-lettered to `<consumed topic>.dlq` with `x-dlq-*` headers and the offset is
  committed (`internal/adapters/inbound/kafka/kafka.go`, `deadletter.go`).
- Inspect the `.dlq` topics; replay by producing the message value back to the source topic once the cause is fixed —
  idempotent, `processed_events` claims on the CloudEvents id. WARN `skipping an event that can never be applied` is
  deterministic and already committed past.
- A consumed topic whose producer has not written yet is simply an empty topic — topics are auto-created by the first
  writer, so an empty topic with zero lag is a normal early state, not an outage; per-consumer progress is observable
  only as the group's committed offsets plus these log lines.

### Generation sweep not producing

- **Symptom**: departures stop appearing although lane schedules or vendor nodes exist.
- **Diagnosis**: every run logs `horizon roll` with `scheduled`/`failed`. `failed > 0` names the broken schedule or
  node in the following `logFailure` lines. `scheduled = 0` with `failed = 0` means the horizon is already full (a
  rerun schedules nothing new by design), no ACTIVE lane schedule (`lane_schedules.state`), or no pickup-eligible node
  (`node_views` profile/generation); confirm `GENERATE_INTERVAL` / `config.generateInterval` was not stretched.
- **Action**: fix the schedule or node named in the logs; the next roll picks it up. A restart forces an immediate
  pass (the sweep also runs at boot).

## Operational actions

- **Restart** (picks up new env/Secret values and runs the boot horizon roll): `kubectl -n warehouse-systems rollout
  restart deploy/linehaul-transportation`.
- **Logs**: Grafana Loki, `app=linehaul-transportation` (JSON slog on stderr; `LOG_LEVEL=debug` raises verbosity).
  Patterns: `outbox relay pass failed`, `reached the attempt cap`, `handling failed; retrying`, `horizon roll`,
  `worker stopped on its own`.
- **REST through the fleet gateway**: Kong route `/api/linehaul-transportation` (prefix stripped) — e.g. `GET
  /api/linehaul-transportation/healthz`, `GET /api/linehaul-transportation/departures`.
- **Migrations**: embedded in the image and run on every boot over the direct DSN; there is no separate migration job.
  Never point `MIGRATIONS_DATABASE_URL` at the pooled listener.
