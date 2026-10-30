# Runtime: configuration, probes, shutdown and the outbox

`cmd/api` is the only binary. It runs on real PostgreSQL and real Kafka; there is no alternative runtime and no log sink.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN of the service's pool. A boot error names it when unset. |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy). |
| `KAFKA_BROKERS` | required | Comma-separated `host:port` list (relay, consumers, `/readyz`). A boot error names it when unset or blank. |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | required | Consumer group of the node-topology consumer. A boot error names it when unset or blank. |
| `ORDER_SOURCING_CONSUMER_GROUP` | required | Consumer group of the order-sourcing consumer. |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | required | Consumer group of the shipping-and-tracking consumer. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Poll interval of the relay (Go duration or seconds). |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited (ADR 0005). |
| `GENERATE_INTERVAL` | `1h` | How often the horizon roll (policy HorizonRoll) expands lane schedules and pickups; it also runs once at boot. |
| `PACKAGES_PER_ORDER` | `1` | Packages one order adds to a pickup departure's capacity (ADR 0002 A9). |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `OTEL_SERVICE_NAME` | `linehaul-transportation` | `service.name` of spans and metrics (ADR 0010); `cmd/mcp` and `cmd/a2a` default to `linehaul-transportation-mcp` and `linehaul-transportation-a2a`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC, `host:port` or URL; export never blocks. |
| `SERVICE_VERSION` / `ENVIRONMENT` | `dev` / `local` | `service.version` / `deployment.environment.name`. |

Configuration errors are raised before anything is opened or bound. Consumer group ids must be stable and service-prefixed
(for example `linehaul-transportation-node-topology`), never derived from a pod name.

## Boot and shutdown

1. Validate the environment. 2. Run the embedded migrations (retried for up to 30 s while Postgres starts). 3. Start the workers: the
outbox relay, the horizon roll and the three consumers (none dials at construction; a broker that is down only makes passes fail until
it is back). 4. Serve.

On SIGINT/SIGTERM: `/readyz` flips to 503 `not_ready`, wait `SHUTDOWN_DRAIN_DELAY`, drain HTTP (10 s), stop and await the workers
(10 s), close the pool and the Kafka clients. If a worker returns on its own the process exits non-zero so the orchestrator restarts it.
The grace period of a pod must exceed the sum of these bounds (5 + 10 + 10 s).

## Deployment (Helm chart)

`charts/linehaul-transportation` deploys `cmd/api` as a single `api` Deployment with a Service, a ConfigMap, a Secret (or your own via
`database.existingSecret`) and a ServiceAccount; the HPA (`autoscaling.api.enabled`), Ingress and Gateway API HTTPRoute
(`gatewayApi.enabled`) are optional and off by default. There is no MCP, frontend, projector or reports component.

- **Mandatory values** (the render fails and names them): `database.url` or `database.existingSecret`, `kafka.brokers`,
  `config.nodeTopologyConsumerGroup`, `config.orderSourcingConsumerGroup`, `config.shippingAndTrackingConsumerGroup`. A consumer group must be
  stable and service-prefixed; empty, blank and pod-derived ids are refused.
- **Env mapping**: every variable in the table above has a `config.*` / `database.*` / `kafka.brokers` value and is always rendered, with the
  binary's defaults (`OUTBOX_RELAY_INTERVAL` 1s, `OUTBOX_MAX_ATTEMPTS` 20, `GENERATE_INTERVAL` 1h, `PACKAGES_PER_ORDER` 1,
  `SHUTDOWN_DRAIN_DELAY` 5s). `extraEnv` may not set a `*_MODE` variable or any variable that has a dedicated value.
- **Observability env**: `otel.endpoint`, `otel.serviceName` (`<name>-mcp` / `<name>-a2a` for the mcp / a2a components) and `environment` render `OTEL_EXPORTER_OTLP_ENDPOINT`,
  `OTEL_SERVICE_NAME` and `ENVIRONMENT`; `SERVICE_VERSION` is `image.tag` (appVersion when empty). `otel.enabled=false` renders no `OTEL_*` env (ADR 0010).
