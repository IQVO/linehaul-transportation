---
id: tools
title: MCP tools
sidebar_label: MCP tools
description: "The read-only MCP tools of Linehaul Transportation, captured from a live tools/list."
---

# MCP tools

`cmd/mcp` is a separate binary serving the Model Context Protocol over **Streamable HTTP only** (no stdio, no SSE) on `MCP_ADDR` (default `:8090`), at `/mcp` and `/`,
plus an open `GET /healthz`. It uses the official Go SDK, has **no authentication** (fleet decision) and **no write path**: it holds only the departure read interface, starts no
Kafka client, relay or consumer, and calls no other context. The tool set is pinned by tests (`internal/adapters/inbound/mcp`, `TestMCPServerIsReadOnly`,
`TestMCPOptionalToolInputsCarryOmitempty`) and adding a tool is an amendment of [ADR 0008](/docs/adr/0008-mcp-server-adoption).

Server `linehaul-transportation-mcp` 1.0.0, protocol `2025-03-26`, capabilities `tools` (`listChanged`) and `logging`. The listing below is the answer of a live
`tools/list` against a running `cmd/mcp`; every tool carries `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false`.

| Tool | Annotation | Purpose |
| --- | --- | --- |
| `list_departures` | read-only | Page through the departure horizon by `cutoffAt`, optionally by origin, destination and cutoff window |
| `get_departure` | read-only | Read one departure by id |

There is no tool that schedules a lane, cancels or re-capacitates a departure or touches a load: those are REST operations of `cmd/api`.

## `list_departures`

Departures of the horizon ordered by `cutoffAt` then `departureId`.

| Input | Type | Required | Meaning |
| --- | --- | --- | --- |
| `origin` | string | no | keep only departures leaving this origin node id; omitted means every origin |
| `destination` | string | no | keep only departures bound for this destination node id |
| `from` | string (RFC 3339) | no | earliest `cutoffAt`, inclusive; omitted means unbounded |
| `to` | string (RFC 3339) | no | latest `cutoffAt`, exclusive; omitted means unbounded |
| `limit` | integer | no | at most this many departures, 1..1000; omitted or 0 means 500 |

Output `departures` (array, may be null in the schema): each item has `departureId`, `laneId`, `kind` (`PICKUP`, `LINEHAUL`, `AIR`), `originNodeId`,
`destinationNodeId`, `cutoffAt`, `departsAt`, `arrivesAt`, `capacityPackages`, `bookedPackages`, `remainingPackages`, `state` (`SCHEDULED`, `CLOSED`, `DEPARTED`,
`ARRIVED`, `CANCELLED`) and, once they apply, `cancelReason`, `cancelledAt`, `departedAt`, `arrivedAt`. Additional properties are refused.

Errors: a `limit` outside 1..1000, a `from` after `to`, or a time that is not an RFC 3339 instant fails with `validation-failed`.

## `get_departure`

| Input | Type | Required | Meaning |
| --- | --- | --- | --- |
| `departureId` | string | yes | the departure id, for example `dep-7f3a` |

Output: one departure with the same fields as above. An unknown id fails with `not-found`.

## Calling the tools

```sh
# 1. initialize: the response carries the Mcp-Session-Id header
curl -s -i -X POST localhost:8090/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

# 2. call a tool with that session id
curl -s -X POST localhost:8090/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -H 'Mcp-Session-Id: <id>' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_departures","arguments":{"limit":1}}}'
```

Captured answer (the `structuredContent` mirrors the text content):

```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"{\"departures\":[{...}]}"}],"structuredContent":{"departures":[{"arrivesAt":"2026-11-03T02:45:00Z","bookedPackages":1,"capacityPackages":500,"cutoffAt":"2026-11-02T21:00:00Z","departedAt":"2026-10-31T22:25:22.770828Z","departsAt":"2026-11-02T21:30:00Z","departureId":"dep-3b54a35b8ce988dbbd6feb8d341b2535","destinationNodeId":"NODE-RJ-DC","kind":"LINEHAUL","laneId":"LANE-SP-RJ","originNodeId":"NODE-SP-DC","remainingPackages":499,"state":"DEPARTED"}]}}}
```

A tool error is a normal result with `isError: true` and the REST problem slug as prefix, for example `get_departure` with `dep-nope`:

```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"not-found: departure dep-nope: not found"}],"isError":true}}
```

## Deployment

In the cluster: `mcp.enabled=true` on the chart (its own Deployment and Service, default off); the Kong route `/api/mcp/linehaul-transportation` (rewritten to `/mcp`) is opt-in via
`mcp.route.enabled`. `OTEL_SERVICE_NAME` defaults to `linehaul-transportation-mcp`. Settings: [Configuration](/docs/operations/configuration).
