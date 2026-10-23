---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

linehaul-transportation PUBLISHES ten events (seven integration events to `retailnetwork.linehaul-transportation.events`, three analytics-only
events to `retailnetwork.linehaul-transportation.analytics`) and CONSUMES thirteen event types from three producers (ADR 0003, ADR 0007).
Never produce or consume a `warehouse.*` topic.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces is a CloudEvents 1.0 event in structured content mode. This is a hard rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write, no envelope toggle env var.
  `internal/architecture/fitness_test.go`'s `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build and validate the envelope with `github.com/cloudevents/sdk-go/v2/event` through ONE helper package,
  `internal/adapters/kafka/cloudevents` (`New`, `Decode`, `ContentTypeHeader`). Transport stays `segmentio/kafka-go`; no hand-rolled CloudEvent
  structs. The only producer-side caller is the encoder `internal/adapters/kafka/wire/wire.go`; consumers decode with `Decode`.
- Every message carries the Kafka header `content-type: application/cloudevents+json; charset=UTF-8`. Message key = the subject (aggregate id),
  `kafkago.Hash{}` balancer (`internal/adapters/outbound/kafka/relay_sink.go`).
- Attributes: `specversion=1.0`; `id` (UUID minted ONCE per event at outbox append and stored in `outbox_events.event_id`, so a redelivery
  carries the same id); `source=/retailnetwork/linehaul-transportation`; `type`; `subject` (`departureId` or `loadId`, for `BookingMoved` the
  `toDepartureId`; never empty); `time` (UTC); `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:linehaul-transportation:<events|analytics>:<EventName>:v1`. No custom extension attributes without an ADR.
- `type` = `com.retailnetwork.logistics.linehaul-transportation.<entity>.<EventName>` with entity `departure` or `load`. A breaking payload change
  is a new `.v2` dataschema, never a mutation of v1; evolution is additive only.
- Payloads follow the wire conventions of contracts-delta: ids are strings, instants RFC 3339 UTC, durations integers ending in `Minutes`, enums
  UPPER_SNAKE, optional fields omitted (never null), arrays always present.
- Tests: a golden exact-JSON test per published type (`internal/adapters/kafka/wire/wire_test.go`, files in `testdata/`), the
  catalogue-vs-contract sensor (`TestEventCatalogueMatchesContract`), the Kafka proof against a real broker
  (`internal/adapters/outbound/outbox/relay_kafka_integration_test.go`) and the real-wiring test (`cmd/api/e2e_integration_test.go`).

### Published types

`subject` and Kafka key is the aggregate id; `dataschema` is `urn:retailnetwork:linehaul-transportation:<stream>:<Event>:v1`.

| `type` (prefix `com.retailnetwork.logistics.linehaul-transportation.`) | Stream | `subject` |
| --- | --- | --- |
| `departure.DepartureScheduled` | events | departureId |
| `departure.DepartureCapacityChanged` | events | departureId |
| `departure.DepartureCancelled` | events | departureId |
| `departure.BookingMoved` | events | toDepartureId |
| `departure.BookingFailed` | events | departureId |
| `load.LoadDeparted` | events | loadId |
| `load.LoadArrived` | events | loadId |
| `departure.DepartureDeparted` | analytics | departureId |
| `departure.DepartureArrived` | analytics | departureId |
| `load.LoadClosed` | analytics | loadId |

### Consumed types

Dispatch on the FULL `type`; the strings are restated from the producers' AsyncAPI in `internal/adapters/inbound/kafka/handlers.go`.

| Topic | Consumer group variable | Types (prefix `com.retailnetwork.`) |
| --- | --- | --- |
| `retailnetwork.node-topology.events` | `NODE_TOPOLOGY_CONSUMER_GROUP` | `logistics.node-topology.node.NodeRegistered`, `...node.NodeOperationalProfileDeclared`, `...node.NodeActivated`, `...node.NodeSuspended`, `...node.NodeRetired`, `...lane.LaneOpened`, `...lane.LaneClosed` |
| `retailnetwork.order-sourcing.events` | `ORDER_SOURCING_CONSUMER_GROUP` | `commerce.order-sourcing.assignment.FulfillmentReleasedToNode`, `...FulfillmentReassigned`, `...FulfillmentLocked`, `...FulfillmentCancelled` |
| `retailnetwork.shipping-and-tracking.events` | `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | `logistics.shipping-and-tracking.label.LabelIssued`, `...label.LabelVoided` |

A consumer is tolerant: it decodes only the fields it relies on and ignores the rest. Before changing a mirror struct re-read the producer's
AsyncAPI (`git -C ~/warehouse-systems/<producer> show origin/feature/service:apis/asyncapi.yaml`).

## Transactional outbox

A use case saves the aggregates and appends the encoded events in ONE Postgres transaction (`ports.EventOutbox`,
`internal/adapters/outbound/postgres/event_outbox.go`). The relay (`internal/adapters/outbound/outbox/relay.go`, always on, started by
`cmd/api/main.go`) drains `outbox_events` in id order, at-least-once, to the topic each row names. The cap `OUTBOX_MAX_ATTEMPTS` (default 20,
0 = unlimited) excludes a poison row; re-arm it with `UPDATE outbox_events SET attempts = 0 WHERE id = ...` (ADR 0005). The first write to a
topic that does not exist yet can fail once with an unknown-topic error: the relay retries it with the same id. The caller of `NewOutboxRelay`
must pass `WithMaxAttempts(...)` inline (`TestOutboxRelaysPassAttemptCap`).

## Consumers and the consumer group ids

Rules of `.claude/rules/fleet/kafka-testing-and-consumers.md` apply. Here: one consumer per producer topic (`internal/adapters/inbound/kafka`),
each under its own mandatory group variable (a boot error naming it, `cmd/api/config.go`; never a literal,
`TestKafkaConsumerGroupNeverHardcodedInline`); an inbox row `processed_events(consumer, event_id)` claimed in the same transaction as the
effect; offsets committed after success only; poison and exhausted retries go to `<consumed topic>.dlq` (ADR 0007); dead-letter calls guard
cancellation (`TestDeadLetterCallsGuardCancellation`).
