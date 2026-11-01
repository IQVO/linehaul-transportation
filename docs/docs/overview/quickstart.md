---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
description: "Run Linehaul Transportation locally against real Postgres and Kafka and make the first calls. Responses below come from a real run."
---

# Quickstart

This page takes you from a clean checkout to a running `cmd/api`, `cmd/mcp` and `cmd/a2a`, with real responses. The service has no in-memory
mode: Postgres and Kafka must be reachable or the process refuses to boot ([Configuration](/docs/operations/configuration)).

## Prerequisites

- Go 1.27.3 (the version in `go.mod`), `make`, `curl`, Docker (for Postgres, Kafka and the testcontainers integration tests).
- Optional: `golangci-lint` v2.14.0, `gremlins` v0.6.0 and `govulncheck` for the quality targets ([Testing](/docs/development/testing)); `lefthook install`
  activates the pre-commit (`fmt-check`, `vet`, `lint`) and pre-push (`make check`) hooks.
- Node 20+ only if you work on `web/` or on this documentation site (`cd docs && npm ci && npm start`).

## Make targets

`make help` lists them all. The ones you need first:

| Target | What it does |
| --- | --- |
| `make build` | `go build ./...` |
| `make test` | `go test ./... -race`: unit, handler and godog tests, no database needed |
| `make check` | `fmt-check vet build lint test`, the pre-push gate |
| `make check-all` | `check` + `coverage` + `arch-test` + `bdd` |
| `make integration` | testcontainers Postgres and Kafka; needs only Docker |
| `make arch-test`, `make mutation`, `make vuln` | fitness tests, gremlins, govulncheck |

## Postgres and Kafka

On the platform's kind cluster there is exactly ONE Kafka broker, reachable from the host at `localhost:9092`; the old docker-compose Kafka is
retired. For a stand-alone run you can use that broker (`KAFKA_BROKERS=localhost:9092`), but note that it is shared: with your own consumer
group the three consumers replay the real node-topology, order-sourcing and shipping-and-tracking topics into your local database, and
the relay publishes your events to `retailnetwork.linehaul-transportation.events`. For experiments, start throwaway containers instead (this is what the
captured run below used: Postgres on 55441, Kafka on 29192):

```sh
docker run -d --name lh-pg -p 55441:5432 -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=linehaul postgres:16-alpine
docker run -d --name lh-kafka -p 29192:29192 \
  -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
  -e KAFKA_LISTENERS=PLAINTEXT://:29192,CONTROLLER://:9093 \
  -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:29192 \
  -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
  -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
  -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 \
  -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
  -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
  apache/kafka:3.9.0
```

Start Kafka before the API: a consumer that cannot dial the broker makes the process exit with `worker stopped on its own` (see
[Troubleshooting](/docs/operations/troubleshooting)). The migrations run at boot, so the database needs no preparation.

## Run the three binaries

```sh
export DATABASE_URL='postgres://postgres@localhost:55441/linehaul?sslmode=disable'
export KAFKA_BROKERS=localhost:29192
export NODE_TOPOLOGY_CONSUMER_GROUP=linehaul-transportation-node-topology
export ORDER_SOURCING_CONSUMER_GROUP=linehaul-transportation-order-sourcing
export SHIPPING_AND_TRACKING_CONSUMER_GROUP=linehaul-transportation-shipping-and-tracking
go run ./cmd/api                      # REST on :8080 (HTTP_ADDR), consumers, relay, horizon roll

# in other terminals, with the same DATABASE_URL:
go run ./cmd/mcp                      # MCP on :8090
A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a   # A2A on :8093
```

`cmd/mcp` and `cmd/a2a` need no Kafka. The captured run used `HTTP_ADDR=:38480` because other ports on the machine were taken; the examples below
use that port, replace it with `:8080`.

## First calls (captured from a real run)

**Probes.**

```sh
curl -s -i localhost:38480/healthz
curl -s -i localhost:38480/readyz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ok"}
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}
```

**Teach the service a lane.** Lanes and nodes come from node-topology; the service keeps local copies built from its events and refuses to
schedule an unknown lane. Without the real node-topology you can publish the events yourself (one CloudEvents JSON per line; two
`NodeRegistered` for `NODE-SP-DC` and `NODE-RJ-DC`, `nodeType` `SORTATION_CENTER`, `operator` `NETWORK`, `timezone` `America/Sao_Paulo`, and this one):

