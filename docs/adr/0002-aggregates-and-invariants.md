# ADR 0002: the Departure, LaneSchedule and Load aggregates, their invariants and the assumptions the model left open

## Status

Accepted (2026-10-23).

## Context

`model/model.yaml` (retail-network-docs, `contexts[linehaul-transportation]`) names three aggregates, their states,
transitions, invariants with `Err` names and commands. The Model Gap Register decisions G3, G9, G14, G15, G28 and G29
and the orchestrator's `contracts-delta` (E3, E13, E14, E16) are final and are applied here. Where the model is still
silent this ADR records the reading chosen as a numbered assumption (A1..A30). The orchestrator decides whether to
move an assumption into the model; none of them edits `model.yaml`.

## Decision

### Package layout

| Package | Content |
| --- | --- |
| `internal/domain/shared` | `Event`, `Header`, shared errors (`ErrValidation`, `ErrIllegalTransition`, `ErrInvalidSnapshot`), id checks, `DeterministicID`, `MaxInt32` |
| `internal/domain/departure` | `Departure` aggregate: kinds, capacity, times, events |
| `internal/domain/schedule` | `LaneSchedule` aggregate, slot expansion and pickup planning (pure functions that return departure specs) |
| `internal/domain/load` | `Load` aggregate and its events |
| `internal/domain/network` | `NodeView`, `LaneView`: the two read models fed by node-topology (value types and their merge rules) |
| `internal/domain/volume` | `ExpectedVolume`: the per-assignment projection and the once-only booking record |

A missing aggregate is the port sentinel `ports.ErrNotFound`, an optimistic-lock mismatch is `ports.ErrConcurrentUpdate`;
neither is a domain error.

### Departure

States `SCHEDULED, CLOSED, DEPARTED, ARRIVED, CANCELLED`; transitions `SCHEDULED->CLOSED` (CloseDeparture),
`CLOSED->DEPARTED` (RecordDeparture), `DEPARTED->ARRIVED` (RecordArrival), `SCHEDULED->CANCELLED` (CancelDeparture).
`ScheduleDeparture` creates at version 1. `BookCapacity`, `ReleaseCapacity` and `ChangeDepartureCapacity` change `booked`
or `capacity`, not the state.

| Invariant | Error | HTTP / problem |
| --- | --- | --- |
| `cutoffAt <= departsAt < arrivesAt` | `ErrInvalidDepartureTimes` | 422 `invalid-departure-times` |
| `booked <= capacity` (also: a capacity change below `booked`) | `ErrDepartureFull` | 409 `departure-full` |
| the lane is OPEN when scheduling | `ErrLaneClosed` | 409 `lane-closed` |
| no booking after `cutoffAt`, and none unless SCHEDULED | `ErrDepartureClosed` | 409 `departure-closed` |
| any other command from a wrong state | `ErrIllegalTransition` | 409 `illegal-transition` |
| a field breaks a value-object rule (kind, ids, capacity range) | `ErrValidation` | 422 `validation-failed` |

Events: `DepartureScheduled` (Schedule), `DepartureCapacityChanged` (Book, Release, ChangeCapacity), `DepartureCancelled`
(Cancel), `DepartureDeparted` (RecordDeparture), `DepartureArrived` (RecordArrival). `CloseDeparture` raises none, as in the model.

### LaneSchedule

States `ACTIVE, RETIRED`; `DefineLaneSchedule` creates (or replaces the slots of an ACTIVE schedule, A4), `RetireLaneSchedule`
ACTIVE->RETIRED. Invariant: the slots of a lane do not overlap (`ErrOverlappingSlots`, 422 `overlapping-slots`). It raises no
event (`events: []`). `GenerateDepartures` is a pure function over `ACTIVE` schedules and node/lane views that returns
`DepartureSpec`s; the use case schedules them.

### Load

States `OPEN, CLOSED, DEPARTED, ARRIVED`; `OpenLoad` creates, then `CloseLoad`, `DispatchLoad`, `ReceiveLoad` in that order.
`AddPackage` adds a tracking number while OPEN.

