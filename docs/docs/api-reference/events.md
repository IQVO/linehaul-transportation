---
id: events
title: Event catalogue
sidebar_label: Events
description: "The CloudEvents Linehaul Transportation publishes and consumes, with topics, fields and delivery guarantees."
---

# Event catalogue

Contract: `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0), pinned to the code by `TestEventCatalogueMatchesContract`. Encoder: `internal/adapters/kafka/cloudevents`,
`internal/adapters/kafka/wire`. Field-level detail with the aggregate that raises each event: [Domain events](/docs/ddd/domain-events).

## Envelope

Every message is a CloudEvents 1.0 event in structured mode; the Kafka header `content-type` is `application/cloudevents+json; charset=UTF-8`.

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | UUID minted once per occurrence and persisted in `outbox_events.event_id`, so a relay retry republishes the same id (dedupe key) |
| `source` | `/retailnetwork/linehaul-transportation` |
| `type` | `com.retailnetwork.logistics.linehaul-transportation.<entity>.<EventName>`; entity `departure` or `load` (the booking events use `departure`) |
| `subject` | the aggregate id (`departureId` or `loadId`; for `BookingMoved` the new departure, for `BookingFailed` the departure) |
| `time` | domain occurred-at, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:linehaul-transportation:<stream>:<EventName>:v1` with stream `events` or `analytics` |
| Kafka key | the `subject` |

Consumers dispatch on the full `type`, ignore unknown types and dedupe on `id`. Delivery is at-least-once ([ADR 0005](/docs/adr/0005-outbox-relay-and-attempt-cap)).

## Published: `retailnetwork.linehaul-transportation.events`

| Type suffix | Raised by | `data` fields |
| --- | --- | --- |
| `departure.DepartureScheduled` | `ScheduleDeparture` in the horizon roll and pickup planning | `departureId`, `laneId`, `kind` (`PICKUP`, `LINEHAUL`, `AIR`), `originNodeId`, `destinationNodeId`, `cutoffAt`, `departsAt`, `arrivesAt`, `capacityPackages` |
| `departure.DepartureCapacityChanged` | booking, release, `change-capacity`, `AddPackage` on a LINEHAUL or AIR departure | `departureId`, `capacityPackages`, `bookedPackages`, `remainingPackages` |
| `departure.DepartureCancelled` | `CancelDeparture`, node suspension or retirement, profile revision | `departureId`, `reason`, `cancelledAt` |
| `departure.BookingMoved` | an assignment rebooked onto the next pickup | `assignmentId`, `fromDepartureId`, `toDepartureId` |
| `departure.BookingFailed` | a booking could not be placed | `assignmentId`, `departureId`, `reason` (`DEPARTURE_CANCELLED`, `DEPARTURE_CLOSED`, `DEPARTURE_FULL`, `DEPARTURE_UNKNOWN`) |
| `load.LoadDeparted` | `DispatchLoad` | `loadId`, `departureId`, `originNodeId`, `trackingNumbers[]`, `departedAt`, `departureKind`, `nodeType` |
| `load.LoadArrived` | `ReceiveLoad` | `loadId`, `departureId`, `destinationNodeId`, `trackingNumbers[]`, `arrivedAt`, `departureKind`, `nodeType` |

`nodeType` is the node type of the origin (`LoadDeparted`) or destination (`LoadArrived`) node: one of `FULFILLMENT_CENTER`, `INBOUND_CROSS_DOCK`,
`RETURNS_CENTER`, `SORTATION_CENTER`, `DELIVERY_STATION`, `AIR_HUB`.

## Published: `retailnetwork.linehaul-transportation.analytics`

Analytics-only events, kept off the integration topic.

| Type suffix | Raised by | `data` fields |
| --- | --- | --- |
| `departure.DepartureDeparted` | `DispatchLoad` (the departure leaves) | `departureId`, `departedAt` |
| `departure.DepartureArrived` | `ReceiveLoad` | `departureId`, `arrivedAt` |
| `load.LoadClosed` | `CloseLoad` | `loadId`, `departureId`, `packageCount` |

## Consumed

| Topic | Types (producer's `type`) | Handler |
| --- | --- | --- |
| `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.{NodeRegistered, NodeOperationalProfileDeclared, NodeActivated, NodeSuspended, NodeRetired}`, `...lane.{LaneOpened, LaneClosed}` | `NodeTopologyHandlers` |
| `retailnetwork.order-sourcing.events` | `com.retailnetwork.commerce.order-sourcing.assignment.{FulfillmentReleasedToNode, FulfillmentReassigned, FulfillmentLocked, FulfillmentCancelled}` | `OrderSourcingHandlers` |
| `retailnetwork.shipping-and-tracking.events` | `com.retailnetwork.logistics.shipping-and-tracking.label.{LabelIssued, LabelVoided}` | `ShippingHandlers` |

Dead letters of a consumed topic go to `<topic>.dlq`. The effect of each consumed event is on [Use cases](/docs/ddd/use-cases) and the fields relied on are on
[Integration](/docs/ecosystem/integration).

## Example

The `LoadDeparted` message published during the [Quickstart](/docs/overview/quickstart) run (Kafka key = `subject`; headers `content-type: application/cloudevents+json; charset=UTF-8`
and `traceparent`):

```json
{"specversion":"1.0","id":"52f1dce8-3cd1-4c31-b57b-33d49f777aa8","source":"/retailnetwork/linehaul-transportation","type":"com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted","subject":"load-06b16401-46d8-46b5-99bb-9f7d384fb8c9","datacontenttype":"application/json","dataschema":"urn:retailnetwork:linehaul-transportation:events:LoadDeparted:v1","time":"2026-10-31T22:25:22.770828Z","data":{"loadId":"load-06b16401-46d8-46b5-99bb-9f7d384fb8c9","departureId":"dep-3b54a35b8ce988dbbd6feb8d341b2535","originNodeId":"NODE-SP-DC","trackingNumbers":["TRK-0001"],"departedAt":"2026-10-31T22:25:22.770828Z","departureKind":"LINEHAUL","nodeType":"SORTATION_CENTER"}}
```
