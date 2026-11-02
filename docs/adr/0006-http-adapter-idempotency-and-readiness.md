# ADR 0006: the HTTP adapter, Idempotency-Key, problem types and readiness

## Status

Accepted (2026-10-23).

## Context

`apis/openapi.yaml` fixes the operations; the model conventions fix the shared problem types; contracts-delta fixes
`Idempotency-Key` on every state-changing route (table `idempotency_keys`, 24 h, replay of the first response). The fleet requires
a dependency-aware `/readyz`, a NUL guard and a graceful shutdown.

## Decision

1. **Router.** `internal/adapters/inbound/http` is a chi router over `usecases.Service`. Middleware: `rejectNUL` on every request,
   then the route; a state-changing route is registered through `Server.keyed`, which puts the Idempotency-Key middleware in front
   of it. The adapter imports only the application layer and the domain; `cmd/api` injects the service, the readiness gate and the
   transaction binder (`pgtx.With`) and never imports the memory or repocontract packages.
2. **Operations.** `POST /lane-schedules` (201 created, 200 replaced), `POST /lane-schedules/{laneId}/retirements`,
   `GET /departures?origin=&destination=&from=&to=&limit=` (`{"items":[...],"nextCursor":"..."}`, keyset-paged, ADR 0012), `GET /departures/{departureId}`,
   `POST /departures/{departureId}/cancellations`, `PUT /departures/{departureId}/capacity` (idempotent by method, unkeyed), `POST /loads` (201),
   `POST /loads/{loadId}/packages`, `POST /loads/{loadId}/closures`, `/dispatches`, `/receipts`; plus the probes `GET /healthz` and
   `GET /readyz`. The bodies are the `views` types.
3. **Problems (RFC 9457, `application/problem+json`).** `type` is `https://retailnetwork.example/problems/<slug>`, plus `title`,
   `status`, `detail` and `instance`. Shared: `not-found` 404, `illegal-transition` 409, `concurrent-update` 409, `validation-failed`
   422. One slug per model Err, the kebab-case of the name without `Err`: 409 `departure-full`, `departure-closed`, `lane-closed`,
   `package-already-loaded`, `load-over-capacity`, `load-already-exists`; 422 `invalid-departure-times`, `overlapping-slots`.
   An unmapped error is an opaque 500 `internal-error` whose cause is logged. Adapter problems: 400 `malformed-request` (bad JSON,
   unknown field, trailing data, NUL in the query or body), 404 `resource-not-found` (NUL in the path), 413
   `request-body-too-large` (64 KiB), 400 `idempotency-key-required`, 400 `idempotency-key-invalid` (not 1 to 128 printable ASCII
   characters), 409 `idempotency-key-reused`, 400 `invalid-limit`, `invalid-cursor` and `unknown-query-parameter`, 503 `dependency-unavailable`. The catalogue is
   `internal/adapters/inbound/http/errors.go`.
4. **NUL.** A NUL (U+0000) cannot be stored in Postgres text and would abort the transaction. `rejectNUL` answers before the
   Idempotency middleware records anything.
5. **Idempotency-Key.** The key is global (ADR 0012; the `route` column is kept for diagnostics only); the request
   fingerprint is `sha256(method, path, body)`, so the same key on another departure is a different request, hence key reuse. The same key and
   fingerprint replays the first status and body (header `Idempotency-Replayed: true`); the same key with another fingerprint is
   **409** `idempotency-key-reused`. The key row and the handler's writes share one transaction: the handler runs in a SAVEPOINT
   bound into its context, a 2xx keeps its writes, a 4xx rolls them back to the savepoint but is stored (a repeat answers the same),
   a 5xx or a panic rolls everything back and is never stored. Concurrent requests with one key race on
   `INSERT ... ON CONFLICT DO NOTHING`; the loser blocks until the winner resolves and then replays. Keys live 24 h: every keyed
   write first deletes the expired rows (index on `created_at`), so an expired key is a new key.
6. **Probes.** `/healthz` is liveness: 200 `{"status":"ok"}` while the process serves. `/readyz` is 200 `{"status":"ready"}` only
   while the drain gate is open AND Postgres answers a ping AND a Kafka broker answers a metadata request. The Kafka request names
   NO topics (an empty, non-nil list), so it never creates a topic and does not need the output topic to exist; it dials a fresh
   connection on every check, because cached metadata would keep answering ready after the broker is gone. Verdicts are cached 3 s
   and bounded 2 s. While draining: 503 `{"status":"not_ready"}`; while a dependency fails: 503 `dependency-unavailable` naming it.
7. **Composition root.** `cmd/api` `run(ctx)` validates the environment before anything opens (`DATABASE_URL`, `KAFKA_BROKERS` and the
   three `*_CONSUMER_GROUP` variables: a boot error names the variable), runs the embedded migrations (retried for up to 30 s),
   builds the service and starts the workers (outbox relay, horizon roll, three consumers) and the listener. Shutdown order:
   `/readyz` flips to 503, wait `SHUTDOWN_DRAIN_DELAY` (default 5 s), drain HTTP (10 s), stop and await the workers (10 s), close
   the pool and the Kafka clients. If a worker returns on its own the process exits non-zero.

## Consequences

- A client retrying a timed-out write with the same key never duplicates a load, a schedule or an event.
- A 4xx is replayed too: after fixing the cause the client sends a new key.
- The real middleware is proven on Postgres by `internal/adapters/inbound/http/idempotency_integration_test.go`.
