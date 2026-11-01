---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
description: "Domain message flow modelling for Linehaul Transportation: the key scenarios as numbered command, event and query flows across contexts."
---

# Domain message flow

Following [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling): commands are solid, events are dashed, queries are dotted; numbers give the order. Only messages
that exist in the code are drawn.

## Scenario 1: a vendor declares cutoffs and a package is booked on the pickup

```mermaid
flowchart LR
    NT[node-topology]
    LH[linehaul-transportation]
    OS[order-sourcing]
    ST[shipping-and-tracking]
    NT -. "1 NodeRegistered, NodeOperationalProfileDeclared, NodeActivated, LaneOpened" .-> LH
    LH -. "2 DepartureScheduled per cutoff of the horizon" .-> OS
    OS -. "3 FulfillmentReleasedToNode with pickupDepartureId" .-> LH
    OS -. "4 FulfillmentLocked" .-> LH
    LH -. "5 DepartureCapacityChanged" .-> OS
    ST -. "6 LabelIssued with trackingNumber" .-> LH
```

Source: `internal/application/usecases/consumers.go`, `generate.go`, `booking.go`; consumers in `order-sourcing/internal/adapters/inbound/kafka/handlers.go`.

1. node-topology publishes the node, its profile with cutoffs and an outbound lane. This context stores the views and plans one PICKUP departure per cutoff for the next 14 days.
2. Each new departure is announced as `DepartureScheduled`. order-sourcing and delivery-promise project it.
3. order-sourcing releases an assignment to the node, naming the pickup departure. This context records an `ExpectedVolume` (RELEASED).
4. At the lock, the context books one package on the pickup departure (`BookOnLock`).
5. The new booked count is published as `DepartureCapacityChanged`.
6. When the label is issued, the tracking number is attached to the expected volume.

## Scenario 2: a departure is cancelled and bookings move

```mermaid
flowchart LR
    PL[Planner]
    NT[node-topology]
    LH[linehaul-transportation]
    OS[order-sourcing]
    DP[delivery-promise]
    PL -- "1 CancelDeparture (REST)" --> LH
    NT -. "1' NodeSuspended or NodeRetired" .-> LH
    LH -. "2 DepartureCancelled" .-> DP
    LH -. "2 DepartureCancelled" .-> OS
    LH -. "3 BookingMoved or BookingFailed per assignment" .-> OS
    LH -. "4 DepartureCapacityChanged of the new pickup" .-> DP
```

Source: `internal/application/usecases/departures.go`, `booking.go`, `consumers.go` (`leaveService`).

1. A planner cancels a SCHEDULED departure, or node-topology suspends or retires a vendor node (all its future pickups are cancelled).
2. `DepartureCancelled` lets delivery-promise revise promises that rested on it.
3. Each assignment booked on it is rebooked on the next pickup from the same origin (`BookingMoved`) or fails (`BookingFailed`).
4. The receiving pickup's capacity change is published.

## Scenario 3: a load travels

```mermaid
flowchart LR
    OP[Operator]
    LH[linehaul-transportation]
    ST[shipping-and-tracking]
    LM[last-mile-delivery]
    OP -- "1 OpenLoad" --> LH
    OP -- "2 AddPackage per tracking number" --> LH
    OP -- "3 CloseLoad" --> LH
    OP -- "4 DispatchLoad" --> LH
    LH -. "5 LoadDeparted" .-> ST
    OP -- "6 ReceiveLoad" --> LH
    LH -. "7 LoadArrived" .-> ST
    LH -. "7 LoadArrived" .-> LM
```

Source: `internal/application/usecases/loads.go`; consumers in `shipping-and-tracking/internal/adapters/inbound/kafka/handlers.go` and `last-mile-delivery/internal/adapters/inbound/kafka/consumers.go`.

1 to 3 are silent for other contexts (`LoadClosed` goes to the analytics topic). 4 records the departure of the trip and publishes `LoadDeparted` with the tracking numbers. 6 records the arrival at the destination node and
publishes `LoadArrived`, which last-mile-delivery uses to induct packages when the destination is a delivery station.

## Scenario 4: reading departures

```mermaid
flowchart LR
    CL[Any context, console, agents]
    LH[linehaul-transportation]
    CL -- "1 GET /departures or list_departures" --> LH
    LH -. "2 departures with remaining capacity" .-> CL
    CL -- "3 GET /departures/{departureId} or get_departure" --> LH
```

Source: `internal/adapters/inbound/http`, `internal/adapters/inbound/mcp`, `internal/adapters/inbound/a2a`. No sibling context is known to call these routes today; they are the Open Host Service for any context and for agents.