- **Secrets**: `DATABASE_URL` is a `secretKeyRef` (key `database.existingSecretKey`); `MIGRATIONS_DATABASE_URL` is a `secretKeyRef` with
  `optional: true` (key `database.migrationsExistingSecretKey`), so the binary falls back to `DATABASE_URL` when the key is absent. No DSN is
  ever in the ConfigMap.
- **Hardening**: `runAsNonRoot` (uid 1000), `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, `drop: [ALL]`, seccomp
  `RuntimeDefault`, no service-account token. Probes: startup and liveness `/healthz`, readiness `/readyz`.
- **Grace period**: `terminationGracePeriodSeconds` (default 40) must exceed `SHUTDOWN_DRAIN_DELAY` + 10 s HTTP drain + 10 s workers drain. The
  chart reads the drain delay as plain seconds or a number with `s`, `ms` or `m`, and refuses other forms.
- **Tests**: `charts/linehaul-transportation/tests/render_test.sh` and `test_service_selectors.py` (CI job `chart-tests`; `helm-lint` runs
  `ct lint` on pull requests). The release job packages `linehaul-transportation-<version>.tgz`.

## Probes

- `GET /healthz` is liveness: 200 `{"status":"ok"}` while the process serves, even while draining or while a dependency is down.
- `GET /readyz` is 200 `{"status":"ready"}` only if the drain gate is open, Postgres answers a ping and a Kafka broker answers a
  metadata request. The request names no topics (an empty, non-nil list), so it never creates a topic and does not need the output
  topic to exist; a fresh connection is dialled on every check. While draining it answers 503 `{"status":"not_ready"}`; while a
  dependency is down, 503 `application/problem+json` of type `dependency-unavailable` naming it. Verdicts are cached 3 s.

## The outbox in operation

- Events are written in the same transaction as the aggregates and published later, so a write is accepted while Kafka is down;
  `outbox_events.published_at IS NULL` rows wait and are published, in id order, when the broker is back.
- Integration events go to `retailnetwork.linehaul-transportation.events`, analytics-only events (`DepartureDeparted`,
  `DepartureArrived`, `LoadClosed`) to `retailnetwork.linehaul-transportation.analytics`.
- Delivery is at-least-once with a stable CloudEvents `id`; consumers deduplicate.
- A row that failed `OUTBOX_MAX_ATTEMPTS` times is skipped (logged once at ERROR with id, topic, type and key) and stays in the table
  with `attempts` and `last_error`. Fix the cause, then re-arm it: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. While a row
  is skipped, later events of the same aggregate can overtake it.
- Idempotency keys older than 24 h are purged on the next keyed write.

## The consumers in operation

- Each consumed message is claimed in `processed_events(consumer, event_id)` in the same transaction as its effect; a redelivery stops
  at the claim.
- A message that can never be applied (undecodable `data`, a value the domain refuses) and a message that kept failing five times are
  published to `<consumed topic>.dlq` (for example `retailnetwork.order-sourcing.events.dlq`) with `x-dlq-source-topic`,
  `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at` headers, and the offset is committed. Inspect the
  dead-letter topics; replay a message by producing its value back to the source topic once the cause is fixed.
- A lock whose pickup departure is unknown, closed, cancelled or full is not a failure: the booking moves to the next pickup from the
  same origin (`BookingMoved`) or fails (`BookingFailed`); the node never changes after lock (ADR 0002 A14).

## Tests

`make check` (format, vet, build, lint, unit + handler + BDD tests), `make coverage` (90% over `internal/domain` and
`internal/application`), `make mutation` (gremlins per domain package), `make arch-test` (fitness), `make integration` (testcontainers
Postgres + Kafka: repositories, the relay, the middleware, the consumers and the real-wiring test `cmd/api/e2e_integration_test.go`;
needs Docker).