| Invariant | Error | HTTP / problem |
| --- | --- | --- |
| a package is on at most one OPEN or CLOSED load | `ErrPackageAlreadyLoaded` | 409 `package-already-loaded` |
| package count <= the capacity snapshot taken at OpenLoad | `ErrLoadOverCapacity` | 409 `load-over-capacity` |
| a departure has at most one load (A19) | `ErrLoadAlreadyExists` | 409 `load-already-exists` |
| wrong state for the command | `ErrIllegalTransition` | 409 `illegal-transition` |

Events: `LoadClosed` (analytics), `LoadDeparted` and `LoadArrived` (integration, with `departureKind` and `nodeType`, E3).

### Read models (not aggregates)

`NodeView` and `LaneView` copy node-topology's facts; `ExpectedVolume` projects order-sourcing and shipping-and-tracking
facts and records which departure an assignment is booked on. They decide nothing except the once-only booking record
(A13). The consumed contracts are ADR 0003.

## Assumptions

**A1. Departure identity.** `departureId` is deterministic: `dep-` + the first 16 bytes of
SHA-256(`laneId|kind|cutoffAt (UTC, RFC 3339)|generation`) in hex. A run of `GenerateDepartures` that finds the id (or a
departure with the same lane, kind and cutoff) schedules nothing. `generation` is 0 for lane-schedule departures and the node's
activation count for pickups (A10). A departure the planner cancelled is not resurrected by the next run.

