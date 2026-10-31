---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: "Every application use case of Linehaul Transportation: trigger, inputs, invariants and the events it raises."
---

# Use cases

Source: `internal/application/usecases` (`schedules.go`, `departures.go`, `loads.go`, `generate.go`, `booking.go`, `consumers.go`), the aggregates in
`internal/domain/{departure,schedule,load,volume,network}` and the problem table in `internal/adapters/inbound/http/errors.go`. Every use case runs
through `Service.write` (or `consume` for Kafka-driven ones): one Postgres transaction commits the aggregate change, its outbox rows and, for a consumed
event, the `processed_events` claim. Reads (`ListDepartures`, `GetDeparture`) are also use cases because REST, MCP and A2A all call them.

## Conventions

- **Idempotency.** Every REST POST needs an `Idempotency-Key` and replays the first stored answer for 24 h. Every consumer claims
  `(consumer, event_id)` in `processed_events`, so a redelivered CloudEvent changes nothing.
- **Optimistic concurrency.** Aggregates carry a `version`; a lost race is `concurrent-update` (409). Departure and ExpectedVolume writes inside a
  transaction share one working set (`bookingSet`) so a departure keeps one version line.
- **Idempotent commands.** A command whose target is already in the target state returns it unchanged and raises nothing (a CLOSED load on
  `:close`, a DEPARTED one on `:dispatch`, an ARRIVED one on `:receive`, a CANCELLED departure on `:cancel`, a RETIRED schedule on `:retire`).
- **Errors** become RFC 9457 problems; the slugs are in [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).

## Commands (REST)

| Use case | Trigger | Inputs | Invariants and rules | Events raised |
| --- | --- | --- | --- | --- |
| `DefineLaneSchedule` | `POST /lane-schedules` | `laneId`, 1 to 100 `slots` (`dayOfWeek`, `cutoffLocalTime`, `departsLocalTime`, `arrivesLocalTime`, `capacityPackages` >= 1) | The lane must be known in `lane_views` (else 404). Slots must not overlap on a weekday (`overlapping-slots`); departs not before cutoff (`invalid-departure-times`). Creates an ACTIVE schedule (201) or replaces the slots of an ACTIVE one (200, effective from the next horizon roll, departures already generated stay). A RETIRED schedule cannot be redefined (`illegal-transition`). | none |
| `RetireLaneSchedule` | `POST /lane-schedules/{laneId}:retire` | `laneId` | ACTIVE to RETIRED; the horizon roll skips it from now on; generated departures stay. | none |
| `CancelDeparture` | `POST /departures/{departureId}:cancel` | `departureId`, `reason` | Only a SCHEDULED departure can be cancelled (`illegal-transition` otherwise; an already CANCELLED one is returned unchanged). In the same transaction every assignment booked on it is rebooked on the next SCHEDULED PICKUP departure from the same origin with room and a later cutoff, or fails (ADR 0002 A15). | `DepartureCancelled`; per moved booking `BookingMoved` plus `DepartureCapacityChanged` of the new departure; per failed booking `BookingFailed` |
| `ChangeDepartureCapacity` | `POST /departures/{departureId}:change-capacity` | `departureId`, `capacityPackages` (0..2147483647) | Only SCHEDULED; never below the booked count (`departure-full`); the same value changes nothing. | `DepartureCapacityChanged` |
| `OpenLoad` | `POST /loads` | `departureId` | The departure must be SCHEDULED; at most one load per departure (`load-already-exists`, unique `loads.departure_id`). Copies kind, nodes, node types and capacity from the departure. | none |
| `AddPackage` | `POST /loads/{loadId}/packages` | `loadId`, `trackingNumber` (1 to 64 characters) | Load OPEN and its departure SCHEDULED; a tracking number is on at most one OPEN or CLOSED load (`package-already-loaded`, partial unique index); package count at most the departure capacity (`load-over-capacity`); a number already on this load is a no-op. On LINEHAUL and AIR departures the package is also booked on the departure (ADR 0002 A20); on a PICKUP departure the capacity was booked by the assignment. | `DepartureCapacityChanged` (LINEHAUL, AIR) |
| `CloseLoad` | `POST /loads/{loadId}:close` | `loadId` | OPEN to CLOSED while the departure is SCHEDULED; closes the departure (SCHEDULED to CLOSED). | `LoadClosed` (analytics topic) |
| `DispatchLoad` | `POST /loads/{loadId}:dispatch` | `loadId` | CLOSED to DEPARTED; records the departure of the trip (departure CLOSED to DEPARTED, `departedAt`). Counted in `linehaul.loads.dispatched` by outcome. | `LoadDeparted`, `DepartureDeparted` (analytics topic) |
| `ReceiveLoad` | `POST /loads/{loadId}:receive` | `loadId` | DEPARTED to ARRIVED; records the arrival of the trip (departure to ARRIVED, `arrivedAt`). | `LoadArrived`, `DepartureArrived` (analytics topic) |

