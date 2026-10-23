---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Route -> use case, kept in sync with `apis/openapi.yaml` (routes are registered in `internal/adapters/inbound/http/server.go` `NewRouter`,
handlers in `handlers.go`, wired in `cmd/api/main.go`):

- `POST /lane-schedules` -> `DefineLaneSchedule` (201 created, 200 replaced)
- `POST /lane-schedules/{laneId}:retire` -> `RetireLaneSchedule`
- `GET /departures?origin=&destination=&from=&to=&limit=` -> `ListDepartures` (`{"departures":[...]}`)
- `GET /departures/{departureId}` -> `GetDeparture`
- `POST /departures/{departureId}:cancel` -> `CancelDeparture` (body `{"reason"}`)
- `POST /departures/{departureId}:change-capacity` -> `ChangeDepartureCapacity` (body `{"capacityPackages"}`)
- `POST /loads` -> `OpenLoad` (201, body `{"departureId"}`)
- `POST /loads/{loadId}/packages` -> `AddPackage` (body `{"trackingNumber"}`)
- `POST /loads/{loadId}:close` -> `CloseLoad`, `:dispatch` -> `DispatchLoad`, `:receive` -> `ReceiveLoad`
- `GET /healthz` (liveness), `GET /readyz` (readiness): not business operations.

## Conventions

- **Errors** are RFC 9457 `application/problem+json`: `type` is `https://retailnetwork.example/problems/<slug>` (catalogue in
  `internal/adapters/inbound/http/errors.go`), plus `title`, `status`, `detail`, `instance`. Shared: `not-found` 404, `illegal-transition` 409,
  `concurrent-update` 409, `validation-failed` 422; one slug per model `Err...` (`departure-full`, `departure-closed`, `lane-closed`,
  `package-already-loaded`, `load-over-capacity`, `load-already-exists`, `invalid-departure-times`, `overlapping-slots`); an unmapped error is
  an opaque 500 `internal-error`. A new `Err` needs a catalogue row, an OpenAPI mention and a case in `handlers_test.go`.
- **Idempotency-Key** is required on all nine state-changing routes (`idempotency.go`): scope `(route, key)`, fingerprint
  `sha256(method, path, body)`; same key + same request replays the first response (header `Idempotency-Replayed: true`), same key + other
  request is 422 `idempotency-key-reused`, missing key 400 `idempotency-key-required`, a key over 200 characters 400 `idempotency-key-invalid`,
  keys live 24 h in `idempotency_keys`. A new state-changing route registers through `Server.keyed`, never `r.Post`.
- **NUL** (U+0000) is rejected before anything else (`nul.go`): 404 `resource-not-found` in the path, 400 `malformed-request` elsewhere. A body
  above 64 KiB is 413 `request-body-too-large`.
- Bodies are decoded strictly (`decodeBody`): unknown fields are 400 `malformed-request`; a value the domain refuses is 422.
- **Handlers hold no business logic**: decode, call a use case, encode the view.
- **Auth**: none. REST is unauthenticated, as in the rest of the network (`internal/architecture/fitness_test.go`
  `TestNoAuthMiddlewareReintroduced` keeps it so).
- Unit tests (`handlers_test.go`) run the real use cases over the memory test doubles; the real middleware is proven on Postgres in
  `idempotency_integration_test.go`, the whole binary in `cmd/api/e2e_integration_test.go`.