```json
{"specversion":"1.0","id":"11111111-1111-4111-8111-111111111111","source":"/retailnetwork/node-topology","type":"com.retailnetwork.logistics.node-topology.lane.LaneOpened","subject":"LANE-SP-RJ","time":"2026-10-31T12:00:00Z","datacontenttype":"application/json","data":{"laneId":"LANE-SP-RJ","originNodeId":"NODE-SP-DC","destinationNodeId":"NODE-RJ-DC","mode":"GROUND","nominalTransitMinutes":360}}
```

```sh
docker exec -i lh-kafka /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server localhost:29192 \
  --topic retailnetwork.node-topology.events < events.ndjson
```

**Define the weekly schedule** (`POST /lane-schedules`; every POST needs an `Idempotency-Key`):

```sh
curl -s -i -X POST localhost:38480/lane-schedules -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-lane-sp-rj-1' --data @schedule.json
```

```text
HTTP/1.1 201 Created
Content-Type: application/json

{"laneId":"LANE-SP-RJ","state":"ACTIVE","slots":[{"dayOfWeek":"MONDAY","cutoffLocalTime":"18:00","departsLocalTime":"18:30","arrivesLocalTime":"23:45","capacityPackages":500},{"dayOfWeek":"THURSDAY","cutoffLocalTime":"18:00","departsLocalTime":"18:30","arrivesLocalTime":"23:45","capacityPackages":500}]}
```

Without the header:

```text
HTTP/1.1 400 Bad Request
Content-Type: application/problem+json

{"type":"https://retailnetwork.example/problems/idempotency-key-required","title":"Idempotency-Key header is required","status":400,"detail":"send an Idempotency-Key header with a unique value per logical request","instance":"/lane-schedules"}
```

Repeating the same request with the same key replays the stored answer and adds `Idempotency-Replayed: true`.

**List the horizon.** Departures are generated by the horizon roll at boot and then every `GENERATE_INTERVAL` (1 h); restart `cmd/api` after
defining a schedule to see departures at once:

```sh
curl -s "localhost:38480/departures?limit=2"
```

```json
{"departures":[{"departureId":"dep-3b54a35b8ce988dbbd6feb8d341b2535","laneId":"LANE-SP-RJ","kind":"LINEHAUL","originNodeId":"NODE-SP-DC","destinationNodeId":"NODE-RJ-DC","cutoffAt":"2026-11-02T21:00:00Z","departsAt":"2026-11-02T21:30:00Z","arrivesAt":"2026-11-03T02:45:00Z","capacityPackages":500,"bookedPackages":0,"remainingPackages":500,"state":"SCHEDULED"},{"departureId":"dep-0630863a54ca876b42722975ec3d2150","laneId":"LANE-SP-RJ","kind":"LINEHAUL","originNodeId":"NODE-SP-DC","destinationNodeId":"NODE-RJ-DC","cutoffAt":"2026-11-05T21:00:00Z","departsAt":"2026-11-05T21:30:00Z","arrivesAt":"2026-11-06T02:45:00Z","capacityPackages":500,"bookedPackages":0,"remainingPackages":500,"state":"SCHEDULED"}]}
```

`GET /departures/dep-nope` answers `404` with `application/problem+json`, type `https://retailnetwork.example/problems/not-found`.

**Run a load.** Open, add a package, close, dispatch:

