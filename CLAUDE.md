# linehaul-transportation

harness-template: v4

Retail network, bounded context Linehaul Transportation (Supporting, logistics tier). Turns lanes into scheduled departures (a pickup departure at every vendor node cutoff), keeps each departure's remaining capacity, builds loads of packages and reports when loads depart and arrive.
Consumes node-topology, order-sourcing and shipping-and-tracking events into local read models; calls no sibling at runtime.
Design source of truth: IQVO/retail-network-docs `model/model.yaml` (context linehaul-transportation). This service belongs to the retail network, not the warehouse-systems fleet.
This project is a domain-driven design study, not associated with any company: never name a real company, brand or product in code, comments, ADRs, specs, tests or fixtures (README `## Disclaimer`).

## Commands
`make check` (fmt, vet, build, lint, unit + handler + BDD; the pre-push gate), `make coverage` (>= 90% over internal/domain + internal/application), `make mutation` (gremlins on every internal/domain/<pkg>, thresholds in .gremlins.yaml strictly below the measurement),
`make arch-test`, `make integration` (testcontainers Postgres + Kafka, needs Docker; build tag `integration`), `make vuln` (needs govulncheck on PATH). Lint: `golangci-lint run --allow-parallel-runners ./...`.
Guides: `python3 scripts/harness/repo_lint.py`, `python3 scripts/harness/guide_lint.py` must stay clean. Never gate a commit on a piped command.

## Layout (hexagonal)
`internal/domain/{shared,departure,schedule,load,network,volume}` pure Go; `internal/application/{ports (interfaces only),repository (filters),views,usecases,outbox}`;
`internal/adapters/inbound/{http (chi),kafka (consumers, inbox, DLQ)}`; `internal/adapters/outbound/{postgres,kafka,outbox,clock,ids,memory,repocontract}`; `internal/adapters/kafka/{cloudevents (the ONLY envelope builder),wire}`;
`internal/testing/{pgtest,kafkatest}`; `cmd/api` (composition root, `run(ctx)`); `features/` (godog, never a .go file at the repo root). Migrations are embedded in the postgres adapter.

## Runtime rule
Real PostgreSQL and Kafka only. `DATABASE_URL`, `KAFKA_BROKERS`, `NODE_TOPOLOGY_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_AND_TRACKING_CONSUMER_GROUP` are mandatory (boot error naming the variable, before the listener binds).
The outbox relay is always on (`OUTBOX_MAX_ATTEMPTS` default 20, 0 = unlimited). `GENERATE_INTERVAL` (1h) rolls the horizon; `PACKAGES_PER_ORDER` (1) converts orders to packages (A9).
`/healthz` = liveness; `/readyz` = drain gate + Postgres ping + Kafka metadata with an EMPTY non-nil topic list. cmd/** never imports the memory or repocontract packages. No authentication on REST. See docs/runtime.md.

## REST
chi router for every operation of apis/openapi.yaml (11 routes: lane schedules, departures incl. `:cancel` and `:change-capacity`, loads incl. `/packages`, `:close`, `:dispatch`, `:receive`) plus /healthz, /readyz.
Every state-changing route takes `Idempotency-Key` (table `idempotency_keys`, key = (route, key), 24h, replays the first response; same key + other request = 422 `idempotency-key-reused`). Errors are RFC 9457 problem+json, `type` = slug (ADR 0006).

## Events: CloudEvents 1.0 is MANDATORY
type `com.retailnetwork.logistics.linehaul-transportation.<entity>.<Event>` (entity: departure, load); dataschema `urn:retailnetwork:linehaul-transportation:events:<Event>:v1`.
Integration events on `retailnetwork.linehaul-transportation.events`: DepartureScheduled, DepartureCapacityChanged, DepartureCancelled, LoadDeparted, LoadArrived (both with `departureKind`, `nodeType`), BookingMoved, BookingFailed.
Analytics-only, ONLY on `.analytics`: DepartureDeparted, DepartureArrived, LoadClosed. Subject and Kafka key = aggregate id. Use internal/adapters/kafka/cloudevents only. Transactional outbox: aggregates + outbox rows in ONE Postgres transaction.
Consumed (full-type dispatch, inbox `processed_events`, DLQ): node-topology Node*/Lane*, order-sourcing Fulfillment{ReleasedToNode,Reassigned,Locked,Cancelled}, shipping-and-tracking LabelIssued/LabelVoided (ADR 0003, 0007).
A new event: domain struct + apis/asyncapi.yaml + ADR 0004 `type:` line in the SAME commit + encoder payload + golden test (.claude/skills/how-to-add-an-integration-event).

## Domain
Aggregates, invariants and assumptions A1-A30 are in docs/adr/0002; outbox/relay/cap in 0005; HTTP, idempotency, readiness in 0006; consumers in 0007. Bookings happen at FulfillmentLocked for PICKUP departures and at AddPackage for line-haul and air legs (G15); a refused or cancelled booking is rebooked on the next departure from the same origin or fails (G14). The node never changes after lock.

## Workflow
GitFlow: feature/* -> PR into develop, CI green before merge, never push develop or main. lefthook: pre-commit fmt/vet/lint, pre-push `make check`. Commit and push after each step.
Deploy: `charts/linehaul-transportation` (api only; refuses to render without database, `kafka.brokers` and the three `config.*ConsumerGroup`; grace period must exceed drain delay + 20 s; `helm lint`/`ct lint`, `tests/render_test.sh`, `tests/test_service_selectors.py`).
