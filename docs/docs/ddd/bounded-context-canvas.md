---
id: bounded-context-canvas
title: Bounded context canvas
sidebar_label: Bounded context canvas
description: "Bounded Context Canvas v5 for Linehaul Transportation: purpose, classification, roles, messages and decisions."
---

# Bounded context canvas

Following the ddd-crew [Bounded Context Canvas](https://github.com/ddd-crew/bounded-context-canvas) (v5), filled from the code and ADRs on this branch.

## 1. Name

**Linehaul Transportation** (`linehaul-transportation`), retail network, tier `logistics`.

## 2. Purpose

Turn the static lanes of the network into concrete, capacity-bounded trips: schedule departures (including a pickup departure at every cutoff of an active vendor node), keep each
departure's booked and remaining packages honest, build loads of packages onto departures, and report when loads depart and arrive. Other contexts promise and source against these trips.

## 3. Strategic classification

| Axis | Value |
| --- | --- |
| Domain | Supporting subdomain (Transportation, middle mile); see [Subdomain classification](/docs/ddd/subdomain-classification) |
| Business model | Cost reducer and speed enabler |
| Evolution | Custom-built |

## 4. Domain roles

**Execution context**: it schedules, books and loads. It also keeps local copies of node, lane and assignment facts (read models) and publishes its own facts.

## 5. Inbound communication

| Message | From | Type | Collaborator relationship |
| --- | --- | --- | --- |
| `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired` | node-topology | Event (Kafka) | Conformist |
| `LaneOpened`, `LaneClosed` | node-topology | Event (Kafka) | Conformist |
| `FulfillmentReleasedToNode`, `FulfillmentReassigned`, `FulfillmentLocked`, `FulfillmentCancelled` | order-sourcing | Event (Kafka) | Customer/Supplier |
| `LabelIssued`, `LabelVoided` | shipping-and-tracking | Event (Kafka) | Conformist |
| `DefineLaneSchedule`, `RetireLaneSchedule`, `CancelDeparture`, `ChangeDepartureCapacity` | network-ops planner | Command (REST) | Open Host Service |
| `OpenLoad`, `AddPackage`, `CloseLoad`, `DispatchLoad`, `ReceiveLoad` | sort-centre and station operators | Command (REST) | Open Host Service |
| `ListDepartures`, `GetDeparture` | any context, console, AI and peer agents | Query (REST, MCP, A2A) | Open Host Service |
| Timer | the process itself | `GenerateDepartures` every `GENERATE_INTERVAL` | internal |

## 6. Outbound communication

| Message | To | Type |
| --- | --- | --- |
| `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`, `BookingMoved`, `BookingFailed`, `LoadDeparted`, `LoadArrived` | `retailnetwork.linehaul-transportation.events` | Event (Kafka) |
| `DepartureDeparted`, `DepartureArrived`, `LoadClosed` | `retailnetwork.linehaul-transportation.analytics` | Event (Kafka, analytics only) |
| Dead letters | `<consumed topic>.dlq` | Kafka |
| Telemetry | OTel Collector | OTLP/gRPC |

## 7. Ubiquitous language

[Ubiquitous language](/docs/ddd/ubiquitous-language): departure, pickup departure, load, lane schedule, horizon, booking, expected volume.

## 8. Business decisions

1. Every declared cutoff of an ACTIVE vendor fulfillment node gets one pickup departure per operating day.
2. A departure's booked packages never exceed its capacity.
3. A package is on at most one OPEN or CLOSED load; a departure has at most one load.
4. Booking is accepted only on a SCHEDULED departure up to its cutoff.
5. A locked assignment books one package on its pickup departure; if that fails it moves to the next pickup from the same origin, or fails with a reason.
6. A cancelled departure rebooks the assignments booked on it in the same transaction.
7. The node of an assignment never changes after lock; the context only updates its view on reassignment or cancellation (before lock).
8. Events are published through a transactional outbox, at least once, with a stable id.

It does **not** decide which node or path fulfils a shipment group, what date the customer is told, or which delivery wave a package joins.

## 9. Assumptions

[ADR 0002](/docs/adr/0002-aggregates-and-invariants) records the assumptions (A1 to A30) the model left open, for example A9 (`PACKAGES_PER_ORDER`), A14 (rebooking) and A15 (cancel).

## 10. Verification metrics

`linehaul.loads.dispatched` by outcome; HTTP request duration; the outbox tail (`outbox_events` with `published_at IS NULL`); the `horizon roll` log line with `scheduled` and `failed`. See
[Observability](/docs/operations/observability).

## 11. Open questions

- Outbox backlog, consumer lag and dead letters are not metered or alerted.
- `BookingFailed` is received by `order-sourcing` and changes no state there; follow-up for a failed booking is outside this context.
- Route optimisation, carrier procurement and freight cost are not modelled.
