# ADR 0005: the outbox relay, its attempt cap and the CloudEvents encoder

## Status

Accepted (2026-10-23).

## Context

Every integration and analytics event is published through the transactional outbox: a use case saves the aggregates and
appends the encoded events in ONE Postgres transaction (`ports.UnitOfWork`, `ports.EventOutbox`). Something has to drain the
rows to Kafka, and the harness (`TestOutboxRelaysPassAttemptCap`) forbids a relay that one poison row can block forever.

## Decision

1. **Relay.** `internal/adapters/outbound/outbox` holds `NewOutboxRelay(store, sink, logger, opts...)`. A pass (`RelayOnce`) claims
   a batch of unpublished rows in id order, sends them one at a time and marks each row published only after the send returned
   nil. `Run` repeats until its context is cancelled: a full batch is followed at once by another pass, anything else sleeps
   `OUTBOX_RELAY_INTERVAL` (default 1 s); a failed pass is logged and retried after the interval. It never dials at construction.
2. **Store.** `postgres.OutboxRepo` claims with `FOR UPDATE SKIP LOCKED` (two replicas never claim the same row), stops at the
   first send failure (a later event of an aggregate never overtakes an earlier one that is being retried), records `attempts`
   and `last_error` on the failed row and commits the rows already sent.
3. **At-least-once.** A crash between the send and the `UPDATE` republishes that row with the SAME CloudEvents `id`, minted once at
   append time and stored in `outbox_events.event_id`. Consumers deduplicate on the id.
4. **Attempt cap.** `WithMaxAttempts(n)` (`OUTBOX_MAX_ATTEMPTS`, default 20, `0` = unlimited) excludes a row from future claims
   once it failed `n` times. The row stays in `outbox_events` with `attempts` and `last_error`; the failure that reaches the cap is
   logged once at ERROR with the row id, topic, type and key. An operator re-arms it with
   `UPDATE outbox_events SET attempts = 0 WHERE id = ...`. **Trade-off:** after the cap a later event of the same aggregate can reach
   the topic before the capped one. `cmd/api` passes `WithMaxAttempts(...)` inline.
5. **Sink.** `outboundkafka.RelaySink` writes each message to the topic the row names with one `kafka.Writer` that has NO fixed topic,
   `RequireAll` acks, the `Hash` balancer on the key, a 10 ms batch timeout and `AllowAutoTopicCreation`.
6. **Two topics.** `outbox_events.topic` is `retailnetwork.linehaul-transportation.events` for integration events and
   `retailnetwork.linehaul-transportation.analytics` for the analytics-only ones (`DepartureDeparted`, `DepartureArrived`,
   `LoadClosed`). An analytics-only event is never written to the events topic and the reverse (ADR 0004; `wire.Stream`).
7. **First write to a new topic.** The broker creates the topic on the first write, and that first write can fail with
   `UnknownTopicOrPartition`. It is an ordinary send failure: the row keeps its place and the next pass publishes the same id.
   `/readyz` therefore never asks for topics (ADR 0006).
8. **Encoder.** `internal/adapters/kafka/wire.Encoder` turns each domain event into the outbox message, building every envelope
   through `internal/adapters/kafka/cloudevents`: `type` `com.retailnetwork.logistics.linehaul-transportation.<entity>.<Event>` with
   entity `departure` or `load`, `source` `/retailnetwork/linehaul-transportation`, `subject` and Kafka key the aggregate id (for
   `BookingMoved` the `toDepartureId`), `dataschema` `urn:retailnetwork:linehaul-transportation:<stream>:<Event>:v1`, header
   `content-type: application/cloudevents+json; charset=UTF-8`. The payloads are those of `apis/asyncapi.yaml`, pinned by golden
   exact-JSON tests (`internal/adapters/kafka/wire/testdata/*.golden.json`).

## Consequences

- A poison row costs at most `OUTBOX_MAX_ATTEMPTS` passes of delay for the rows behind it, never a permanent block.
- A write is accepted while Kafka is down; the rows wait in the table and publish when the broker returns (proved by the
  Kafka-stopped subtest of `cmd/api/e2e_integration_test.go`).
- Postgres integration tests prove claiming, failure recording, the cap and re-arming; a Kafka integration test proves that the
  outbox rows become exactly the CloudEvents on the topic.
