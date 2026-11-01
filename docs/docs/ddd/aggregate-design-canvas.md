---
id: aggregate-design-canvas
title: Aggregate design canvas
sidebar_label: Aggregate design canvas
description: "Aggregate Design Canvas for Departure, LaneSchedule, Load and ExpectedVolume: states, invariants, commands, events and concurrency."
---

# Aggregate design canvas

Following the ddd-crew [Aggregate Design Canvas](https://github.com/ddd-crew/aggregate-design-canvas) (v1.1). Source: `internal/domain/departure/departure.go`, `types.go`, `events.go`;
`internal/domain/schedule/schedule.go`; `internal/domain/load/load.go`, `events.go`; `internal/domain/volume/volume.go`, `events.go`; `internal/application/usecases`;
[ADR 0002](/docs/adr/0002-aggregates-and-invariants). Local read models (`NodeView`, `LaneView`) are not aggregates: they are replaced as a whole from consumed events.

## Departure

| Section | Content |
| --- | --- |
| Name | `Departure` (root id `departureId`, a deterministic `dep-<hash>` of lane, kind, cutoff and generation) |
| Description | One concrete trip on a lane with a cutoff, a departure and an arrival instant and a package capacity |
| State | `SCHEDULED`, `CLOSED`, `DEPARTED`, `ARRIVED`, `CANCELLED` |
| State transitions | `SCHEDULED -> CLOSED` (a load is closed), `CLOSED -> DEPARTED` (a load is dispatched), `DEPARTED -> ARRIVED` (a load is received), `SCHEDULED -> CANCELLED` |
| Enforced invariants | `cutoffAt <= departsAt < arrivesAt`; `0 <= booked <= capacity` (also a `CHECK` on `departures`); booking only while SCHEDULED and not after `cutoffAt`; capacity never below the booked count; a lane must be OPEN to schedule a departure |
| Commands | `Schedule` (horizon roll, pickup planning), `Book`, `Release`, `ChangeCapacity`, `Close`, `RecordDeparture`, `RecordArrival`, `Cancel` |
| Events raised | `DepartureScheduled`, `DepartureCapacityChanged` (booking, release on a SCHEDULED departure, capacity change), `DepartureCancelled`, `DepartureDeparted`, `DepartureArrived` |
| Errors | `ErrInvalidDepartureTimes`, `ErrDepartureFull`, `ErrLaneClosed`, `ErrDepartureClosed`, illegal transition |
| Correlation and concurrency | Optimistic: `version` is checked on save; a lost race is `ErrConcurrentUpdate` (409). Bookings inside one transaction share a working set so a departure keeps one version line. A release on a CANCELLED departure raises no event |
| Size | One row in `departures`; a 14-day horizon is generated ahead of every ACTIVE schedule and vendor node |

## LaneSchedule

| Section | Content |
| --- | --- |
| Name | `LaneSchedule` (root id `laneId`) |
| Description | The weekly template of one lane: 1 to 100 slots |
| State | `ACTIVE`, `RETIRED`; `ACTIVE -> RETIRED` |
| Enforced invariants | Slots of one weekday do not overlap; each slot has `cutoff <= departs` (`HH:mm`), capacity >= 1; a RETIRED schedule cannot be redefined |
| Commands | `Define` (create), `Redefine` (replace the slots of an ACTIVE schedule), `Retire` |
| Events raised | none (the departures it generates raise `DepartureScheduled`) |
| Concurrency | `version`; slots are one JSONB array, always read and written whole |

## Load

| Section | Content |
| --- | --- |
| Name | `Load` (root id `loadId`, `load-<uuid>`) |
| Description | The tracking numbers consolidated on one departure at its origin node |
| State | `OPEN`, `CLOSED`, `DEPARTED`, `ARRIVED`; `OPEN -> CLOSED -> DEPARTED -> ARRIVED` |
| Enforced invariants | A departure has at most one load (unique `loads.departure_id`); a tracking number is on at most one OPEN or CLOSED load (partial unique index); package count at most the departure capacity; the departure must be SCHEDULED to open, add to or close the load; 1 to 64 character tracking numbers |
| Commands | `Open`, `AddPackage`, `Close`, `Dispatch`, `Receive` |
| Events raised | `LoadClosed`, `LoadDeparted`, `LoadArrived` |
| Errors | `ErrPackageAlreadyLoaded`, `ErrLoadOverCapacity`, `ErrLoadAlreadyExists`, illegal transition |
| Concurrency | `version` checked on save; the load and its departure change in one transaction (`withLoad`) |

## ExpectedVolume

| Section | Content |
| --- | --- |
| Name | `ExpectedVolume` (root id `assignmentId`) |
| Description | The local record of one fulfillment assignment: its node, planned pickup departure, the departure it is booked on and its tracking numbers |
| State | `UNKNOWN` (only labels seen), `RELEASED`, `BOOKED`, `FAILED`, `CANCELLED` |
| Commands | `Release`, `Reassign`, `Cancel`, `Lock`, `Book`, `Rebook`, `Fail`, `AddTracking`, `RemoveTracking` |
| Events raised | `BookingMoved`, `BookingFailed` (reasons `DEPARTURE_CANCELLED`, `DEPARTURE_CLOSED`, `DEPARTURE_FULL`, `DEPARTURE_UNKNOWN`) |
| Enforced invariants | Valid order, node and departure ids; group number >= 1; at most 1000 tracking numbers; one booking per assignment (the lock books once) |
| Concurrency | `version`; every event from order-sourcing and shipping-and-tracking is claimed in `processed_events`, so a redelivery changes nothing |

## Policies

| Policy | Trigger | Command |
| --- | --- | --- |
| `HorizonRoll` | boot, then every `GENERATE_INTERVAL` | `GenerateDepartures` |
| `PickupFromCutoffs` | node and lane events; the horizon roll | schedule missing pickups, cancel stale unbooked ones |
| `BookOnLock` | `FulfillmentLocked` | `Book` on the pickup departure, else rebook or fail |
