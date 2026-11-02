---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: "Symptom, cause, check and fix for the real failure modes of Linehaul Transportation, including every REST problem type."
---

# Troubleshooting

Each entry follows symptom, cause, check, fix. The checks use `psql` against the service database, `kubectl` logs and the log messages listed on
[Observability](/docs/operations/observability). The procedures behind them are on the [Runbook](/docs/operations/runbook).

## Boot and readiness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `cmd/api` exits at once with `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode` | The variable is unset or empty (`cmd/api/config.go`) | First log line of `kubectl logs --previous` | Set `database.url` or `database.existingSecret` in the chart, or export `DATABASE_URL` |
| `KAFKA_BROKERS is required: this context runs on real Kafka (...)` | Empty list (blank entries are dropped) | Same | Set `kafka.brokers` (`host:port,host:port`) |
| `NODE_TOPOLOGY_CONSUMER_GROUP is required: every consumer runs under a stable, service-prefixed consumer group` (or the order-sourcing / shipping variant) | One of the three consumer-group variables is blank | The message names the variable | Set `config.nodeTopologyConsumerGroup`, `config.orderSourcingConsumerGroup`, `config.shippingAndTrackingConsumerGroup`; the chart refuses to render without them |
| `OUTBOX_RELAY_INTERVAL="abc" is neither a Go duration nor a number of seconds`, `... must not be negative`, `PACKAGES_PER_ORDER="0" must be an integer >= 1`, `OUTBOX_MAX_ATTEMPTS=... must be an integer >= 0` | Invalid duration or integer | The message names variable and value | Use `500ms`, `2s`, `5`; see [Configuration](/docs/operations/configuration) |
| `cmd/a2a` exits with `A2A_AGENT_URL is required: the Agent Card must advertise the absolute URL clients dial` or `A2A_AGENT_URL="..." is not an absolute http(s) URL` | Missing or malformed card URL; the error is reported together with a missing `DATABASE_URL` | Boot log | Set `a2a.agentUrl` (empty in the chart means the in-cluster Service DNS) or `A2A_AGENT_URL=http://host:8093/` |
| Pod restarts during boot with `run migrations failed; retrying` (mcp, a2a) or `migrations (MIGRATIONS_DATABASE_URL / DATABASE_URL): ...` (api) | Postgres not reachable for the ~30 s retry window, a wrong direct DSN, or `MIGRATIONS_DATABASE_URL` pointing at a transaction-pooling proxy (golang-migrate's advisory lock needs a session) | `kubectl logs`; try `psql "$MIGRATIONS_DATABASE_URL"` | Fix the Secret key `MIGRATIONS_DATABASE_URL` (a direct, session-mode DSN); the startup probe allows 2 s x 45 |
| `/readyz` answers 503 `application/problem+json` of type `dependency-unavailable` naming `postgres` or `kafka` | A dependency failed its probe (verdict cached 3 s, bounded 2 s) | WARN `readiness: dependency is not ready` with `dependency` and `error` | Restore the dependency. `/healthz` stays 200 on purpose: do not restart the pod for this |
| `/readyz` 503 `{"status":"not_ready"}` | The pod is draining after SIGTERM | Log `shutting down` | Expected during rollout |
| Process exits with `worker stopped on its own` / `service exited with error` naming `... consumer` and `failed to dial` | A consumer could not connect to Kafka at all (connection refused is fatal for the worker; only stalled or reset connections are retried, up to 10 times) | Log line; `KAFKA_BROKERS` value and reachability from the pod | Fix the broker address or bring Kafka up before the pod; Kubernetes restarts until it succeeds |
| `bind: address already in use` on start | `HTTP_ADDR`, `MCP_ADDR` or `A2A_ADDR` collides with another process | Log `service exited with error` | Choose another address |

## Events and data

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| A downstream context misses an event that later events of the same aggregate overtook | The outbox row reached `OUTBOX_MAX_ATTEMPTS` (20) failures and is excluded from claims | ERROR `outbox relay: row reached the attempt cap ...`; `SELECT id, topic, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id;` | Fix the cause (broker, topic permissions), then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;` |
| Events accumulate with `published_at IS NULL` and `attempts` rising | Kafka unreachable or the topic cannot be created | ERROR `outbox relay pass failed` each pass; `/readyz` names `kafka` | Restore Kafka; no re-arm needed below the cap. The first publish to a new topic may show `attempts = 2` while it auto-creates: normal |
| Writes succeed but nothing is published | `cmd/api` is not running (only `cmd/mcp`/`cmd/a2a` are up) or the relay died | Is the `api` Deployment healthy; `worker stopped on its own` | Run `cmd/api`; the MCP and A2A binaries never relay |
| No departures appear after defining a lane schedule | Departures are produced by the horizon roll (boot, then every `GENERATE_INTERVAL`, default 1 h), not by the POST; or the lane is not OPEN / the origin and destination node views are missing | Log `horizon roll` with `scheduled`/`failed`; `SELECT * FROM lane_views WHERE id = '<laneId>';`, `SELECT id, state FROM lane_schedules;` | Wait for the next roll or restart `cmd/api` (it rolls at boot); make sure node-topology's `NodeRegistered` and `LaneOpened` reached the topic and the consumer |
| `POST /lane-schedules` answers 404 `not-found` | The lane is unknown: no `LaneOpened` was consumed yet | `SELECT * FROM lane_views;` and the node-topology consumer logs | Publish or wait for the node-topology events; see [Integration](/docs/ecosystem/integration) |
| Pickup departures missing for a vendor node | The node is not an ACTIVE vendor fulfillment centre with a profile and cutoffs, or has no default outbound lane | `SELECT * FROM node_views WHERE id = '<nodeId>';` | Check the node-topology events (`NodeActivated`, `NodeOperationalProfileDeclared`) |
| ERROR `<consumer> handling failed; retrying the same message` repeating, then a message on `<topic>.dlq` | The handler error persisted for 5 attempts, or the payload can never be applied | Consumer log with `partition`, `offset`; read the `.dlq` message headers `x-dlq-error`, `x-dlq-source-topic`, `x-dlq-source-offset` | Fix the cause and produce the DLQ message value back to the source topic: replay is idempotent through `processed_events` |
| WARN `skipping a message that is not a valid CloudEvent` / `skipping an event that can never be applied` | The producer sent a non-CloudEvents 1.0 message or a payload the domain refuses | Log line (`type`, `event_id`) | Fix the producer; nothing to do here, the offset is committed |
| A booking moved to another pickup (`BookingMoved`) or failed (`BookingFailed`) | The locked pickup departure is unknown, closed, cancelled or full | Events on `retailnetwork.linehaul-transportation.events` | By design (ADR 0002 A14); add capacity with `PUT /departures/{departureId}/capacity` |
| Consumer topic has zero lag and no data | The producer has not written yet; topics are auto-created by the first writer | Kafka topic list | Normal early state |

## REST problem types

All errors are RFC 9457 `application/problem+json` with `type = https://retailnetwork.example/problems/<slug>` (`internal/adapters/inbound/http/errors.go`).

| Status | Slug | Cause | Fix |
| --- | --- | --- | --- |
| 400 | `malformed-request` | Body is not valid JSON, has unknown fields or could not be read | Send the documented body |
| 400 | `idempotency-key-required` | A POST without the `Idempotency-Key` header | Send a unique key per logical request |
| 400 | `idempotency-key-invalid` | Key not 1 to 128 printable ASCII characters | Send a UUID |
| 404 | `not-found` | Unknown departure, load, lane or schedule | Check the id; for a lane, see above |
| 409 | `illegal-transition` | The state does not allow the command (for example `POST /loads/{loadId}/dispatches` on an OPEN load, which must be CLOSED first) | Read the resource state; follow open, close, dispatch, receive |
| 409 | `concurrent-update` | Another request modified the aggregate (version guard) | Re-fetch and retry |
| 409 | `departure-full` | The departure has no remaining capacity | `change-capacity` or use another departure |
| 409 | `departure-closed` | A booking after `cutoffAt`, or the departure is no longer SCHEDULED | Pick a later departure |
| 409 | `lane-closed` | A departure was being scheduled on a lane that is not OPEN | Wait for node-topology's `LaneOpened` |
| 409 | `package-already-loaded` | The tracking number is already on an OPEN or CLOSED load | Use that load; the package can be loaded again once that load has departed |
| 409 | `load-over-capacity` | Adding the package exceeds the departure capacity | Raise capacity or open another load |
| 409 | `load-already-exists` | The departure already has a load | Use that load |
| 413 | `request-body-too-large` | Body above the limit | Send less data (a schedule has at most 100 slots) |
| 422 | `validation-failed` | A field is invalid (bad time, unknown mode) | Fix the field named in `detail` |
| 422 | `invalid-departure-times` | `departsLocalTime` is before `cutoffLocalTime` | Depart at or after the cutoff |
| 422 | `overlapping-slots` | Two slots overlap on the same weekday | Remove the overlap |
| 409 | `idempotency-key-reused` | Same key, different method, path or body | Use a new key for a different request |
| 400 | `invalid-limit`, `invalid-cursor`, `unknown-query-parameter` | `GET /departures` with a `limit` outside 1..500, a cursor that was not issued by the service, or a retired paging parameter (`after`, `offset`, `nextToken`) | Follow `nextCursor` as returned; send only the declared parameters |
| 500 | `internal-error` | Unexpected failure; 5xx are never stored for replay | Log line with `request_id` and `trace_id`; retry with the same key |

An equal request with the same key replays the first stored answer (2xx and 4xx) and adds `Idempotency-Replayed: true`; keys live 24 h.

## MCP and A2A

| Symptom | Cause | Fix |
| --- | --- | --- |
| MCP `tools/call` fails with `not-found` or `validation-failed` prefix | Unknown departure id; `limit` outside 1..1000, `from` after `to`, or a time that is not RFC 3339 | Fix the arguments |
| MCP request without a session id is rejected | Streamable HTTP needs `initialize` first and the `Mcp-Session-Id` header afterwards; send `Accept: application/json, text/event-stream` | See [Quickstart](/docs/overview/quickstart) |
| A2A answers `400 unknown-skill`, `malformed-request` or `invalid-argument` inside the message | The data part is not `{"skill": "...", "arguments": {...}}`, or has unknown arguments | Use `list_departures` or `get_departure` with documented arguments |
| A2A client dials the wrong host | The Agent Card advertises `A2A_AGENT_URL` | Set the URL clients can reach |
| MCP/A2A pod is up but data is stale or empty | They read the same database as `cmd/api`; nothing is written by them | Check `cmd/api` (consumers, horizon roll) |

## Logging in

There is nothing to log in to: REST, MCP and A2A are unauthenticated and rely on the cluster network boundary. A `401` or `403` therefore comes
from infrastructure in front of the service, not from this code.
