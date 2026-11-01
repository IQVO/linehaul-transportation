---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
description: "Design-level EventStorming of Linehaul Transportation: commands, events, policies, aggregates and hotspots."
---

# EventStorming

A design-level storm following the [EventStorming glossary](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet): orange events, blue commands, lilac policies, yellow aggregates, pink external systems,
red hotspots. Chronology runs left to right; the diagram is read as "command, aggregate, event, policy, next command".

```mermaid
flowchart LR
    classDef event fill:#f6a35c,stroke:#8a4b08,color:#000
    classDef command fill:#6fa8dc,stroke:#1c4587,color:#000
    classDef policy fill:#b4a7d6,stroke:#351c75,color:#000
    classDef agg fill:#ffe599,stroke:#7f6000,color:#000
    classDef ext fill:#f4a6c0,stroke:#741b47,color:#000
    classDef hot fill:#e06666,stroke:#660000,color:#000

    NT["node-topology"]:::ext
    OS["order-sourcing"]:::ext
    ST["shipping-and-tracking"]:::ext

    E1(["NodeActivated, ProfileDeclared, LaneOpened"]):::event
    P1{{"PickupFromCutoffs"}}:::policy
    C1["DefineLaneSchedule"]:::command
    A2("LaneSchedule"):::agg
    P2{{"HorizonRoll"}}:::policy
    C2["ScheduleDeparture"]:::command
    A1("Departure"):::agg
    E2(["DepartureScheduled"]):::event

    E3(["FulfillmentLocked"]):::event
    P3{{"BookOnLock"}}:::policy
    C3["Book"]:::command
    E4(["DepartureCapacityChanged"]):::event
    E5(["BookingMoved"]):::event
    E6(["BookingFailed"]):::event
    H1["Hotspot: BookingFailed has no follow-up here"]:::hot

    C4["CancelDeparture"]:::command
    E7(["DepartureCancelled"]):::event

    C5["OpenLoad, AddPackage"]:::command
    A3("Load"):::agg
    C6["CloseLoad"]:::command
    E8(["LoadClosed"]):::event
    C7["DispatchLoad"]:::command
    E9(["LoadDeparted, DepartureDeparted"]):::event
    C8["ReceiveLoad"]:::command
    E10(["LoadArrived, DepartureArrived"]):::event

    NT --> E1 --> P1 --> C2
    C1 --> A2 --> P2 --> C2 --> A1 --> E2
    OS --> E3 --> P3 --> C3 --> A1
    A1 --> E4
    A1 --> E5
    A1 --> E6 --> H1
    NT --> C4
    C4 --> A1 --> E7
    C5 --> A3 --> C6 --> E8
    C6 --> C7 --> E9 --> C8 --> E10
```

Source: `internal/domain/departure/events.go`, `internal/domain/load/events.go`, `internal/domain/volume/events.go`, `internal/application/usecases/*.go` (policy names in the comments of `generate.go`, `consumers.go`).

## Timeline in words

1. **Network is shaped** (external): node-topology registers nodes, declares profiles with cutoffs, activates vendor fulfillment centres and opens lanes.
2. **Policy `PickupFromCutoffs`**: every cutoff of a pickup-eligible node gets a PICKUP departure for the horizon, and stale unbooked ones are cancelled when the profile changes.
3. **Planner defines a lane schedule**; **policy `HorizonRoll`** turns its slots into LINEHAUL or AIR departures every `GENERATE_INTERVAL`. `DepartureScheduled` is the first event other contexts see.
4. **Order-sourcing locks an assignment**; **policy `BookOnLock`** books one package, or moves the booking, or fails it.
5. **A departure may be cancelled** by the planner or by a node leaving service: its bookings are rebooked.
6. **Operators build the load**: open, add packages, close (the departure closes with it), dispatch (departure leaves), receive (departure arrives).

## Hotspots

| Hotspot | State |
| --- | --- |
| `BookingFailed` is received by order-sourcing but changes no state there; what happens to an assignment that no pickup can hold is not decided in either context | open |
| `FulfillmentCancelled` only updates the expected volume (ADR 0002 A16): it never releases the package booked on a departure | accepted assumption |
| A `LabelVoided` removes the tracking number but keeps the booking (A18) | accepted assumption |
| Outbox backlog, consumer lag and dead letters are not metered | open, see [Observability](/docs/operations/observability) |
| A row past `OUTBOX_MAX_ATTEMPTS` lets later events of the same aggregate overtake it | accepted, see [Runbook](/docs/operations/runbook) |
