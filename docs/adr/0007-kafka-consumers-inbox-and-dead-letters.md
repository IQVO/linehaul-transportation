# ADR 0007: the Kafka consumers, the inbox and the dead letters

## Status

Accepted (2026-10-23).

## Context

ADR 0003 decides what linehaul-transportation consumes and how its local read models are fed. This ADR records how the three
consumers are built (`internal/adapters/inbound/kafka`) and run.

## Decision

1. **Three consumers, three groups.** One `Consumer` per producer topic: node-topology (`retailnetwork.node-topology.events`),
   order-sourcing (`retailnetwork.order-sourcing.events`), shipping-and-tracking (`retailnetwork.shipping-and-tracking.events`).
   Each runs under its own consumer group from a mandatory variable: `NODE_TOPOLOGY_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`,
   `SHIPPING_AND_TRACKING_CONSUMER_GROUP`. A missing or blank one is a boot error naming it, raised before the listener binds
   (`cmd/api/config.go`). The group id is never a literal in a consumer.
2. **At-least-once with an atomic effect.** The run loop fetches a message, handles it and commits its offset only after the handler
   returned nil (`FetchMessage` + `CommitMessages`). A handler is a `usecases.Service.Handle*` method: it claims the CloudEvents id in
   `processed_events(consumer, event_id)`, applies the effect and appends the events it raises, all in one unit of work. A repeated id
   stops at the claim.
3. **Dispatch on the full `type`.** `Consumer.Handlers` is keyed by the whole type string (for example
   `com.retailnetwork.logistics.node-topology.node.NodeActivated`); the strings are restated from the producers' AsyncAPI
   documents, never imported. A type without a handler is ignored.
4. **Tolerant decoding.** Each handler decodes `data` into a private struct that holds only the fields this service relies on;
   unknown fields are ignored.
5. **Failures.** A message that is not a CloudEvent is logged and committed past. A payload that cannot be decoded or whose values
   the domain refuses (`ErrValidation`) is poison: it is published at once to `<consumed topic>.dlq` with the original key, value and
   headers plus `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error`, `x-dlq-failed-at`, and the
   offset is committed. A transient failure retries the same message with capped exponential backoff (200 ms doubling to 5 s) and
   after five attempts the message is dead-lettered the same way. Dead-letter publishing is guarded against shutdown
   (`TestDeadLetterCallsGuardCancellation`): a cancelled context never dead-letters.
6. **Ordering across topics is not assumed.** The pickups of a node are re-planned whenever its registration, profile, activation or a
   lane arrives, and by the horizon roll (ADR 0002 A6, A7).

## Consequences

- One stuck message delays its partition by at most five attempts, never forever.
- The consumers are proved against a real broker and Postgres by
  `internal/adapters/inbound/kafka/consumers_integration_test.go` (effect, redelivery, dead letter) and by the real-wiring test
  `cmd/api/e2e_integration_test.go`; the handler mapping and the failure paths by `handlers_test.go` and `consume_loop_test.go`.
