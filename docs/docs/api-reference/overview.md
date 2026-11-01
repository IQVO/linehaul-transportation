---
id: overview
title: API overview
slug: /api-reference
sidebar_label: Overview
sidebar_position: 1
description: "The interfaces of Linehaul Transportation: REST, events, MCP and A2A, the endpoint table and the contract conventions."
---

# API overview

:::info Implemented by cmd/api, cmd/mcp and cmd/a2a
`cmd/api` serves every REST route below (`internal/adapters/inbound/http`) on `HTTP_ADDR` (default `:8080`). The contracts are `apis/openapi.yaml` (OpenAPI 3.0.3, version 0.1.0) and
`apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0). The router registers exactly the eleven resource paths of the spec plus `/healthz` and `/readyz`, which the spec does not list.
See the [Quickstart](/docs/overview/quickstart) for real responses.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Server named in the spec: `http://localhost:8080`. Through Kong on the kind cluster: `http://localhost:8000/api/linehaul-transportation` (prefix stripped). |
| Events | `apis/asyncapi.yaml` | Two published topics and three consumed ones; summarised on the [event catalogue](/docs/api-reference/events), every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0008](/docs/adr/0008-mcp-server-adoption), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` and `/` (port 8090; Kong `/api/mcp/linehaul-transportation`, opt-in). Two read-only tools, no write tool: `list_departures` and `get_departure`. See [MCP tools](/docs/mcp/tools). No auth, by fleet decision. |
| A2A | [ADR 0011](/docs/adr/0011-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; Kong `/api/a2a/linehaul-transportation`, opt-in) and the Agent Card at `/.well-known/agent-card.json`. Two read-only skills, no write skill: `list_departures` (`origin`, `destination`, `from`, `to`, `limit`) and `get_departure` (`departureId`). A request is the data part `{"skill":"<id>","arguments":{...}}` of a `SendMessage` call; unknown arguments are refused and rejections come back in the message as problems (`404 not-found`, `422 validation-failed`, `400 malformed-request`, `unknown-skill` or `invalid-argument`, `500 internal-error`), never as JSON-RPC errors. No auth, by fleet decision. |

## Endpoints

| Tag | Endpoint | Operation id | Success | Notes |
| --- | --- | --- | --- | --- |
| Lane schedules | `POST /lane-schedules` | `defineLaneSchedule` | `201` created, `200` slots replaced | Body `laneId`, `slots[1..100]`; lane must be known |
| Lane schedules | `POST /lane-schedules/{laneId}:retire` | `retireLaneSchedule` | `200` | Idempotent on a RETIRED schedule |
| Departures | `GET /departures` | `listDepartures` | `200 {departures}` | Filters `origin`, `destination`, `from`, `to`, `limit` (1..1000, default 500); ordered by `cutoffAt` |
| Departures | `GET /departures/{departureId}` | `getDeparture` | `200` | `404 not-found` |
| Departures | `POST /departures/{departureId}:cancel` | `cancelDeparture` | `200` | Body `reason`; rebooks the assignments booked on it |
| Departures | `POST /departures/{departureId}:change-capacity` | `changeDepartureCapacity` | `200` | Body `capacityPackages`; not below the booked count |
| Loads | `POST /loads` | `openLoad` | `201` | Body `departureId`; one load per departure |
| Loads | `POST /loads/{loadId}/packages` | `addPackageToLoad` | `200` | Body `trackingNumber` (max 64 characters) |
| Loads | `POST /loads/{loadId}:close` | `closeLoad` | `200` | OPEN to CLOSED |
| Loads | `POST /loads/{loadId}:dispatch` | `dispatchLoad` | `200` | CLOSED to DEPARTED |
| Loads | `POST /loads/{loadId}:receive` | `receiveLoad` | `200` | DEPARTED to ARRIVED |
| Health | `GET /healthz`, `GET /readyz` | none (not in the spec) | `200` | `/readyz` is 503 while draining or when Postgres or Kafka is down; `cmd/mcp` and `cmd/a2a` serve `/healthz` only |

## Conventions in the contract

- **Errors** are RFC 9457 `application/problem+json` with `type = https://retailnetwork.example/problems/<slug>`. The slugs: `malformed-request`,
  `request-body-too-large`, `not-found`, `invalid-departure-times`, `overlapping-slots`, `validation-failed`, `departure-full`, `lane-closed`,
  `departure-closed`, `package-already-loaded`, `load-over-capacity`, `load-already-exists`, `illegal-transition`, `concurrent-update`,
  `idempotency-key-required`, `idempotency-key-invalid`, `idempotency-key-reused` and `internal-error`. Statuses and remedies are on
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).
- **`Idempotency-Key`.** Required on every state-changing POST (`400 idempotency-key-required` when absent, `idempotency-key-invalid` when empty or longer than 200 characters). The
  same key with the same method, path and body replays the first stored response (2xx and 4xx) with `Idempotency-Replayed: true` for 24 hours; the same key with a different request is
  `422 idempotency-key-reused`. 5xx answers are not stored.
- **Optimistic concurrency.** Aggregates carry a version; a write that loses a race is `409 concurrent-update`.
- **Time.** Every instant is RFC 3339 in UTC. Lane-schedule slots are wall-clock `HH:MM` times: cutoff and departure in the time zone of the lane's origin node, arrival in
  the zone of its destination node.
- **Paging.** `GET /departures` returns up to `limit` rows ordered by `cutoffAt`, then `departureId`; there is no cursor: narrow the window with `from` and `to`.
- **Commands are idempotent by state.** Repeating a command that already holds (close of a CLOSED load, cancel of a CANCELLED departure) returns the resource unchanged.
- **Auth**: none. The fleet reverted REST, MCP and A2A auth, and `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns under `internal/adapters/inbound`.
- **Events** are CloudEvents 1.0, structured mode; the contract is dispatched on the full `type`, deduplicated on `id`, and unknown types are ignored by consumers.

## Discrepancies between spec and code

- `apis/openapi.yaml` lists no `/healthz` and `/readyz`; the code serves both (`internal/adapters/inbound/http/server.go`).
- The AsyncAPI server block names `kafka.retail-network.internal:9092` as an example host; the real broker comes from `KAFKA_BROKERS` (platform broker `localhost:9092` from the host).
