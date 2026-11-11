# ADR 0014: a follower lock books nothing

## Status

Accepted (2026-11-10). Amends [ADR 0003](./0003-consumed-contracts-and-local-read-models.md) (the fields relied on from
`FulfillmentLocked`) and [ADR 0002](./0002-aggregates-and-invariants.md) A13 (booking on lock). The decision is the linehaul slice of the
network's cross-order consolidation (order-sourcing merges compatible groups of one shopper and one ship-to into one package).

## Context

Until now one `FulfillmentLocked` meant one assignment, one `ExpectedVolume` row and one package booked on the pickup departure
(`HandleLocked`, policy `BookOnLock`).

Order-sourcing now merges several QUEUED assignments at release time. One of them is the **carrier**: it is the assignment that is due, and
the single purchase order, the single label and the single tracking number carry its id. The others are **followers**. Every member keeps
its own events from the lock on, so order-sourcing publishes one `FulfillmentLocked` per member. A follower's event carries the additive
optional field `consolidatedInto` (the carrier's assignment id) and, on every merged member, `mergedAssignmentIds`. Followers carry the
carrier's `nodeId` and `pickupDepartureId`. A non-merged lock carries neither field.

Booked per lock event, one merged package would take N packages of capacity on the departure while one package ships.

## Decision

- **A lock with a non-empty `consolidatedInto` books nothing.** `HandleLocked` receives the field in `LockedInput.ConsolidatedInto`
  (decoded by the private `lockedData` mirror in `internal/adapters/inbound/kafka/handlers.go`, tolerant like every other field).
  When it is set the CloudEvents id is claimed in the inbox (same transaction, as every consumer) and nothing else happens: no
  `ExpectedVolume` row is created, no booking, no `DepartureCapacityChanged`, `BookingMoved` or `BookingFailed`. A redelivery stops at the inbox claim.
- **The carrier's lock books ONE package,** exactly as an unmerged assignment does (it carries no `consolidatedInto`). Rebooking,
  `BookingMoved` and `BookingFailed` are raised for the carrier id only; order-sourcing fans `BookingMoved` out to the followers on its side.
- **`mergedAssignmentIds` is ignored.** Nothing in this context needs the member list: `consolidatedInto` alone decides.
- **Absence means unmerged.** An old event without the field is handled as before. A follower lock that arrives before or after the
  carrier lock gives the same result (nothing is stored for it), so no ordering between the two is assumed (ADR 0007 decision 6).
- **No other handler changes.** Followers have no label, so `LabelIssued` never names one.

## Consequences

- One merged package occupies one package of pickup capacity, which is what ships.
- The view holds no row for a follower that locked while merged. A follower that order-sourcing dissolves from its merge before the lock
  is re-queued and, locking later on its own, carries no `consolidatedInto`: it is booked like any other assignment.
- delivery-promise still charges one order of capacity per promise (a deliberate over-charge there); this context never sees that and
  needs no counterpart.
- Deploy order: this consumer is tolerant of old events and ships before the order-sourcing policy that enables merging is switched on
  (`consolidationMaxAssignments >= 2`). Merging while this change is not deployed would book every follower as its own package. No new
  environment variable, topic, subscription, migration or chart value.
- Tests: carrier lock books one package; follower lock books none and writes only its inbox row; a redelivered follower lock changes
  nothing; a follower lock followed by the carrier lock books exactly one package in either order; the handler decodes `consolidatedInto`
  and ignores `mergedAssignmentIds`; a BDD scenario for the merged lock.
