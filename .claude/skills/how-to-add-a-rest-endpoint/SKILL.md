---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the hexagonal order (domain invariant, use case, handler, apis/openapi.yaml, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the invariant it relies on validates nothing and gets bypassed.

The worked example is `POST /loads/{loadId}:close`: `internal/domain/load/load.go` `Close` -> `internal/application/usecases/loads.go`
`CloseLoad` -> `internal/adapters/inbound/http/handlers.go` `handleCloseLoad`. The read side example is `GET /departures/{departureId}`
(`usecases/departures.go` `GetDeparture` -> `handleGetDeparture`).

## 1. Domain first

Does the rule already exist in `internal/domain/<departure|schedule|load>/`? A handler holds no business logic. A new rule goes into the
aggregate with a table-driven test in the same package (`load_test.go`, ...) and a named `Err...` variable. Pure Go: no I/O, `now` and ids
passed in. Then re-run the mutation gate for that package (`gremlins unleash ./internal/domain/<pkg>`) and keep its threshold in
`.gremlins.yaml` strictly below the measurement.

## 2. Application: the use case

Add it to `internal/application/usecases/` (`schedules.go`, `departures.go`, `loads.go` for commands and reads). A command runs in
`s.write`: load, domain call, `Save` and the outbox append in ONE `ports.UnitOfWork`, with a retry on `ports.ErrConcurrentUpdate`. Return a
view from `internal/application/views`. If a new repository method is needed, add it to `internal/application/ports/ports.go` (interfaces
only), implement it in `internal/adapters/outbound/postgres/` AND `internal/adapters/outbound/memory/`, and extend the shared suite in
`internal/adapters/outbound/repocontract/` so both run it. Unit-test the use case in `internal/application/usecases/*_test.go`.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- add the request body type next to the others in `handlers.go` (strict decoding via `decodeBody`: unknown fields are 400);
- add the handler there and register it in `server.go` `NewRouter`: a state-changing route with
  `s.keyed(r, http.MethodPost, pattern, handler)` so it sits behind the Idempotency-Key middleware, a read with `r.Get`;
- map a new domain `Err` in `errors.go` `problemCatalogue` (kebab-case slug, the status the OpenAPI documents), more specific errors before
  `ErrValidation` and `ErrIllegalTransition`;
- `cmd/api/main.go` already passes the whole `usecases.Service`, so no further wiring is needed.

## 4. Contract: apis/openapi.yaml

Describe the operation, its `Idempotency-Key` parameter (writes), the success body and every problem it can return, then lint:
`npx --yes @stoplight/spectral-cli lint apis/openapi.yaml --ruleset .spectral.yaml` (the `api-lint` CI job runs the same). Keep
`.claude/rules/rest-api.md` and the README table in step.

## 5. Behaviour

Add the happy path and each refusal to a scenario in `features/*.feature` (steps in `features/steps_*_test.go` drive the use cases; refusals
are named by the problem slug in `features/world_test.go` `slugs`) and a case to `internal/adapters/inbound/http/handlers_test.go`
(`TestEveryOperationAnswersItsContractStatus`). The real middleware and Postgres are proven by `idempotency_integration_test.go`, the whole
binary by `cmd/api/e2e_integration_test.go`.

## 6. Verify before opening the PR

`make check`, `make coverage`, `go test -tags=integration ./internal/adapters/inbound/http/ ./cmd/... -race -count=1`, and the spectral lint
above.
