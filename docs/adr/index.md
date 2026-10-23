# Architecture decision records

The decisions of `linehaul-transportation`, in the order they were taken. The ADR
bodies are immutable; a changed decision gets a new ADR that supersedes the old
one. The *Status* column is copied from each ADR's own `## Status` section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-linehaul-transportation-bounded-context.md) | Linehaul Transportation is a bounded context of the retail network | Accepted (bootstrap) |
| [0002](./0002-aggregates-and-invariants.md) | the Departure, LaneSchedule and Load aggregates, their invariants and the assumptions the model left open | Accepted (2026-10-23) |
| [0003](./0003-consumed-contracts-and-local-read-models.md) | consumed contracts, local read models and relationships | Accepted (2026-10-23) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-23) |
| [0005](./0005-outbox-relay-and-attempt-cap.md) | the outbox relay, its attempt cap and the CloudEvents encoder | Accepted (2026-10-23) |
| [0006](./0006-http-adapter-idempotency-and-readiness.md) | the HTTP adapter, Idempotency-Key, problem types and readiness | Accepted (2026-10-23) |
| [0007](./0007-kafka-consumers-inbox-and-dead-letters.md) | the Kafka consumers, the inbox and the dead letters | Accepted (2026-10-23) |
