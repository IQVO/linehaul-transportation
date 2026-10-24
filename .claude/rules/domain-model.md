# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: warehouse-docs `network/model/model.yaml` (context `linehaul-transportation`) and the contracts-delta of the
campaign. Invariants, transitions and every assumption the model left open are in
`docs/adr/0002-aggregates-and-invariants.md` (assumptions A1-A30).

## Ubiquitous Language (use these exact names)

- **Departure** — a scheduled trip on a lane with a cutoff, a departure time, an arrival time and a package capacity. Identified by
  `departureId` (deterministic, `dep-` + a hash of lane, kind and cutoff; A1). Kinds `PICKUP` (starts at a vendor fulfillment center),
  `LINEHAUL` (GROUND lane), `AIR` (AIR lane).
- **Cutoff** — the last instant a package can be booked on a departure (`cutoffAt <= departsAt < arrivesAt`).
- **Capacity / booked / remaining** — `capacityPackages`, `bookedPackages`, `remainingPackages = capacity - booked`; booked never exceeds capacity.
- **LaneSchedule** — the weekly slots (`dayOfWeek`, local cutoff, departs, arrives, `capacityPackages`) of a lane; a redefinition replaces the slots.
- **Load** — the packages (tracking numbers) consolidated onto one departure at its origin node; at most one load per departure (A19).
- **Booking** — one package of an assignment held on a departure; `BookingMoved` when it was moved to the next pickup from the same origin,
  `BookingFailed` when none fits.
- **Pickup** — a `PICKUP` departure generated for an ACTIVE vendor fulfillment center at each cutoff of its profile, toward the destination of
  its default outbound lane (A7-A9).
- **Horizon roll** — policy that keeps 14 operating days of departures generated (A6); also reacts to node and lane events.
- **ExpectedVolume** — the per-assignment projection of order-sourcing and shipping-and-tracking facts, with the once-only booking record.

## Aggregates

- **Departure** (`internal/domain/departure`): states `SCHEDULED`, `CLOSED`, `DEPARTED`, `ARRIVED`, `CANCELLED`; times in order
  (`ErrInvalidDepartureTimes`), `booked <= capacity` (`ErrDepartureFull`), lane open when scheduling (`ErrLaneClosed`), no booking after the
  cutoff (`ErrDepartureClosed`).
- **LaneSchedule** (`internal/domain/schedule`): `ACTIVE` -> `RETIRED`; slots of a lane do not overlap (`ErrOverlappingSlots`). Also holds the pure
  slot expansion and pickup planning that return departure specs.
- **Load** (`internal/domain/load`): `OPEN` -> `CLOSED` -> `DEPARTED` -> `ARRIVED`; a package on at most one active load
  (`ErrPackageAlreadyLoaded`), count within the capacity taken at open (`ErrLoadOverCapacity`), one load per departure (`ErrLoadAlreadyExists`).
- Read models, not aggregates: `internal/domain/network` (`NodeView`, `LaneView`, fed by node-topology) and `internal/domain/volume`
  (`ExpectedVolume`).
- `internal/domain/shared`: `Event`, `Header`, `ErrValidation`, `ErrIllegalTransition`, `ErrInvalidSnapshot`, id checks.

Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` and an optimistic `Version()`; `now` and ids are passed in. `ErrNotFound`
and `ErrConcurrentUpdate` are port errors (`internal/application/ports/errors.go`), not domain errors. The domain imports nothing outside
`internal/domain`.

## Domain events

Published (wire shape in `apis/asyncapi.yaml`, catalogue in ADR 0004; `internal/domain/*/events.go`):

- Departure: `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`, `BookingMoved`, `BookingFailed` (integration, `.events`);
  `DepartureDeparted`, `DepartureArrived` (analytics only).
- Load: `LoadDeparted`, `LoadArrived` (integration, carry `departureKind` and `nodeType`); `LoadClosed` (analytics only).
- `LaneSchedule` raises no event.

## Key use cases (`internal/application/usecases`)

One per command and query; commands run in ONE `ports.UnitOfWork` (load, domain call, save, outbox append) and retry on `ErrConcurrentUpdate`.

- Lane schedules (`schedules.go`): `DefineLaneSchedule`, `RetireLaneSchedule`.
- Departures (`departures.go`): `ListDepartures`, `GetDeparture`, `CancelDeparture`, `ChangeDepartureCapacity`; `generate.go`: `GenerateDepartures`
  (the horizon roll) and the pickup planning.
- Loads (`loads.go`): `OpenLoad`, `AddPackage`, `CloseLoad`, `DispatchLoad`, `ReceiveLoad`.
- Booking (`booking.go`): booking, rebooking and cancelling on a working set (`BookingMoved`, `BookingFailed`).
- Consumed events (`consumers.go`): `HandleNodeRegistered`, `HandleProfileDeclared`, `HandleNodeActivated`, `HandleNodeSuspended`,
  `HandleNodeRetired`, `HandleLaneOpened`, `HandleLaneClosed`, `HandleReleased`, `HandleReassigned`, `HandleLocked`, `HandleCancelled`,
  `HandleLabelIssued`, `HandleLabelVoided`; each claims the CloudEvents id in the inbox in the same unit of work.
- Ports (`internal/application/ports/ports.go`) are interfaces only; the memory test doubles in `internal/adapters/outbound/memory` are test
  doubles and must pass the same suite as Postgres (`internal/adapters/outbound/repocontract`). Read DTOs are in `internal/application/views`,
  filters in `internal/application/repository`.
