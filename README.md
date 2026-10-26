# linehaul-transportation

Retail network, bounded context **Linehaul Transportation** (Supporting, logistics tier). Runs the middle mile: turns lanes into scheduled
departures (including a pickup departure at every vendor node cutoff), keeps each departure's remaining capacity, builds loads of packages
onto departures, and reports when loads depart and arrive.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

The design is proposed in [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/linehaul-transportation); `model/model.yaml` there is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

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

## REST API

Contract: [`apis/openapi.yaml`](apis/openapi.yaml). No authentication. Errors are RFC 9457 `application/problem+json` (`type` is a slug such as
`not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`, `departure-full`, `package-already-loaded`).
Every state-changing request needs an `Idempotency-Key` header: a repeat with the same request replays the first response for 24 hours.

| Method and path | Operation |
| --- | --- |
| `POST /lane-schedules`, `POST /lane-schedules/{laneId}:retire` | define (create or replace), retire |
| `GET /departures?origin=&destination=&from=&to=`, `GET /departures/{departureId}` | list, read |
| `POST /departures/{departureId}:cancel`, `POST /departures/{departureId}:change-capacity` | planner commands |
| `POST /loads`, `POST /loads/{loadId}/packages` | open a load, add a package |
| `POST /loads/{loadId}:close`, `:dispatch`, `:receive` | load lifecycle |
| `GET /healthz`, `GET /readyz` | liveness, readiness |

## Events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml), catalogue in [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md).
CloudEvents 1.0 structured JSON, type `com.retailnetwork.logistics.linehaul-transportation.<entity>.<Event>` (`departure`, `load`), `subject` and Kafka
key = aggregate id.

- **Published** to `retailnetwork.linehaul-transportation.events`: `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`,
  `BookingMoved`, `BookingFailed`, `LoadDeparted`, `LoadArrived` (the last two carry `departureKind` and `nodeType`).
  To `retailnetwork.linehaul-transportation.analytics` only: `DepartureDeparted`, `DepartureArrived`, `LoadClosed`.
- **Consumed**: node-topology `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `LaneOpened`,
  `LaneClosed`; order-sourcing `FulfillmentReleasedToNode`, `FulfillmentReassigned`, `FulfillmentLocked`, `FulfillmentCancelled`;
  shipping-and-tracking `LabelIssued`, `LabelVoided`. Idempotent on the CloudEvents id, with dead letters on `<topic>.dlq`
  ([ADR 0007](docs/adr/0007-kafka-consumers-inbox-and-dead-letters.md)).

Delivery is at-least-once with a stable event id ([ADR 0005](docs/adr/0005-outbox-relay-and-attempt-cap.md)).

## MCP server (read-only)

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

## Web remote (linehaul_mfe)

`web/` is the warehouse-console Module Federation remote `linehaul_mfe` ([ADR 0009](docs/adr/0009-console-remote.md)): a small
**read-only** React UI over this context's two REST reads — the departure horizon (origin/destination/cutoff-window/limit filters)
and one departure's detail. It resolves its API base at runtime from the console's `/config.json`
(`apiOrigin` → `${apiOrigin}/api/linehaul-transportation`), never calls a sibling context, and has no write action.

```sh
cd web && npm ci && npm run dev              # standalone on :4015 (strictPort)
cd web && npm run lint && npx tsc -b && npm test && npm run build
```

In the cluster: `frontend.enabled=true` on the chart (nginx pod, ClusterIP, no env, default off), served by warehouse-infra's
web gateway at `/mfes/linehaul-transportation/`.

## Run it

Real PostgreSQL and Kafka only. `DATABASE_URL`, `KAFKA_BROKERS` and the three consumer groups (`NODE_TOPOLOGY_CONSUMER_GROUP`,
`ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_AND_TRACKING_CONSUMER_GROUP`) are mandatory (a boot error names the missing variable); the embedded
migrations run at boot and the outbox relay is always on.

```sh
export DATABASE_URL=postgres://user:***@localhost:5432/linehaul?sslmode=disable
export KAFKA_BROKERS=localhost:29092   # whatever your broker advertises
export NODE_TOPOLOGY_CONSUMER_GROUP=linehaul-transportation-node-topology
export ORDER_SOURCING_CONSUMER_GROUP=linehaul-transportation-order-sourcing
export SHIPPING_AND_TRACKING_CONSUMER_GROUP=linehaul-transportation-shipping
go run ./cmd/api                       # listens on :8080 (HTTP_ADDR)
```

All settings, the probes, the shutdown order and the outbox and dead-letter runbook: [docs/runtime.md](docs/runtime.md).

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0010](docs/adr/0010-fleet-observability-standard.md)): JSON logs carry `trace_id`/`span_id`
and a `request_id`; `cmd/api` and `cmd/mcp` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`,
`ENVIRONMENT`; export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business
counter `linehaul.loads.dispatched` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is
picked up by the consumers. The chart's `otel.*` values render the env.

## Deploy

The Helm chart is `charts/linehaul-transportation` (one `api` Deployment and Service; HPA, Ingress and Gateway API route are off by default).
It refuses to render without `database.url` or `database.existingSecret`, `kafka.brokers` and the three `config.*ConsumerGroup` values:

```sh
helm template linehaul-transportation charts/linehaul-transportation -f charts/linehaul-transportation/ci/default-values.yaml
bash charts/linehaul-transportation/tests/render_test.sh
python3 charts/linehaul-transportation/tests/test_service_selectors.py
```

Chart values, hardening and the grace-period rule: [docs/runtime.md](docs/runtime.md#deployment-helm-chart).

## Develop

| Command | What it runs |
| --- | --- |
| `make check` | format, vet, build, lint, unit, handler and BDD tests (the pre-push gate) |
| `make coverage` | 90% gate over `internal/domain` and `internal/application` |
| `make mutation` | gremlins on every `internal/domain/<pkg>` |
| `make arch-test` | hexagonal and harness fitness tests |
| `make bdd` | godog scenarios in `features/` |
| `make integration` | testcontainers Postgres and Kafka: repositories, relay, middleware, consumers, real-wiring test (needs Docker) |
| `make vuln` | govulncheck |

Layout: `internal/domain/{shared,departure,schedule,load,network,volume}` (pure Go), `internal/application/{ports,views,usecases,repository,outbox}`,
`internal/adapters/inbound/{http,kafka,mcp}` (chi, consumers, read-only MCP server), `internal/adapters/outbound/{postgres,kafka,outbox,clock,ids,memory,repocontract}`,
`internal/adapters/kafka/{cloudevents,wire}`, `cmd/{api,mcp}`, `web/` (the linehaul_mfe remote), `features/` (godog BDD). The `memory` and `repocontract` packages are test doubles; `cmd/**`
never imports them.

Decisions: [docs/adr/](docs/adr/index.md). Packaging checklist: [docs/before-first-release.md](docs/before-first-release.md).
