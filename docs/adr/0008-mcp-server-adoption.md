# ADR 0008: a read-only MCP server

## Status

Accepted (2026-10-24).

## Context

Every other bounded context of the fleet that exposes an HTTP API also ships an
MCP server so that agents can read its data without a bespoke REST client.
linehaul-transportation had none. The service already has the read side behind
its REST adapter (`GET /departures` and `GET /departures/{departureId}`, the
`ListDepartures` and `GetDeparture` methods of the application layer's
`Service`), so an MCP adapter is another driving adapter over them, not new
domain. Its read surface is deliberately small: the Open Host Service reads of
apis/openapi.yaml are exactly the two departure queries -- everything else the
context serves is a write.

## Decision

- **A second driving adapter, a separate binary.** `internal/adapters/inbound/mcp`
  is built on the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`
  v1.8.0) and served over Streamable HTTP only (no stdio, no SSE) by `cmd/mcp`,
  mounted at `/mcp` and `/` with an open `/healthz`. It is a second deployable
  beside `cmd/api`; the Helm chart gets an `mcp` Deployment, Service and an
  opt-in Kong HTTPRoute (`/api/mcp/linehaul-transportation` rewritten to
  `/mcp`), all default-off.
- **Two read tools over the existing queries, nothing else.**
  `list_departures(origin?, destination?, from?, to?, limit?)` and
  `get_departure(departureId)` (capacity, booked and remaining packages, state,
  and the cancel/depart/arrive trail). Arguments and results are lowerCamelCase
  like the REST contract, and every optional argument carries `omitempty` in
  its input struct so the SDK publishes it as optional
  (`internal/architecture/mcp_optional_inputs_test.go`). Typed errors carry the
  REST problem slug (`not-found`, `validation-failed`) as the prefix of the
  tool error; anything untyped is reported as `internal-error` with the detail
  logged, never leaked. Every write stays REST-only.
- **Read-only is structural, not conventional.** The adapter's `Deps` holds
  only `DepartureReader`, a local interface whose two methods are exactly the
  REST reads; the adapter and `cmd/mcp` import no write seam (no unit of work,
  no outbox, no Kafka adapter, no REST adapter, no memory doubles), may name no
  `usecases` identifier outside the read surface, and `cmd/mcp`'s
  `usecases.Deps` literal sets only the departure repository.
  `internal/architecture/mcp_readonly_fitness_test.go` pins all of this
  (`TestMCPServerIsReadOnly`); `TestMCPServerIsReadOnly`'s detectors
  self-test in `TestMCPReadOnlySensorBites`. In the adapter package,
  `governance_test.go` pins the exact curated tool set (budget 2), the
  read-only/idempotent/closed-world annotations, the absence of write verbs in
  tool names and the whole advertised registry byte for byte
  (`testdata/tool_registry.golden.json`).
- **No sibling calls, no auth, no workers.** The server reads this context's
  own `departures` table only, calls no other context over REST or MCP, and
  has no auth layer (the fleet-wide revert of 2026-09-11). `cmd/mcp` never
  dials Kafka, never runs the outbox relay, the horizon roll or the three
  consumers: those stay in `cmd/api`.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode); `MCP_ADDR`
  defaults to `:8090` and `LOG_LEVEL` to `info`. Like `cmd/api`, the binary
  runs the idempotent embedded migrations on start (with
  `MIGRATIONS_DATABASE_URL` for a direct DSN behind PgBouncer) and retries the
  migrations and the first pool ping for up to ~30s, then refuses to boot. The
  integration test (`cmd/mcp/main_integration_test.go`, testcontainers) proves
  it migrates, reads what the write side persisted through both tools, and
  adds no outbox row.

## Consequences

- Agents can answer "which departures leave node n-a tomorrow, how much
  capacity remains on each, and did the 13:00 pickup actually leave" without
  REST glue.
- Adding a tool is a reviewed act: the tool budget, the curated list and the
  golden file all change together (an amendment of this ADR).
- Scheduling a lane, cancelling or re-capacitating a departure and the load
  lifecycle stay REST operations with `Idempotency-Key`.
