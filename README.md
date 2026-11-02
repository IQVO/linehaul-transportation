# linehaul-transportation

[![CI](https://github.com/IQVO/linehaul-transportation/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/IQVO/linehaul-transportation/actions/workflows/ci.yml?query=branch%3Adevelop) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/linehaul-transportation/blob/develop/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/linehaul-transportation/blob/develop/go.mod) [![release](https://img.shields.io/github/v/release/IQVO/linehaul-transportation?sort=semver)](https://github.com/IQVO/linehaul-transportation/releases) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/linehaul-transportation/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/linehaul-transportation/blob/develop/LICENSE)

Retail network, bounded context **Linehaul Transportation** (Supporting,
logistics tier). It runs the middle mile: turns lanes into scheduled
departures (including a pickup departure at every vendor node cutoff), keeps
each departure's remaining capacity, builds loads of packages onto
departures, and reports when loads depart and arrive. It does **not** own
nodes or lanes (`node-topology` does), fulfilment assignments
(`order-sourcing` does) or parcel labels (`shipping-and-tracking` does) — it
consumes their events into local read models and calls no other service. It
is an Open Host Service (REST plus a published CloudEvents language on
`retailnetwork.linehaul-transportation.events`). This repository is a
service of the retail network, not of the warehouse-systems fleet; the fleet
reaches the network only through the `vendor-connectivity` Vendor API.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

---

## Documentation

The documentation site of this repository: **<https://iqvo.github.io/linehaul-transportation/>** (source in [`docs/`](docs/), built with Docusaurus, deployed on every push to `main`).
Build it locally with `cd docs && npm ci && npm start`.

| Area | Pages |
|---|---|
| Overview | [Introduction](https://iqvo.github.io/linehaul-transportation/docs/overview/introduction), [Architecture](https://iqvo.github.io/linehaul-transportation/docs/overview/architecture), [Runtime](https://iqvo.github.io/linehaul-transportation/docs/overview/runtime), [Quickstart](https://iqvo.github.io/linehaul-transportation/docs/overview/quickstart) |
| Operations | [Configuration](https://iqvo.github.io/linehaul-transportation/docs/operations/configuration), [Runbook](https://iqvo.github.io/linehaul-transportation/docs/operations/runbook), [Observability](https://iqvo.github.io/linehaul-transportation/docs/operations/observability), [Troubleshooting](https://iqvo.github.io/linehaul-transportation/docs/operations/troubleshooting), [Before the first release](https://iqvo.github.io/linehaul-transportation/docs/operations/before-first-release) |
| Development | [Testing](https://iqvo.github.io/linehaul-transportation/docs/development/testing) |
| Domain-driven design | [Use cases](https://iqvo.github.io/linehaul-transportation/docs/ddd/use-cases), [Subdomain classification](https://iqvo.github.io/linehaul-transportation/docs/ddd/subdomain-classification), [Ubiquitous language](https://iqvo.github.io/linehaul-transportation/docs/ddd/ubiquitous-language), [DDD artifacts](https://iqvo.github.io/linehaul-transportation/docs/ddd/ddd-artifacts) |
| DDD pack | [Core domain chart](https://iqvo.github.io/linehaul-transportation/docs/ddd/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/linehaul-transportation/docs/ddd/bounded-context-canvas), [Context map](https://iqvo.github.io/linehaul-transportation/docs/ddd/context-map), [Aggregate design canvas](https://iqvo.github.io/linehaul-transportation/docs/ddd/aggregate-design-canvas), [Domain message flow](https://iqvo.github.io/linehaul-transportation/docs/ddd/domain-message-flow), [EventStorming](https://iqvo.github.io/linehaul-transportation/docs/ddd/eventstorming), [Class diagram](https://iqvo.github.io/linehaul-transportation/docs/ddd/class-diagram), [Entity-relationship](https://iqvo.github.io/linehaul-transportation/docs/ddd/entity-relationship), [Sequence diagrams](https://iqvo.github.io/linehaul-transportation/docs/ddd/sequence-diagrams), [Domain events](https://iqvo.github.io/linehaul-transportation/docs/ddd/domain-events) |
| API reference | [API overview](https://iqvo.github.io/linehaul-transportation/docs/api-reference), [Events](https://iqvo.github.io/linehaul-transportation/docs/api-reference/events), [REST reference](https://iqvo.github.io/linehaul-transportation/docs/api-reference/rest/linehaul-transportation-api) |
| Ecosystem | [Integration](https://iqvo.github.io/linehaul-transportation/docs/ecosystem/integration), [MCP tools](https://iqvo.github.io/linehaul-transportation/docs/mcp/tools) |
| Decisions | [ADR index](https://iqvo.github.io/linehaul-transportation/docs/adr) ([`docs/adr/`](docs/adr/index.md)) |

The network-level view of this context (its neighbours and the model it is built from) is on the
[retail-network pages of warehouse-docs](https://iqvo.github.io/warehouse-docs/network/contexts/linehaul-transportation).

---

## What it does

- **Lane schedules**: define the weekly slots of a lane (day, local cutoff, departs, arrives, capacity); a redefinition replaces the slots from the
  next horizon roll; retire a schedule.
- **Departures**: a 14-day horizon of `LINEHAUL` and `AIR` departures from the lane schedules, and a `PICKUP` departure at every cutoff of an ACTIVE
  vendor fulfillment center (toward the destination of its default outbound lane). Each keeps booked and remaining capacity; the planner can
  cancel one or change its capacity.
- **Bookings**: a locked assignment books one package on its pickup departure; if the departure cannot hold it the booking moves to the next pickup
  from the same origin (`BookingMoved`) or fails (`BookingFailed`). Line-haul and air legs are booked when the package is loaded.
- **Loads**: open a load on a departure, add packages, close, dispatch and receive it. Dispatch and receive record the departure and the arrival of the trip.
- Every change publishes a CloudEvents 1.0 event through a transactional outbox. The service consumes the node-topology, order-sourcing and
  shipping-and-tracking topics into local read models and calls no other service.

Aggregates, invariants and the thirty assumptions the model left open: [docs/adr/0002](docs/adr/0002-aggregates-and-invariants.md).
Consumed contracts and read models: [docs/adr/0003](docs/adr/0003-consumed-contracts-and-local-read-models.md).

## Architecture

Hexagonal / ports & adapters. Dependencies point inward only:

```
cmd/api/                     the REST service (composition root): HTTP, the three Kafka consumers, the horizon roll and the outbox relay in one process
cmd/mcp/                     read-only MCP server (Streamable HTTP, :8090)
cmd/a2a/                     read-only A2A agent (JSON-RPC over HTTP, Agent Card at /.well-known/agent-card.json)
internal/
  domain/                    pure Go: shared, departure, schedule, load, network, volume
  application/               ports, usecases, views, repository, outbox
  adapters/
    inbound/http/            chi handlers, DTOs, RFC 9457 problem+json errors, /healthz + /readyz
    inbound/kafka/           the node-topology, order-sourcing and shipping-and-tracking consumers
    inbound/mcp/  inbound/a2a/   the read-only agent surfaces
    outbound/postgres/       pgx repos, embedded golang-migrate SQL, transactional outbox
    outbound/kafka/          the integration- and analytics-topic writers
    outbound/outbox/         the relay
    outbound/clock/ ids/     the wall clock and id source
    outbound/memory/ repocontract/   test doubles, never imported by cmd/** (enforced)
    outbound/telemetry/      OpenTelemetry wiring
  adapters/kafka/            cloudevents, wire: the shared envelope plumbing
  architecture/              the fitness tests
features/                    godog BDD (Gherkin)
web/                         the linehaul_mfe console remote
```

The layering rules are executable tests in `internal/architecture/`, not
conventions: `architecture_test.go` (hexagonal dependencies),
`runtime_no_memory_fitness_test.go` (no binary links the in-memory doubles),
`mcp_readonly_fitness_test.go`, `kafka_writer_topic_fitness_test.go` /
`kafka_writer_acks_fitness_test.go`, `outbox_relay_cap_fitness_test.go`,
`a2a_fitness_test.go` and `no_go_files_at_root_fitness_test.go`, among
others — run by `make arch-test`.

## Domain events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml), catalogue in [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md).
CloudEvents 1.0 structured JSON, type `com.retailnetwork.logistics.linehaul-transportation.<entity>.<Event>` (`departure`, `load`), `subject` and Kafka
key = aggregate id.

- **Published** to `retailnetwork.linehaul-transportation.events`: `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`,
  `BookingMoved`, `BookingFailed`, `LoadDeparted`, `LoadArrived` (the last two carry `departureKind` and `nodeType`).
  To `retailnetwork.linehaul-transportation.analytics` only: `DepartureDeparted`, `DepartureArrived`, `LoadClosed`.
  Real types, verbatim from the contract:
  `com.retailnetwork.logistics.linehaul-transportation.departure.DepartureScheduled` /
  `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived`.
- **Consumed**: node-topology `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `LaneOpened`,
  `LaneClosed`; order-sourcing `FulfillmentReleasedToNode`, `FulfillmentReassigned`, `FulfillmentLocked`, `FulfillmentCancelled`;
  shipping-and-tracking `LabelIssued`, `LabelVoided`. Idempotent on the CloudEvents id, with dead letters on `<topic>.dlq`
  ([ADR 0007](docs/adr/0007-kafka-consumers-inbox-and-dead-letters.md)).

Delivery is at-least-once with a stable event id ([ADR 0005](docs/adr/0005-outbox-relay-and-attempt-cap.md)).

## Running it

Real PostgreSQL and Kafka only. `DATABASE_URL`, `KAFKA_BROKERS` and the three consumer groups (`NODE_TOPOLOGY_CONSUMER_GROUP`,
`ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_AND_TRACKING_CONSUMER_GROUP`) are mandatory (a boot error names the missing variable); the embedded
migrations run at boot and the outbox relay is always on.

```sh
export DATABASE_URL=postgres://user:***@localhost:5432/linehaul?sslmode=disable
export KAFKA_BROKERS=localhost:9092    # the platform's single broker from the kind cluster, or your own
export NODE_TOPOLOGY_CONSUMER_GROUP=linehaul-transportation-node-topology
export ORDER_SOURCING_CONSUMER_GROUP=linehaul-transportation-order-sourcing
export SHIPPING_AND_TRACKING_CONSUMER_GROUP=linehaul-transportation-shipping
go run ./cmd/api                       # listens on :8080 (HTTP_ADDR)
```

All settings, the probes, the shutdown order and the outbox and dead-letter runbook: [Runtime](https://iqvo.github.io/linehaul-transportation/docs/overview/runtime) and [Configuration](https://iqvo.github.io/linehaul-transportation/docs/operations/configuration).

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | **required** | Postgres DSN; unset ⇒ boot error naming the variable |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers; feeds the relay, the consumers and `/readyz` |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | **required** | Consumer group of the node-topology consumer |
| `ORDER_SOURCING_CONSUMER_GROUP` | **required** | Consumer group of the order-sourcing consumer |
| `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | **required** | Consumer group of the shipping-and-tracking consumer |
| `HTTP_ADDR` | `:8080` | Listen address |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or plain seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `GENERATE_INTERVAL` | `1h` | How often the horizon roll runs (Go duration or plain seconds) |
| `PACKAGES_PER_ORDER` | `1` | Packages one order adds to a pickup (ADR 0002 A9) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |

`cmd/mcp` reads `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL`
(default `DATABASE_URL`), `MCP_ADDR` (default `:8090`) and `LOG_LEVEL`.
`cmd/a2a` reads `DATABASE_URL` (**required**), `A2A_AGENT_URL` (**required**,
the URL the Agent Card advertises), `MIGRATIONS_DATABASE_URL` (default
`DATABASE_URL`), `A2A_ADDR` (default `:8093`) and `LOG_LEVEL`.

All three binaries export telemetry and read `OTEL_EXPORTER_OTLP_ENDPOINT`
(default `localhost:4317`), `OTEL_SERVICE_NAME` (default
`linehaul-transportation`; `linehaul-transportation-mcp` /
`linehaul-transportation-a2a` for the agent binaries), `SERVICE_VERSION`
(default `dev`) and `ENVIRONMENT` (default `local`) — see
[Observability](#observability).

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0010](docs/adr/0010-fleet-observability-standard.md)): JSON logs carry `trace_id`/`span_id`
and a `request_id`; `cmd/api` and `cmd/mcp` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`,
`ENVIRONMENT`; export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business
counter `linehaul.loads.dispatched` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is
picked up by the consumers. The chart's `otel.*` values render the env.

## API

Contract: [`apis/openapi.yaml`](apis/openapi.yaml). No authentication. Errors are RFC 9457 `application/problem+json` (`type` is a slug such as
`not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`, `departure-full`, `package-already-loaded`).
Every `POST` needs an `Idempotency-Key` header (1 to 128 printable ASCII characters, one per logical submit; `PUT` is idempotent by method and unkeyed): a repeat of the same request replays the first response for 24 hours with `Idempotency-Replayed: true`, and the same key on a different request is `409 idempotency-key-reused`. `GET /departures` is keyset-paged: `limit` (1-500, default 100) and the opaque `cursor`, answered as `{"items":[...],"nextCursor":"..."}`; any other query parameter is `400 unknown-query-parameter` (ADR 0012).

| Method and path | Operation |
| --- | --- |
| `POST /lane-schedules`, `POST /lane-schedules/{laneId}/retirements` | define (create or replace), retire |
| `GET /departures?origin=&destination=&from=&to=&limit=&cursor=`, `GET /departures/{departureId}` | list, read |
| `POST /departures/{departureId}/cancellations`, `PUT /departures/{departureId}/capacity` | planner commands |
| `POST /loads`, `POST /loads/{loadId}/packages` | open a load, add a package |
| `POST /loads/{loadId}/closures`, `POST /loads/{loadId}/dispatches`, `POST /loads/{loadId}/receipts` | load lifecycle |
| `GET /healthz`, `GET /readyz` | liveness, readiness (served by the binary; the spec covers the eleven resource paths above) |

## Helm chart

The Helm chart is `charts/linehaul-transportation` (one `api` Deployment and Service; HPA, Ingress and Gateway API route are off by default).
It refuses to render without `database.url` or `database.existingSecret`, `kafka.brokers` and the three `config.*ConsumerGroup` values:

```sh
helm template linehaul-transportation charts/linehaul-transportation -f charts/linehaul-transportation/ci/default-values.yaml
bash charts/linehaul-transportation/tests/render_test.sh
python3 charts/linehaul-transportation/tests/test_service_selectors.py
```

Values that matter: `database.url` / `database.existingSecret`, `kafka.brokers`, `config.*` (`httpAddr`, `logLevel`, `outboxRelayInterval`,
`outboxMaxAttempts`, `generateInterval`, `packagesPerOrder`, `shutdownDrainDelay`, the three consumer groups), the `otel.*` block, `environment`,
and the optional `mcp`, `a2a` and `frontend` components (default off). Render invariants are asserted by
`charts/linehaul-transportation/tests/render_test.sh` and `test_service_selectors.py` (every Service selects exactly one Deployment), over
`ci/default-values.yaml` and `ci/all-components-values.yaml`. Linted in CI with
`ct lint --charts charts/linehaul-transportation`. Chart values, hardening and the grace-period rule: [Runtime](https://iqvo.github.io/linehaul-transportation/docs/overview/runtime#deployment-helm-chart).

### Running the MCP server in Kubernetes

`cmd/mcp` exposes the same read use cases the REST adapter serves to AI agents over the
Model Context Protocol ([ADR 0008](docs/adr/0008-mcp-server-adoption.md)): Streamable HTTP at `/mcp` (and `/`), an open `GET /healthz`,
official Go SDK, **no auth** (fleet decision) and **no writes** — no Kafka, no outbox, no consumers in that binary.

| Tool | Mirrors | What it returns |
| --- | --- | --- |
| `list_departures(origin?, destination?, from?, to?, limit?)` | `GET /departures` | the horizon by `cutoffAt`: kind, lane, leg, cutoff/departs/arrives, capacity, booked, remaining, state |
| `get_departure(departureId)` | `GET /departures/{departureId}` | one departure, plus `cancelReason`/`cancelledAt`/`departedAt`/`arrivedAt` once it ended |

Tool errors carry the REST problem slug (`not-found`, `validation-failed`) as their prefix. The tool set is pinned by tests
(`internal/adapters/inbound/mcp`) and byte-pinned in its golden registry; adding a tool is an ADR 0008 amendment.

```sh
export DATABASE_URL=postgres://user:***@localhost:5432/linehaul?sslmode=disable
go run ./cmd/mcp                            # MCP_ADDR :8090, LOG_LEVEL info
```

In the cluster: `mcp.enabled=true` on the chart (own Deployment + Service, default off); the Kong route
`/api/mcp/linehaul-transportation` (rewritten to `/mcp`) is opt-in via `mcp.route.enabled`.

### Running the A2A agent in Kubernetes

`cmd/a2a` exposes the same two reads to peer agents as a delegation channel over Agent2Agent
([ADR 0011](docs/adr/0011-a2a-inbound-adapter.md)): JSON-RPC at `/` and `/a2a`, the Agent Card at `/.well-known/agent-card.json`, an open
`GET /healthz`, official Go SDK, **no auth** (fleet decision) and **no writes** — no Kafka, no outbox, no consumers in that binary. A2A
carries delegation only; facts still travel as CloudEvents on Kafka.

| Skill | Mirrors | What it answers |
| --- | --- | --- |
| `list_departures` (`origin?`, `destination?`, `from?`, `to?`, `limit?`) | MCP `list_departures` | the horizon by `cutoffAt` with capacity, booked, remaining and state |
| `get_departure` (`departureId`) | MCP `get_departure` | one departure, plus the cancel/depart/arrive trail |

A request is `{"skill":"<id>","arguments":{...}}` in a data part; unknown arguments are refused, and rejections come back in-band as
problems (`404 not-found`, `422 validation-failed`, `400 malformed-request|unknown-skill|invalid-argument`, `500 internal-error`).

```sh
export DATABASE_URL=postgres://user:***@localhost:5432/linehaul?sslmode=disable
export A2A_AGENT_URL=http://localhost:8093/    # mandatory: the URL the card advertises
go run ./cmd/a2a                               # A2A_ADDR :8093, LOG_LEVEL info
```

In the cluster: `a2a.enabled=true` on the chart (own Deployment + Service, default off); `A2A_AGENT_URL` defaults to the in-cluster Service
DNS (`a2a.agentUrl` overrides it); the Kong route `/api/a2a/linehaul-transportation` (rewritten to `/`) is opt-in via `a2a.route.enabled`.

## Operator micro-frontend (`web/`)

`web/` is the warehouse-console Module Federation remote `linehaul_mfe` ([ADR 0009](docs/adr/0009-console-remote.md)): a small
**read-only** React UI over this context's two REST reads — the departure horizon (origin/destination/cutoff-window/limit filters)
and one departure's detail. It resolves its API base at runtime from the console's `window.__WAREHOUSE_CONFIG__.apiOrigin`
(`apiOrigin` → `${apiOrigin}/api/linehaul-transportation`), never calls a sibling context, and has no write action.

```sh
cd web && npm ci && npm run dev              # standalone on :4015 (strictPort)
cd web && npm run lint && npx tsc -b && npm test && npm run build
```

In the cluster: `frontend.enabled=true` on the chart (nginx pod, ClusterIP, no env, default off), served by warehouse-infra's
web gateway at `/mfes/linehaul-transportation/`.

## Local development / quality gate

Every CI sensor is a `make` target. `make help` lists them all.

| Command | What it runs |
| --- | --- |
| `make check` | format, vet, build, lint, unit, handler and BDD tests (the pre-push gate) |
| `make check-fast` | fmt-check, vet, arch-test |
| `make check-all` | check + coverage + arch-test + bdd |
| `make coverage` | 90% gate over `internal/domain` and `internal/application` |
| `make mutation` | gremlins on every `internal/domain/<pkg>` |
| `make arch-test` | hexagonal and harness fitness tests |
| `make bdd` | godog scenarios in `features/` |
| `make integration` | testcontainers Postgres and Kafka: repositories, relay, middleware, consumers, real-wiring test (needs Docker) |
| `make vuln` | govulncheck |

Git hooks are managed by [lefthook](https://github.com/evilmartians/lefthook)
(`lefthook.yml`): pre-commit runs `fmt-check` + `vet` + `lint`; pre-push runs
`make check`. Activate once per clone: `lefthook install`.

## Tests

- **Unit** — `go test ./...` (domain, use cases, handlers; `make test`).
- **BDD acceptance** — godog/Gherkin over the real HTTP API in `features/` (`assignments`, `booking`, `loads`, `pickups`, `schedules`; `make bdd`).
- **Integration** — testcontainers Postgres and Kafka: repositories, relay, middleware, consumers and a real-wiring test (`make integration`, build-tagged, needs only Docker).
- **Coverage gate** — `make coverage`: ≥ 90% over `internal/domain` + `internal/application`.
- **Mutation** — gremlins over every `internal/domain/<pkg>` (`make mutation`; the exhaustive `mutation-full`).
- **Architecture fitness** — `internal/architecture/` (`make arch-test`).
- **Web** — lint, `tsc -b`, vitest and build in `web/`.

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`, `test`
(with the 90% coverage gate), `bdd`, `integration`, `mutation-fast`, `api-lint`,
`vuln`, `chart-tests`, `arch-test` and `web` on every push and PR (`helm-lint`
with `ct lint` on PRs); the exhaustive `mutation` and the advisory `drift`
sensor run only on the weekly schedule or manual dispatch; `docker-publish`
pushes the image to GHCR on `main` once the gate jobs are green, and `release`
then tags the next version and publishes the chart.

## Architecture Decision Records

The ADR index lives at [docs/adr/index.md](docs/adr/index.md) — 11 decisions,
from the bounded context and aggregates to the Kafka consumers' inbox and dead
letters, the MCP server, the console remote and the A2A adapter.

## License

MIT — see [LICENSE](LICENSE).