**A2. Time zones of slots (G29).** A lane-schedule slot has `dayOfWeek`, `cutoffLocalTime`, `departsLocalTime`,
`arrivesLocalTime`, `capacityPackages` (the pack's shape). Cutoff and departure are wall-clock times in the IANA zone of the
lane's origin node; the arrival is the first instant after `departsAt` whose wall-clock time in the destination node's zone is
`arrivesLocalTime`. A lane whose origin or destination zone is not known yet is skipped and retried at the next run.

**A3. Slot rules.** `cutoffLocalTime <= departsLocalTime` on the same day (`ErrInvalidDepartureTimes`; overnight slots are
not supported). Capacity is 1..2147483647; 1..100 slots per schedule. Two slots of the same weekday overlap when their closed
intervals `[cutoff, departs]` intersect (`ErrOverlappingSlots`, so slots that merely touch overlap).

**A4. Redefinition and retirement (G29).** `DefineLaneSchedule` on an ACTIVE schedule replaces its slots (version +1, from the
next horizon roll; already generated departures stay); on a RETIRED one it is `ErrIllegalTransition`. The lane must be in
`LaneView` (else 404 `not-found`); a CLOSED lane may be given a schedule, `ErrLaneClosed` guards scheduling. Retiring a RETIRED
schedule returns it unchanged. Retiring cancels nothing.

**A5. Kind of generated departures.** A GROUND lane produces `LINEHAUL` departures, an AIR lane `AIR` ones. A schedule on a lane
whose origin is a vendor `FULFILLMENT_CENTER` generates nothing: pickups come only from the node's declared cutoffs (G9).

**A6. Horizon and roll.** The horizon is 14 operating days counted from the current local date at the origin node. A departure
whose `cutoffAt` is not after `now` is not generated. HorizonRoll is an in-process loop in `cmd/api` that runs
`GenerateDepartures` at start and every `HORIZON_ROLL_INTERVAL` (optional, default `1h`); it is idempotent (A1), so several
replicas are harmless.

**A7. Pickup planning (G9, G28).** A pickup is planned for a node that is vendor-operated, a `FULFILLMENT_CENTER`, `ACTIVE`, has a
declared profile and has a default outbound lane (A8). For each cutoff: the weekdays are `days` or, when absent, every weekday
with operating hours; days without operating hours and the `holidays` (ISO dates in the node's zone) are skipped. `cutoffAt` is the
local date + `localTime` in the node's zone; the truck leaves at the cutoff (`departsAt = cutoffAt`) and arrives
`nominalTransitMinutes` of the lane later. Pickups are generated on NodeActivated, on every profile revision of an active node, on
LaneOpened of its lanes and by the horizon roll, whichever event arrives last completes the inputs.

**A8. Default outbound lane.** node-topology declares no default lane. The default is the OPEN GROUND lane that leaves the node
and ranks first by: destination type SORTATION_CENTER before any other known type before an unknown one, then lowest
`nominalTransitMinutes`, then lowest `laneId`. No such lane: no pickups yet (retried when a lane opens).

**A9. Pickup capacity (G9).** `capacityPackages = capacityOrders x packagesPerOrder`. `capacityOrders = max(1, min(maxOrders, cap
over the window))`, `maxOrders` applying only when set. The window is the time from the previous applicable cutoff of the same day
(the opening time for the first) to this cutoff, in minutes, at least 60. `HOUR` cap: `value x windowMinutes / 60`; `DAY` cap:
`value x windowMinutes / operatingMinutesOfTheDay` (24 h when the hours are not increasing). Integer division, floor.
`packagesPerOrder` is the optional service setting `PACKAGES_PER_ORDER` (default 1, range 1..1000): node-topology declares no such
field.

**A10. Suspension and activation epochs.** NodeSuspended and NodeRetired cancel the future SCHEDULED pickup departures of the node
(reasons `NODE_SUSPENDED`, `NODE_RETIRED`), then rebook their assignments (A15). Each NodeActivated that follows a non-ACTIVE
status increments the node's `generation` (A1), so a re-activated node gets fresh pickup ids instead of colliding with its
cancelled ones.

**A11. Profile revisions.** `NodeOperationalProfileDeclared` is applied only when its `profileRevision` is higher than the stored
one. For an ACTIVE node the pickups are re-planned: missing ones are scheduled; future SCHEDULED pickups of the node that are no
longer in the plan and have `booked = 0` are cancelled (`PROFILE_REVISED`); booked ones stay.

**A12. LaneClosed.** It marks the LaneView row CLOSED and cancels nothing (the model names no reaction); `ErrLaneClosed` only
guards new scheduling and a planner cancels departures by hand.

**A13. Booking at lock (BookOnLock, G15).** `FulfillmentLocked` books exactly one package per assignment on its pickup
departure, once: `ExpectedVolume` records `bookedOn` and a repeated lock books nothing. Packages beyond the first of a group are
not booked separately (the planner sizes capacity with `packagesPerOrder`). The release branch of BookOnLock is removed (G15).

**A14. Rebooking (G14, E13).** When the departure named by the lock cannot take the booking (reason `DEPARTURE_UNKNOWN`,
`DEPARTURE_CANCELLED`, `DEPARTURE_CLOSED` for a non-SCHEDULED state or `now > cutoffAt`, `DEPARTURE_FULL`), the service picks the
next departure: same `originNodeId` (the departure's, else the event's `nodeId`), kind PICKUP, state SCHEDULED, `cutoffAt > now`
and later than the failed departure's cutoff when known, `remaining >= 1`, ordered by `cutoffAt` then `departureId`. Found: book
it, publish `DepartureCapacityChanged` for it and `BookingMoved{assignmentId, fromDepartureId, toDepartureId}`. None:
`BookingFailed{assignmentId, departureId, reason}` with the reason of the first failure, and the assignment is recorded as FAILED
(no capacity held). The node never changes after lock.

**A15. Cancelling a departure rebooks its bookings.** CancelDeparture (planner, node suspension, profile revision) cancels first
and, in the same transaction, rebooks every assignment booked on it by A14 (reason `DEPARTURE_CANCELLED`), after all departures of
one event were cancelled so a sibling being cancelled is never a target. ReleaseCapacity is allowed on SCHEDULED and CANCELLED
departures; on a cancelled departure it publishes no `DepartureCapacityChanged` (the cancellation is the signal).

**A16. Reassigned and Cancelled.** Assignments are reassigned and cancelled only before LOCKED, so nothing is booked yet: both
update `ExpectedVolume` only. If order-sourcing ever cancels after the lock the capacity stays booked until the departure leaves
(model gap, reported).

**A17. Out-of-order events.** `FulfillmentReleasedToNode` applies when its `releaseNo` is not older than the stored one and never
touches a booking. A lock creates the row if the release was missed. A lock for an assignment already CANCELLED books nothing. A
label for an unknown assignment creates a row in state UNKNOWN.

**A18. Labels (E16).** `LabelIssued` records the tracking number on the assignment (also the `packageNo`); `LabelVoided` removes
that tracking number from the assignment that holds it. The booking is unchanged. The tracking numbers decide nothing.

**A19. One load per departure.** OpenLoad needs a SCHEDULED departure (else `ErrIllegalTransition`) and fails with
`ErrLoadAlreadyExists` when the departure already has a load (the model sizes a load as one per departure). OpenLoad copies kind,
origin, destination, both node types and the capacity from the departure. The tracking-number uniqueness is global over loads
that are OPEN or CLOSED (a unique index in Postgres, a scan in the memory double).

**A20. AddPackage and booking (G15, E14).** Order of checks: load OPEN and departure SCHEDULED (`ErrIllegalTransition`), tracking
number already on this load (no-op), on another active load (`ErrPackageAlreadyLoaded`), count would exceed the snapshot
(`ErrLoadOverCapacity`). Then, on a LINEHAUL or AIR departure only, the package is booked on the departure in the same transaction
(`ErrDepartureClosed` after the cutoff, `ErrDepartureFull`); a PICKUP package was booked at the lock and `booked` does not change.

**A21. Drive of the departure by the load (G28).** CloseLoad runs CloseDeparture when the departure is SCHEDULED, DispatchLoad
runs RecordDeparture (the departure must be CLOSED) and ReceiveLoad runs RecordArrival, each in the load's transaction. No
clock closes a departure: `ErrDepartureClosed` is computed from `now > cutoffAt` at booking time. AddPackage and CloseLoad on a
load whose departure is CANCELLED are `ErrIllegalTransition`.

**A22. Snapshots and wire names.** A departure stores the node types of its origin and destination (known when scheduled) and a
load copies them, so `LoadDeparted.nodeType` is the origin's type and `LoadArrived.nodeType` the destination's without a lookup.
The brief's gap register text says `destinationNodeType`; the final contracts-delta E3 says `nodeType`, which this service uses.

**A23. ChangeDepartureCapacity (G28).** A planner command: `POST /departures/{departureId}:change-capacity`, SCHEDULED only,
`0 <= capacityPackages <= 2147483647`, refused below `booked` (`ErrDepartureFull`), a no-op when equal. Open loads keep their
snapshot.

**A24. ListDepartures.** Filters `origin`, `destination`, `from` (inclusive), `to` (exclusive) on `cutoffAt`, plus `limit`
(default 500, max 1000); ordered by `cutoffAt`, `departureId`. An unparsable instant or `from > to` is 422 `validation-failed`.

**A25. Problems.** Slugs are the model's `Err` names in kebab case without `Err`, plus the shared `not-found`,
`illegal-transition`, `concurrent-update`, `validation-failed` (D3). `ErrLoadAlreadyExists` is the one `Err` this ADR adds. The
placeholders of the docs pack (`invalid-state-transition`, `concurrent-modification`) are not used.

**A26. Concurrency.** Every aggregate has an optimistic `version`. A consumer retries a use case up to 5 times on
`ErrConcurrentUpdate` (hot pickup rows); REST returns 409 `concurrent-update`.

**A27. Cancel reasons.** REST reasons are free text of 1..200 characters; system reasons are `NODE_SUSPENDED`, `NODE_RETIRED`,
`PROFILE_REVISED`.

**A28. ReleaseCapacity has no event trigger of its own.** It is used by the rebooking of A15 only (G15 removed the release branch).

**A29. Booking events.** `BookingMoved` and `BookingFailed` belong to the Departure aggregate (entity `departure`), go to the
integration topic and use the `toDepartureId` / `departureId` as subject and key.

**A30. Bounds.** Counts and capacities are at most 2147483647 (a plain literal, the Postgres `integer` ceiling); ids are 1..64
characters from `A-Za-z0-9._:-`; tracking numbers are 1..64 non-control characters; free text is trimmed and at most 200.

## model_gaps (still open after this ADR)

1. node-topology declares no default outbound lane (A8) and no `packagesPerOrder` (A9).
2. The model does not say what a cancellation after lock does to a booking (A16).
3. Overnight slots and multi-load departures are not modelled (A3, A19).
4. `LabelVoided` carries no `assignmentId`; the voided tracking number is matched through the projection (A18).

## Consequences

- Every behaviour above has a table-driven test in its package; mutation thresholds are set per package (`.gremlins.yaml`).
- The aggregate packages stay free of I/O; clock and ids are passed in.