## Queries (REST, MCP, A2A)

| Use case | Trigger | Inputs | Notes |
| --- | --- | --- | --- |
| `ListDepartures` | `GET /departures`, MCP `list_departures`, A2A skill `list_departures` | `origin`, `destination`, `from`, `to` (cutoff window, from inclusive, to exclusive, RFC 3339), `limit` (1..1000, default 500) | Ordered by `cutoffAt` then `departureId`. `limit` out of range or `from` after `to` is `validation-failed`. |
| `GetDeparture` | `GET /departures/{departureId}`, MCP `get_departure`, A2A skill `get_departure` | `departureId` | Unknown id is `not-found`. |

## Workers

| Use case | Trigger | Rules | Events raised |
| --- | --- | --- | --- |
| `GenerateDepartures` (policy `HorizonRoll`) | Once at boot of `cmd/api`, then every `GENERATE_INTERVAL` | For every ACTIVE lane schedule with a known, OPEN lane and known origin and destination nodes, plans the departures of the next 14 days (`schedule.PlanLane`; GROUND lanes produce LINEHAUL, AIR lanes AIR); for every pickup-eligible node, plans PICKUP departures from its declared cutoffs (`schedule.PlanPickups`). Departure ids are deterministic (lane, kind, cutoff, generation), so a rerun schedules nothing new. One failing schedule or node is counted in `failed` and logged; the rest still run. | `DepartureScheduled` per new departure |
| Outbox relay | Every `OUTBOX_RELAY_INTERVAL` | Publishes `outbox_events` rows in id order; skips rows past `OUTBOX_MAX_ATTEMPTS` | none (transport) |

## Consumed events (Kafka)

Each handler runs as `consume(consumer, eventId, ...)`: claim, apply, enqueue events, commit together.

| Use case | Consumed event | Effect | Events raised |
| --- | --- | --- | --- |
| `HandleNodeRegistered` | node-topology `NodeRegistered` | Stores the node view (type, operator, time zone); re-plans pickups of the node and of origins of lanes that end at it | `DepartureScheduled` for new pickups |
| `HandleProfileDeclared` | `NodeOperationalProfileDeclared` | Stores the profile when its revision is higher (ADR 0002 A11); re-plans pickups; cancels future unbooked pickups the cutoffs no longer produce (reason `PROFILE_REVISED`) | `DepartureScheduled`, `DepartureCancelled` |
| `HandleNodeActivated` | `NodeActivated` | Marks the node ACTIVE and generates its pickups (A10) | `DepartureScheduled` |
| `HandleNodeSuspended`, `HandleNodeRetired` | `NodeSuspended`, `NodeRetired` | Cancels every future SCHEDULED pickup of the node (reason `NODE_SUSPENDED` or `NODE_RETIRED`), rebooking its assignments | `DepartureCancelled`, `BookingMoved`, `BookingFailed` |
| `HandleLaneOpened` | `LaneOpened` | Stores or refreshes the lane view (mode GROUND or AIR, transit minutes >= 1) and re-plans pickups at its origin | `DepartureScheduled` |
| `HandleLaneClosed` | `LaneClosed` | Marks the lane closed: no new departure is scheduled on it (A12); unknown lane is ignored | none |
| `HandleReleased` | order-sourcing `FulfillmentReleasedToNode` | Records the assignment's expected volume (RELEASED) with its planned pickup departure | none |
| `HandleReassigned` | `FulfillmentReassigned` | Updates the node of the expected volume only (A16) | none |
| `HandleLocked` (policy `BookOnLock`) | `FulfillmentLocked` | Books one package on the pickup departure; if it is unknown, cancelled, closed or full, rebooks on the next PICKUP departure from the same origin or records the failure (A13, A14) | `DepartureCapacityChanged`, `BookingMoved`, `BookingFailed` |
| `HandleCancelled` | `FulfillmentCancelled` | Marks the expected volume CANCELLED; the booking is not released (A16) | none |
| `HandleLabelIssued` | shipping-and-tracking `LabelIssued` | Adds the tracking number to the assignment's expected volume (A18) | none |
| `HandleLabelVoided` | `LabelVoided` | Removes the tracking number from the projection; the booking stays (A18) | none |

`BookingFailed` carries one of the reasons `DEPARTURE_CANCELLED`, `DEPARTURE_CLOSED`, `DEPARTURE_FULL`, `DEPARTURE_UNKNOWN`.

## Not use cases

`CloseDeparture`, `RecordDeparture` and `RecordArrival` exist on the aggregate but have no entry point of their own: they run inside `CloseLoad`,
`DispatchLoad` and `ReceiveLoad`. `BookCapacity` and `ReleaseCapacity` are the aggregate methods `Book` and `Release` used by `HandleLocked`, `AddPackage` and
the cancel path.
