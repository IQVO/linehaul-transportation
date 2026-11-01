---
id: integration
title: Integration
sidebar_label: Integration
description: "Every upstream and downstream of Linehaul Transportation: protocol, contract, event types and failure behaviour."
---

# Integration

This context calls no other service at runtime. It talks to its neighbours only through Kafka: it consumes three producers' topics into local read
models and publishes two topics of its own. Everything else here is how others read it.

| Direction | Peer | Protocol | Contract |
| --- | --- | --- | --- |
| Upstream | `node-topology` | Kafka, topic `retailnetwork.node-topology.events` | 7 event types |
| Upstream | `order-sourcing` | Kafka, topic `retailnetwork.order-sourcing.events` | 4 event types |
| Upstream | `shipping-and-tracking` | Kafka, topic `retailnetwork.shipping-and-tracking.events` | 2 event types |
| Downstream | `order-sourcing`, `delivery-promise` | Kafka, topic `retailnetwork.linehaul-transportation.events` | departure and booking events |
| Downstream | `shipping-and-tracking`, `last-mile-delivery` | Kafka, same topic | `LoadDeparted`, `LoadArrived` |
| Downstream | analytics readers | Kafka, topic `retailnetwork.linehaul-transportation.analytics` | `DepartureDeparted`, `DepartureArrived`, `LoadClosed` |
| Clients | planners, operators, UIs | REST on `cmd/api` (Kong `/api/linehaul-transportation`) | `apis/openapi.yaml` |
| Clients | AI agents | MCP on `cmd/mcp` (Kong `/api/mcp/linehaul-transportation`) | `list_departures`, `get_departure` |
| Clients | peer agents | A2A on `cmd/a2a` (Kong `/api/a2a/linehaul-transportation`) | skills `list_departures`, `get_departure` |

All Kafka messages are CloudEvents 1.0 in structured mode (`content-type: application/cloudevents+json; charset=UTF-8`); the CloudEvents `id` is the
dedupe key, the `subject` and Kafka key are the aggregate id, and `type` is `com.retailnetwork.<tier>.<context>.<entity>.<EventName>`. REST, MCP and A2A are
unauthenticated; the cluster network boundary is the access control.

## Upstream: consumed events

Source: `internal/adapters/inbound/kafka/handlers.go`, `consumer.go`; contract copies in `apis/asyncapi.yaml` (`*Received` messages), ADR 0003.

| Producer | Event type (CloudEvents `type`) | Fields relied on | Effect |
| --- | --- | --- | --- |
| node-topology | `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | `nodeId`, `nodeType`, `operator`, `timezone` | Creates the node view |
| node-topology | `...node.NodeOperationalProfileDeclared` | `nodeId`, `profileRevision`, `timezone`, `operatingHoursByDay`, `holidays`, `cutoffs[]`, `throughputCap` | Stores the profile, re-plans pickups |
| node-topology | `...node.NodeActivated`, `NodeSuspended`, `NodeRetired` | `nodeId` | Pickups generated or cancelled |
| node-topology | `...lane.LaneOpened` | `laneId`, `originNodeId`, `destinationNodeId`, `mode` (GROUND or AIR), `nominalTransitMinutes` | Creates or refreshes the lane view |
| node-topology | `...lane.LaneClosed` | `laneId` | Lane closed: no new departures |
| order-sourcing | `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode` | `assignmentId`, `releaseNo`, `orderId`, `groupNo`, `nodeId`, `pickupDepartureId` | Records the expected volume |
| order-sourcing | `...assignment.FulfillmentReassigned` | `assignmentId`, `toNodeId` | Updates the node |
| order-sourcing | `...assignment.FulfillmentLocked` | `assignmentId`, `orderId`, `groupNo`, `nodeId`, `pickupDepartureId` | Books capacity (`BookOnLock`) |
| order-sourcing | `...assignment.FulfillmentCancelled` | `assignmentId` | Marks the volume cancelled |
| shipping-and-tracking | `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `assignmentId`, `trackingNumber` | Adds the tracking number |
| shipping-and-tracking | `...label.LabelVoided` | `trackingNumber` | Removes it |

