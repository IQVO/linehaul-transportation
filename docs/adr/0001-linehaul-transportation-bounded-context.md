# 1. Linehaul Transportation is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: retail-network-docs `model/model.yaml`, context `linehaul-transportation`, and its ADRs RN-0001 and RN-0007.

## Context

The retail network is modelled as ten bounded contexts, each its own service and database. Runs the middle mile: turns lanes into scheduled departures (including a pickup departure at every vendor node cutoff), keeps each departure's remaining capacity, builds loads of packages onto departures, and reports when loads depart and arrive.

## Decision

- This repo owns the aggregates Departure, LaneSchedule, Load; the invariants and error names are those of the model.
- Business decisions of the context:
- Every declared cutoff of an ACTIVE vendor fulfillment node gets one pickup departure per operating day.
- A departure's booked packages never exceed its capacity.
- A package is on at most one open load at a time.
- A cancelled departure is announced before its cutoff wherever possible, so promises can be revised.
- Events use the network CloudEvents namespace `com.retailnetwork.logistics.linehaul-transportation.<entity>.<Event>` on `retailnetwork.linehaul-transportation.events`
  (analytics-only events on `.analytics`). Published: DepartureScheduled, DepartureCapacityChanged, DepartureCancelled, DepartureDeparted, DepartureArrived, BookingMoved, BookingFailed, LoadClosed, LoadDeparted, LoadArrived (ADR 0004). No `warehouse.*` topic is consumed or produced.
- No live REST lookup into sibling contexts except the calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet. PII exists only in customer-ordering and shipping-and-tracking.

## Consequences

Relationships and the consumed events are in ADR 0003; the contracts are `apis/openapi.yaml` and `apis/asyncapi.yaml`; the runtime is described in ADR 0005 to 0007 and `docs/runtime.md`.
