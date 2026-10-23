# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-23).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (retail-network-docs `model/model.yaml` `conventions.cloudevents`; the repo
rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue of every type
linehaul-transportation publishes and the list of the types it consumes.
`TestEventCatalogueMatchesContract` checks the published list against
`apis/asyncapi.yaml`.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across
  relay retries; consumers dedupe on it); `source`
  `/retailnetwork/linehaul-transportation`; `subject` the aggregate root id
  (`departureId` or `loadId`; for `BookingMoved` the `toDepartureId`); `time` the
  instant the change was committed, in UTC; `datacontenttype`
  `application/json`; `dataschema`
  `urn:retailnetwork:linehaul-transportation:<events|analytics>:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one aggregate stay ordered on one partition.
- `data` is camelCase; optional fields are omitted when absent (never null);
  arrays are always present; instants are RFC 3339 UTC.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the raising aggregate, lowercase, no separators: `departure` or
  `load`. Tier is `logistics`.

### Published types

Integration events go ONLY to `retailnetwork.linehaul-transportation.events`;
analytics-only events go ONLY to `retailnetwork.linehaul-transportation.analytics`.

```text
type: com.retailnetwork.logistics.linehaul-transportation.departure.DepartureScheduled stream: events
type: com.retailnetwork.logistics.linehaul-transportation.departure.DepartureCapacityChanged stream: events
type: com.retailnetwork.logistics.linehaul-transportation.departure.DepartureCancelled stream: events
type: com.retailnetwork.logistics.linehaul-transportation.departure.DepartureDeparted stream: analytics
type: com.retailnetwork.logistics.linehaul-transportation.departure.DepartureArrived stream: analytics
type: com.retailnetwork.logistics.linehaul-transportation.load.LoadClosed stream: analytics
type: com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted stream: events
type: com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived stream: events
type: com.retailnetwork.logistics.linehaul-transportation.departure.BookingMoved stream: events
type: com.retailnetwork.logistics.linehaul-transportation.departure.BookingFailed stream: events
```

| Event | Subject / key | Raised when | `data` |
|---|---|---|---|
| `DepartureScheduled` | `departureId` | ScheduleDeparture is saved | `{departureId, laneId, kind, originNodeId, destinationNodeId, cutoffAt, departsAt, arrivesAt, capacityPackages}` |
| `DepartureCapacityChanged` | `departureId` | BookCapacity, ReleaseCapacity or ChangeDepartureCapacity is saved | `{departureId, capacityPackages, bookedPackages, remainingPackages}` |
| `DepartureCancelled` | `departureId` | CancelDeparture is saved | `{departureId, reason, cancelledAt}` |
| `DepartureDeparted` | `departureId` | RecordDeparture (by DispatchLoad) is saved | `{departureId, departedAt}` |
| `DepartureArrived` | `departureId` | RecordArrival (by ReceiveLoad) is saved | `{departureId, arrivedAt}` |
| `LoadClosed` | `loadId` | CloseLoad is saved | `{loadId, departureId, packageCount}` |
| `LoadDeparted` | `loadId` | DispatchLoad is saved | `{loadId, departureId, originNodeId, trackingNumbers, departedAt, departureKind, nodeType}` |
| `LoadArrived` | `loadId` | ReceiveLoad is saved | `{loadId, departureId, destinationNodeId, trackingNumbers, arrivedAt, departureKind, nodeType}` |
| `BookingMoved` | `toDepartureId` | an assignment is rebooked on the next departure | `{assignmentId, fromDepartureId, toDepartureId}` |
| `BookingFailed` | `departureId` | no departure can hold the assignment | `{assignmentId, departureId, reason}` |

Payload rules:

- The `data` field lists are the model's `publishes[].data` lists plus the
  additions of the orchestrator's contracts-delta: `departureKind` and `nodeType`
  on `LoadDeparted` and `LoadArrived` (E3, gap G3: `nodeType` is the type of the
  node where it happened, the origin on departure and the destination on
  arrival), and the two events `BookingMoved` and `BookingFailed` (E13, gap G14).
  Wire types are an implementation decision recorded in `apis/asyncapi.yaml`.
- `DepartureDeparted`, `DepartureArrived` and `LoadClosed` are the model's
  analytics-only events.
- `BookingFailed.reason` is one of `DEPARTURE_CANCELLED`, `DEPARTURE_CLOSED`,
  `DEPARTURE_FULL`, `DEPARTURE_UNKNOWN` (ADR 0002, A14).
- A breaking payload change is a new `.v2` type and dataschema, never a mutation
  of v1.

### Consumed types (dispatched on the full string)

```text
consumes: com.retailnetwork.logistics.node-topology.node.NodeRegistered
consumes: com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared
consumes: com.retailnetwork.logistics.node-topology.node.NodeActivated
consumes: com.retailnetwork.logistics.node-topology.node.NodeSuspended
consumes: com.retailnetwork.logistics.node-topology.node.NodeRetired
consumes: com.retailnetwork.logistics.node-topology.lane.LaneOpened
consumes: com.retailnetwork.logistics.node-topology.lane.LaneClosed
consumes: com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode
consumes: com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReassigned
consumes: com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked
consumes: com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelled
consumes: com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued
consumes: com.retailnetwork.logistics.shipping-and-tracking.label.LabelVoided
```

The consumed contracts and the read models they feed are ADR 0003.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is
  no shared Go code.
- Adding a published type requires a line here in the same commit as its
  asyncapi message, or the architecture fitness test fails.