Consumer groups are mandatory settings (`NODE_TOPOLOGY_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_AND_TRACKING_CONSUMER_GROUP`). Payload decoding is
tolerant: only the fields above are read and unknown fields and unknown event types are ignored.

**Failure behaviour.** Each message is claimed in `processed_events(consumer, event_id)` in the same transaction as its effect, so redelivery is harmless.
A message that is not a valid CloudEvent, or whose data can never be applied (undecodable, refused by the domain), is dead-lettered to
`<topic>.dlq` with `x-dlq-*` headers and the offset is committed. A transient handler failure is retried on the same message with a 200 ms to 5 s
backoff and dead-lettered after 5 attempts. A stalled or reset broker connection is retried (up to 10 consecutive failures); a refused connection ends
the worker and the process, and Kubernetes restarts it. An event that arrives before what it depends on (a lane before its nodes, a lock before its
release) is applied in whatever order it comes: the views are order-tolerant, and pickups are re-planned when the missing piece arrives ([Use cases](/docs/ddd/use-cases)).
A topic that has not been written yet is simply empty.

## Downstream: published events

Source: `internal/adapters/kafka/wire/wire.go`, `apis/asyncapi.yaml`. The full catalogue with fields is on [Events](/docs/api-reference/events).

| Topic | Events |
| --- | --- |
| `retailnetwork.linehaul-transportation.events` | `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`, `BookingMoved`, `BookingFailed`, `LoadDeparted`, `LoadArrived` |
| `retailnetwork.linehaul-transportation.analytics` | `DepartureDeparted`, `DepartureArrived`, `LoadClosed` |

Delivery is at-least-once with a stable event id (ADR 0005); events of one aggregate are keyed by it, so they stay ordered per partition unless a row
reaches the attempt cap and is overtaken ([Runbook](/docs/operations/runbook)). Consumers must dedupe on `id`, dispatch on the full `type` and ignore
unknown types.

Consumers found in sibling repositories on `develop` (read from their code, not from this service):

| Consumer | Types it handles from this service | Use |
| --- | --- | --- |
| `delivery-promise` | `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled` | Builds its departures read model; revises promises on a cancellation |
| `order-sourcing` | `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`, `BookingMoved`; `BookingFailed` is received but changes no state | Pickup availability, rebooked assignments |
| `shipping-and-tracking` | `LoadDeparted`, `LoadArrived` | Package timeline (pickup departure, station arrival) |
| `last-mile-delivery` | `LoadArrived` | Inducts packages into delivery waves when the destination is a delivery station |

If one of them is down, nothing happens here: the relay publishes regardless and the consumer catches up from its committed offsets.

## Absent edges

- **No outbound REST, MCP or A2A call to any context.** The three agent and REST adapters only read this context's own database.
- **No call to `vendor-connectivity` and no knowledge of vendors beyond node-topology's profile.** The warehouse-systems fleet reaches the network only through the
  Vendor API; this context never talks to it.
- **No dependency on `warehouse-console` at runtime.** The `linehaul_mfe` remote (`web/`, ADR 0009) is a read-only screen served by the web gateway that calls
  this context's REST through Kong.
- **No schema registry.** Contracts are `apis/asyncapi.yaml` and a fitness test (`TestEventCatalogueMatchesContract`) that keeps the code in line with it.

## Reading the service

| Interface | Where | Notes |
| --- | --- | --- |
| REST | `cmd/api` `:8080`, Kong `/api/linehaul-transportation` | Eleven resource paths plus `/healthz` and `/readyz`; errors are RFC 9457 problems; every POST needs `Idempotency-Key` |
| MCP | `cmd/mcp` `:8090`, `/mcp`; Kong `/api/mcp/linehaul-transportation` (opt-in) | Read-only tools, see [MCP tools](/docs/mcp/tools) |
| A2A | `cmd/a2a` `:8093`, `/` and `/a2a`; Agent Card `/.well-known/agent-card.json`; Kong `/api/a2a/linehaul-transportation` (opt-in) | Read-only skills; A2A carries delegation only, facts between contexts travel as CloudEvents |

See [API overview](/docs/api-reference) for the endpoint table.
