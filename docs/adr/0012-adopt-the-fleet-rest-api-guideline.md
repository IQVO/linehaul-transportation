# ADR 0012: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01). Amends [ADR 0006](./0006-http-adapter-idempotency-and-readiness.md) (routes, Idempotency-Key scope and reuse status) and
the list answer of [ADR 0002](./0002-aggregates-and-invariants.md) A24.

## Context

The fleet standardised its REST contracts (warehouse REST API guideline, enforced by the `rest-guideline` Spectral rule in the `api-lint`
job): URLs name things, every POST and PATCH is idempotent with one global `Idempotency-Key`, key reuse is `409`, and every collection is
keyset-paged as `{items,nextCursor}`. This service had `:verb` custom-method routes, an Idempotency-Key scoped by `(route, key)` whose
reuse was `422`, a key limit of 200 characters and an unpaged `{departures}` list capped by `limit`.

## Decision

- **Noun routes (R1, R2).** Hard cutover, no alias:

  | Old | New |
  | --- | --- |
  | `POST /lane-schedules/{laneId}:retire` | `POST /lane-schedules/{laneId}/retirements` |
  | `POST /departures/{departureId}:cancel` | `POST /departures/{departureId}/cancellations` |
  | `POST /departures/{departureId}:change-capacity` | `PUT /departures/{departureId}/capacity` |
  | `POST /loads/{loadId}:close` | `POST /loads/{loadId}/closures` |
  | `POST /loads/{loadId}:dispatch` | `POST /loads/{loadId}/dispatches` |
  | `POST /loads/{loadId}:receive` | `POST /loads/{loadId}/receipts` |

  Capacity is an attribute replace, so it is a `PUT` (idempotent by method, no middleware; a key sent to it is accepted and ignored).
  Status codes, bodies and `operationId`s do not change. A test (`TestRetiredRoutesAreGone`) asserts each old route answers 404 or 405.
- **Idempotency (R3-R6, R8).** All eight POST routes stay behind the middleware (none was unkeyed). The key is **global**: migration
  `0002_idempotency_global_key` makes `key` the primary key of `idempotency_keys` (older duplicates across routes are dropped, `route` is
  kept for diagnostics). The fingerprint stays `sha256(method, path, body)`, so the same key on another route or body is **409**
  `idempotency-key-reused` (was 422). A key must be 1 to 128 printable ASCII characters (`idempotency-key-invalid` otherwise, was 200
  characters of anything printable); a missing key is 400 `idempotency-key-required`. The replay header is `Idempotency-Replayed: true`
  (only on a replay). The key row, the aggregate and the outbox rows still commit in ONE Postgres transaction (SAVEPOINT design of ADR
  0006): this store already met the requirement, nothing else changed in it.
- **Pagination (R7).** `GET /departures` takes `limit` (1-500, default 100) and the opaque `cursor`, orders by `(cutoffAt, departureId)`
  with a keyset predicate `(cutoff_at, id) > ($1, $2)` in both the Postgres and the in-memory repository, and answers
  `{items,nextCursor}` (`nextCursor` absent on the last page). The cursor is the fleet `page` package format
  (`internal/adapters/inbound/http/page`, base64url JSON of the last sort key and id). `invalid-limit`, `invalid-cursor` and
  `unknown-query-parameter` (`page.Unknown`: `after`, `offset`, `nextToken` and any undeclared name) are 400. The use case `ListDeparturesPage`
  serves REST; `ListDepartures` (limit 1..1000, default 500, one complete bounded read) keeps serving the MCP tool and the A2A skill, whose own
  schemas are unchanged.
- **Exemptions.** None: the only collection GET is paginated.
- **Consumers in this repo.** The console remote (`web/src/api.ts`) reads `items` and follows `nextCursor` until it is absent or the
  caller's `limit` is reached. The e2e helper (`cmd/api/e2e_integration_test.go`) does the same. No outbound HTTP adapter exists.

## Consequences

Breaking by design (new URLs, new list envelope, global key, 409): this PR merges and deploys with the rest of the REST cutover set. A
consumer still sending `after`, `offset` or `nextToken` fails loudly with 400. No Schemathesis contract job exists in this repo, so no
`schemathesis.toml` is needed.
