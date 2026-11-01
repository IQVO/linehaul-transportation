---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event in this service: CloudEvents 1.0 type naming, AsyncAPI, the type catalogue ADR, the encoder, the transactional outbox, the consumers. Use when touching internal/adapters kafka or outbox code, a domain event, or apis/asyncapi*.yaml.
---

# How to add an integration event

linehaul-transportation publishes ten events and consumes thirteen types from three producers (ADR 0003, 0004, 0007). The envelope is
CloudEvents 1.0 only; read `.claude/rules/integration-events.md` and `.claude/rules/fleet/cloudevents.md` first. The worked example is
`LoadArrived`.

## Publishing a new event

1. **Domain event.** A typed struct in `internal/domain/<pkg>/events.go` (`load.LoadArrived`): embeds `shared.Header`, fields exactly the
   model's `data` list, methods `EventName()`, `Entity()` (`departure` or `load`, lowercase, no separators) and `SubjectID()` (the aggregate
   id). The aggregate command (`Load.Receive`) returns it; `AggregateVersion()` is the version it carries.
2. **Contract, one commit.** Add the message and its `data` schema to `apis/asyncapi.yaml`, AND one
   `type: com.retailnetwork.logistics.linehaul-transportation.<entity>.<Event>` line with its stream to
   `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`. `TestEventCatalogueMatchesContract` fails when the two disagree, so the push
   fails when they are not in the same commit. Lint:
   `npx --yes @stoplight/spectral-cli lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml`. Evolution is additive only; a breaking
   change is a new `:v2` dataschema.
3. **Encoder.** In `internal/adapters/kafka/wire/wire.go` add the payload struct (camelCase json tags, instants through `utc`, arrays through
   `strs` so they are never null) and a `case` in `dataOf`. An analytics-only event also goes into `analyticsOnly` (it is then written to
   `retailnetwork.linehaul-transportation.analytics` and never to the events topic). The envelope itself is built by
   `internal/adapters/kafka/cloudevents` only; never hand-build one. Add the event to `cases` in `wire_test.go` and generate its golden file
   with `UPDATE_GOLDEN=1 go test ./internal/adapters/kafka/wire/` (read the diff before committing).
4. **Raise it from the use case** inside the same `ports.UnitOfWork` as the `Save` (`usecases.Service.write` appends the events it is given to
   the outbox in that transaction): the aggregate row and the outbox row commit or roll back together. Never publish after the commit and never
   call Kafka from a use case.
5. **Nothing else.** The relay (`internal/adapters/outbound/outbox/relay.go`) already drains every row to the topic the row names.
6. **Prove it.** `go test -tags=integration ./internal/adapters/outbound/outbox/ ./cmd/... -race -count=1` reads the events back from a real
   broker; extend `cmd/api/e2e_integration_test.go` when the event is part of a flow.

## Consuming a new event from a sibling context

1. Re-read the producer's AsyncAPI: `git -C ~/warehouse-systems/<producer> show origin/feature/service:apis/asyncapi.yaml`. Use its exact
   `type`, topic and field names; report a mismatch instead of silently adapting.
2. Add the full type string and a tolerant payload struct (only the fields you rely on) to `internal/adapters/inbound/kafka/handlers.go`, and a
   `Handle...` use case in `internal/application/usecases/consumers.go` that runs inside `s.consume(ctx, consumer, eventID, ...)` so the
   CloudEvents id is claimed in `processed_events` in the same transaction as the effect.
3. Register the type in the producer's handler map (`NodeTopologyHandlers`, `OrderSourcingHandlers`, `ShippingHandlers`) or, for a new producer,
   a new consumer in `NewConsumers` with its own mandatory `*_CONSUMER_GROUP` variable in `cmd/api/config.go` (a boot error naming it), listed in
   `docs/docs/overview/runtime.md`, the README and ADR 0003. A group id is never a string literal (`TestKafkaConsumerGroupNeverHardcodedInline`).
4. Add the receive operation and the fields you rely on to `apis/asyncapi.yaml`, and update ADR 0003.
5. Test: a case in `internal/adapters/inbound/kafka/handlers_test.go` (decoding and effect over the memory doubles), the use case in
   `internal/application/usecases/consumers_test.go`, a scenario in `features/` if it changes behaviour, and
   `consumers_integration_test.go` / `cmd/api/e2e_integration_test.go` for the real broker.

## Verify before opening the PR

`make check`, `go test ./internal/architecture/ -count=1` (catalogue, CloudEvents-only, relay cap, dead-letter cancellation), the spectral lint of
the AsyncAPI, and the integration command above.