```sh
curl -s -X POST localhost:38480/loads -H 'Content-Type: application/json' -H 'Idempotency-Key: demo-open-load-1' \
  -d '{"departureId":"dep-3b54a35b8ce988dbbd6feb8d341b2535"}'
# 201 {"loadId":"load-06b16401-46d8-46b5-99bb-9f7d384fb8c9","departureId":"dep-3b54...","departureKind":"LINEHAUL","originNodeId":"NODE-SP-DC","destinationNodeId":"NODE-RJ-DC","capacityPackages":500,"packageCount":0,"trackingNumbers":[],"state":"OPEN"}

curl -s -X POST localhost:38480/loads/load-06b16401-46d8-46b5-99bb-9f7d384fb8c9/packages -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-add-pkg-1' -d '{"trackingNumber":"TRK-0001"}'
# 200 ... "packageCount":1,"trackingNumbers":["TRK-0001"],"state":"OPEN"

curl -s -X POST 'localhost:38480/loads/load-06b16401-46d8-46b5-99bb-9f7d384fb8c9:dispatch' -H 'Idempotency-Key: demo-dispatch-1'
# 409 {"type":"https://retailnetwork.example/problems/illegal-transition","title":"Illegal state transition","status":409,"detail":"illegal transition: DispatchLoad on load load-06b1... in state OPEN", ...}

curl -s -X POST 'localhost:38480/loads/load-06b16401-46d8-46b5-99bb-9f7d384fb8c9:close' -H 'Idempotency-Key: demo-close-1'
# 200 ... "state":"CLOSED"
curl -s -X POST 'localhost:38480/loads/load-06b16401-46d8-46b5-99bb-9f7d384fb8c9:dispatch' -H 'Idempotency-Key: demo-dispatch-2'
# 200 ... "state":"DEPARTED","departedAt":"2026-10-31T22:25:22.770828Z"
```

A load must be CLOSED before it can be dispatched (`illegal-transition` otherwise). The outbox then holds the events of the run, all published:

```text
 id | topic                                           | event_type                                                           | attempts | published
  1 | retailnetwork.linehaul-transportation.events    | ...departure.DepartureScheduled (ids 1-4)                           | 1-2      | t
  5 | retailnetwork.linehaul-transportation.events    | ...departure.DepartureCapacityChanged                               | 1        | t
  6 | retailnetwork.linehaul-transportation.analytics | ...load.LoadClosed                                                  | 2        | t
  7 | retailnetwork.linehaul-transportation.events    | ...load.LoadDeparted                                                | 1        | t
  8 | retailnetwork.linehaul-transportation.analytics | ...departure.DepartureDeparted                                      | 1        | t
```

(`attempts` of 2 is the first publish to a topic that did not exist yet: the writer auto-creates it.)

**MCP.** Streamable HTTP, a session is opened with `initialize`:

```sh
curl -s -i -X POST localhost:8090/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
```

The response carries `Mcp-Session-Id: <id>` and `serverInfo` `linehaul-transportation-mcp` 1.0.0; send that id as the `Mcp-Session-Id` header on
`tools/list` and `tools/call`. The tools are on the [MCP tools](/docs/mcp/tools) page.

**A2A.** Agent Card and a skill call:

```sh
curl -s localhost:8093/.well-known/agent-card.json
curl -s -X POST localhost:8093/ -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' -d '{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m-1","role":"ROLE_USER","parts":[{"data":{"skill":"get_departure","arguments":{"departureId":"dep-3b54a35b8ce988dbbd6feb8d341b2535"}}}]}}}'
```

```json
{"jsonrpc":"2.0","id":1,"result":{"message":{"messageId":"...","parts":[{"data":{"skill":"get_departure","data":{"departure":{"departureId":"dep-3b54a35b8ce988dbbd6feb8d341b2535","laneId":"LANE-SP-RJ","kind":"LINEHAUL","state":"DEPARTED","bookedPackages":1,"remainingPackages":499,"departedAt":"2026-10-31T22:25:22Z"}}}},{"text":"dep-3b54a35b8ce988dbbd6feb8d341b2535: DEPARTED LINEHAUL NODE-SP-DC -> NODE-RJ-DC, cutoff 2026-11-02T21:00:00Z, 1 of 500 packages booked (499 remaining)"}],"role":"ROLE_AGENT"}}}
```

(Fields trimmed for the page; the real answer lists every departure field.)

## Through Kong on the kind cluster

On the platform cluster the REST API is behind the Kong gateway on `localhost:8000`, prefix `/api/linehaul-transportation` (stripped):
`curl -s localhost:8000/api/linehaul-transportation/departures?limit=5`. MCP and A2A are opt-in routes of the chart
(`/api/mcp/linehaul-transportation`, `/api/a2a/linehaul-transportation`).

## Clean up

```sh
docker rm -f lh-pg lh-kafka
```
