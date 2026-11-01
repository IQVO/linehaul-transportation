---
id: domain-events
title: Domain events
sidebar_label: Domain events
description: "Every event Linehaul Transportation publishes and consumes: CloudEvents type, topic, key, payload, producer and consumers."
---

# Domain events

Every event is a CloudEvents 1.0 structured-mode message ([envelope](/docs/api-reference/events#envelope)). Published events are raised by the aggregates, encoded in the same transaction and written to
`outbox_events`; the relay publishes them to Kafka at least once with a stable `id`. Full type prefix: `com.retailnetwork.logistics.linehaul-transportation.`; Kafka key = `subject` = the aggregate id.

Source: `internal/domain/departure/events.go`, `internal/domain/load/events.go`, `internal/domain/volume/events.go`, `internal/adapters/kafka/wire/wire.go`,
`internal/adapters/inbound/kafka/handlers.go`, `apis/asyncapi.yaml`; consumers read from `delivery-promise`, `order-sourcing`, `shipping-and-tracking` and `last-mile-delivery` on `develop`.

## Published events

| Event | Type suffix | Topic | Subject | Raised when | Payload | Consumers |
| --- | --- | --- | --- | --- | --- | --- |
| `DepartureScheduled` | `departure.DepartureScheduled` | `...linehaul-transportation.events` | `departureId` | a departure is created by the horizon roll or pickup planning | `departureId`, `laneId`, `kind`, `originNodeId`, `destinationNodeId`, `cutoffAt`, `departsAt`, `arrivesAt`, `capacityPackages` | delivery-promise, order-sourcing |
| `DepartureCapacityChanged` | `departure.DepartureCapacityChanged` | `...events` | `departureId` | a package is booked or released on a SCHEDULED departure, or its capacity changes | `departureId`, `capacityPackages`, `bookedPackages`, `remainingPackages` | delivery-promise, order-sourcing |
| `DepartureCancelled` | `departure.DepartureCancelled` | `...events` | `departureId` | a SCHEDULED departure is cancelled (planner, node suspended or retired, profile revised) | `departureId`, `reason`, `cancelledAt` | delivery-promise, order-sourcing |
| `BookingMoved` | `departure.BookingMoved` | `...events` | the new `departureId` | an assignment was rebooked on the next pickup of the same origin | `assignmentId`, `fromDepartureId`, `toDepartureId` | order-sourcing |
| `BookingFailed` | `departure.BookingFailed` | `...events` | `departureId` | no departure could hold the assignment | `assignmentId`, `departureId`, `reason` | order-sourcing (received, no state change) |
| `LoadDeparted` | `load.LoadDeparted` | `...events` | `loadId` | a CLOSED load is dispatched | `loadId`, `departureId`, `originNodeId`, `trackingNumbers`, `departedAt`, `departureKind`, `nodeType` | shipping-and-tracking |
| `LoadArrived` | `load.LoadArrived` | `...events` | `loadId` | a DEPARTED load is received | `loadId`, `departureId`, `destinationNodeId`, `trackingNumbers`, `arrivedAt`, `departureKind`, `nodeType` | shipping-and-tracking, last-mile-delivery |
| `DepartureDeparted` | `departure.DepartureDeparted` | `...analytics` | `departureId` | the departure of a dispatched load is recorded | `departureId`, `departedAt` | none found |
| `DepartureArrived` | `departure.DepartureArrived` | `...analytics` | `departureId` | the arrival of a received load is recorded | `departureId`, `arrivedAt` | none found |
| `LoadClosed` | `load.LoadClosed` | `...analytics` | `loadId` | an OPEN load is closed | `loadId`, `departureId`, `packageCount` | none found |

Topics: `retailnetwork.linehaul-transportation.events` and `retailnetwork.linehaul-transportation.analytics`. `BookingMoved` and `BookingFailed` use the entity segment `departure`.
`BookingFailed.reason` is one of `DEPARTURE_CANCELLED`, `DEPARTURE_CLOSED`, `DEPARTURE_FULL`, `DEPARTURE_UNKNOWN`. `departureKind` is `PICKUP`, `LINEHAUL` or `AIR`; `nodeType` is the node type of the origin (departed) or destination (arrived) node.

## Consumed events

| Event | Producer, topic | Aggregate or view touched | Consumer group |
| --- | --- | --- | --- |
| `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired` | node-topology, `retailnetwork.node-topology.events` | `node_views` (then pickups re-planned) | `NODE_TOPOLOGY_CONSUMER_GROUP` |
| `LaneOpened`, `LaneClosed` | node-topology, same topic | `lane_views` | same |
| `FulfillmentReleasedToNode`, `FulfillmentReassigned`, `FulfillmentLocked`, `FulfillmentCancelled` | order-sourcing, `retailnetwork.order-sourcing.events` | `ExpectedVolume`; `Departure` on lock | `ORDER_SOURCING_CONSUMER_GROUP` |
| `LabelIssued`, `LabelVoided` | shipping-and-tracking, `retailnetwork.shipping-and-tracking.events` | `ExpectedVolume.trackingNumbers` | `SHIPPING_AND_TRACKING_CONSUMER_GROUP` |

Consumed types carry the producer's tier and context: `com.retailnetwork.logistics.node-topology.{node,lane}.*`, `com.retailnetwork.commerce.order-sourcing.assignment.*`,
`com.retailnetwork.logistics.shipping-and-tracking.label.*`. Handling: [Use cases](/docs/ddd/use-cases#consumed-events-kafka).

## Delivery guarantees

- At-least-once, ordered per aggregate key unless a row hits `OUTBOX_MAX_ATTEMPTS` and is overtaken ([Runbook](/docs/operations/runbook)).
- Consumers deduplicate on the CloudEvents `id`; this service does so through `processed_events`.
- A trace context travels in the `traceparent` header of each published message.
