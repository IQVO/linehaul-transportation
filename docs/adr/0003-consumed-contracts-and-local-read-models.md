# ADR 0003: consumed contracts, local read models and relationships

## Status

Accepted (2026-10-23).

## Context

linehaul-transportation makes no synchronous call to a sibling context (`sync_calls: []`). Everything it needs from others is
event-fed and kept in local read models of its own database. The producer contracts are read from the producer repositories
(`apis/asyncapi.yaml`); this service vendors a tolerant copy of the fields it relies on (`apis/asyncapi.yaml`, receive
operations) and imports no sibling code.

## Decision

### Consumed events

| Topic | Consumer group (env, mandatory) | Types (full strings in ADR 0004) | Relationship |
| --- | --- | --- | --- |
| `retailnetwork.node-topology.events` | `NODE_TOPOLOGY_CONSUMER_GROUP` | NodeRegistered, NodeOperationalProfileDeclared, NodeActivated, NodeSuspended, NodeRetired, LaneOpened, LaneClosed | Conformist |
| `retailnetwork.order-sourcing.events` | `ORDER_SOURCING_CONSUMER_GROUP` | FulfillmentReleasedToNode, FulfillmentReassigned, FulfillmentLocked, FulfillmentCancelled | Customer/Supplier |
| `retailnetwork.shipping-and-tracking.events` | `SHIPPING_AND_TRACKING_CONSUMER_GROUP` | LabelIssued, LabelVoided | Conformist |

`LabelVoided` is added by gap G18 / contracts-delta E16. The fields relied on per message are in `apis/asyncapi.yaml`.

### Read models

- **NodeView** (`node_views`): `nodeId, nodeType, operator, timezone, status (PROPOSED|ACTIVE|SUSPENDED|RETIRED), profile
  (revision, hours, holidays, cutoffs, throughput cap), generation`. Fed by the five node events. Keeps the profile of the highest
  revision. Drives the pickup policy (ADR 0002 A7 to A11).
- **LaneView** (`lane_views`): `laneId, originNodeId, destinationNodeId, mode, nominalTransitMinutes, status`. Fed by LaneOpened
  and LaneClosed. Answers the `ErrLaneClosed` check and the default outbound lane.
- **ExpectedVolume** (`expected_volumes`): one row per `assignmentId`: `orderId, groupNo, nodeId, plannedDepartureId, bookedOn,
  state (RELEASED|BOOKED|FAILED|CANCELLED|UNKNOWN), releaseNo, trackingNumbers[]`. Fed by the order-sourcing events and the
  labels. `bookedOn` is the record that makes BookOnLock once-only and tells the rebooking which bookings sit on a cancelled
  departure.

None of the three is an aggregate; none enforces a rule on its source.

### Processing rules

- **Inbox.** Each message is claimed in `processed_events(event_id, consumer)` in the same transaction as its effect; a redelivery
  stops at the claim. The offset is committed after the transaction.
- **Dispatch on the full `type`**; a type the consumer has no handler for is ignored (not claimed in the inbox), never an error.
- **Tolerant decoding.** Unknown fields are ignored; a payload that cannot be decoded, or whose values the use case refuses
  (`ErrValidation`, for example an id that is not a valid id), is deterministic poison.
- **Poison and failures.** A message that is not a valid CloudEvent is logged and committed past. Poison (see above) goes at once to
  the dead-letter topic of the consumed topic, `<consumed topic>.dlq` (for example
  `retailnetwork.order-sourcing.events.dlq`), with `x-dlq-*` headers, and is committed. A domain refusal that is an outcome is not a
  failure: a lock that no departure can hold publishes BookingFailed and commits. A transient failure (the transaction rolled back)
  retries the same message with capped backoff (200 ms doubling to 5 s) up to five attempts, then the message is dead-lettered so
  one stuck message never wedges the partition (ADR 0007).
- **Consumer groups are mandatory** and read from the environment at boot; a missing one is a boot error naming the variable.
- **Ordering across topics** is not assumed: pickups are re-planned whenever any of registration, profile, activation or lane
  arrives, and by the horizon roll (ADR 0002 A6, A7).

### Published to consumers

delivery-promise and order-sourcing read DepartureScheduled, DepartureCapacityChanged, DepartureCancelled, BookingMoved and
BookingFailed; shipping-and-tracking reads LoadDeparted and LoadArrived; last-mile-delivery reads LoadArrived (ADR 0004). Any
context may call `GET /departures` (Open Host Service).

## Consequences

- The service keeps working when a sibling is down; its inputs are stale instead of missing.
- A new consumed type needs a receive message in `apis/asyncapi.yaml`, a line in ADR 0004 and a handler in the dispatcher.
