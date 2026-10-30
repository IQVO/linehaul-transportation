# ADR 0011: A2A inbound adapter

## Status

Accepted (2026-10-30).

## Context

The fleet's contexts are reachable by agents through MCP ([ADR 0008](./0008-mcp-server-adoption.md)) and exchange facts as CloudEvents on
Kafka. A third transport, Agent2Agent (A2A), carries the *delegation of a goal* from one agent to another: "which departures leave node
n-a tomorrow and how full are they" is a goal a sibling agent can hand to this context without knowing its REST or MCP contract. The three
transports do not overlap: Kafka + CloudEvents carries FACTS, MCP carries an agent's TOOLS, A2A carries DELEGATION only, never data sync.

This context's MCP charter is entirely read-only (ADR 0008: `list_departures`, `get_departure`; every write stays REST with
`Idempotency-Key`). An A2A skill may change state only if the same write is already offered over MCP, so the A2A agent is read-only too.
Delegation must never widen what the context exposes.

## Decision

- **A third driving adapter, a separate binary.** `internal/adapters/inbound/a2a` is built on the official A2A Go SDK
  (`github.com/a2aproject/a2a-go/v2` v2.7.0) and served by `cmd/a2a` as JSON-RPC over HTTP: the Agent Card at
  `/.well-known/agent-card.json`, the JSON-RPC endpoint at both `/` and `/a2a`, an open `GET /healthz`. It is a third deployable beside
  `cmd/api` and `cmd/mcp`; the Helm chart gets an `a2a` Deployment, Service and an opt-in Kong HTTPRoute
  (`/api/a2a/linehaul-transportation` rewritten to `/`), all default-off (`a2a.enabled`).
- **Message-only agent, two curated read skills.** The skills are the goal-shaped twins of the MCP tools, with the same ids and the same
  use cases (`ListDepartures`, `GetDeparture`): `list_departures(origin?, destination?, from?, to?, limit?)` and
  `get_departure(departureId)`. A request is `{"skill":"<id>","arguments":{...}}` in a Data part (or a Text part holding the same JSON);
  the answer is one agent Message with a Data part (`{skill, data | problem}`) and a one-line Text summary. Nothing here is one skill per
  endpoint, and adding a skill is an amendment of this ADR.
- **Strict input, in-band problems.** Arguments decode with `DisallowUnknownFields` and trailing data is refused, so a misspelled name
  never silently falls back to a default. Domain rejections and bad requests are ANSWERED in the message envelope as RFC 7807-shaped
  `problem`s, never as JSON-RPC errors, with the REST adapter's classification and `type` namespace
  (`https://retailnetwork.example/problems/`): 404 `not-found`, 422 `validation-failed` (the REST class for a rejected query), 500
  `internal-error`; adapter problems are 400 `malformed-request`, `unknown-skill` and `invalid-argument`. The REST 409 classes belong to
  writes and are unreachable. Not-found and internal details are adapter-owned text, so neither the shared error wording nor
  infrastructure detail (DSNs, SQL) reaches a caller; the cause of a 500 is logged. One OpenTelemetry span per skill carries the
  `a2a.skill` attribute.
- **Read-only is structural.** The adapter's `Deps` holds `DepartureReader`, a local interface with exactly the two REST reads; `cmd/a2a`'s
  `usecases.Deps` literal sets only the departure repository. `internal/architecture/a2a_fitness_test.go` pins the dependency rule (the
  adapter depends only on application and domain; nothing depends on it; no outbound A2A client; no sibling-context call) and the
  read-only allowlist of `usecases` identifiers, with a self-test that the sensor bites (`TestA2AAdapterDependencyRule`,
  `TestA2AAdapterIsReadOnly`, `TestA2AReadOnlySensorBites`).
- **No auth, no workers, no Kafka.** The endpoints are unauthenticated by the fleet-wide decision (2026-09-11); the card declares no security
  schemes. `cmd/a2a` never dials Kafka, runs no outbox relay, no horizon roll and no consumer, so it has no `KAFKA_BROKERS` or
  `EVENT_PUBLISHER`; a delegated write would have to go through the same use case and transactional outbox, but there is none.
- **Boot.** `DATABASE_URL` is mandatory (real Postgres only) and so is `A2A_AGENT_URL`, the absolute http(s) URL advertised on the card
  (the process refuses to boot otherwise: a card nobody can dial is worse than none). `A2A_ADDR` defaults to `:8093` (8080 api, 8090 mcp).
  The chart defaults `A2A_AGENT_URL` to `http://<release>-a2a.<namespace>.svc.cluster.local:8093/`, overridable with `a2a.agentUrl`. Like
  `cmd/api` and `cmd/mcp` the binary runs the idempotent embedded migrations on start (`MIGRATIONS_DATABASE_URL` for a direct DSN).

## Consequences

- A delegating agent can ask for the departure horizon or one departure's status without a REST or MCP client for this context.
- The chart tests (`render_test.sh`, `test_service_selectors.py`) assert the a2a component: command `/app/a2a`, the mandatory env, no
  Kafka/relay/consumer env, `component=a2a` selectors, the route rewrite and its absence by default.
- Scheduling lanes, cancelling or re-capacitating a departure and the load lifecycle stay REST operations; there is no outbound A2A client.
- No integration test with outbox rows is needed: the agent has no write skill.
