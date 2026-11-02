---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
description: "UML sequence diagrams of the key flows of Linehaul Transportation: define a schedule, horizon roll, book on lock, cancel with rebooking, load lifecycle and outbox relay."
---

# Sequence diagrams

Each diagram follows the code path of one use case ([Use cases](/docs/ddd/use-cases)). Every state change commits aggregate rows and outbox rows in one Postgres transaction, so the diagrams
show `PG` once per transaction.

## 1. Define a lane schedule and generate departures

```mermaid
sequenceDiagram
    autonumber
    actor P as Planner
    participant H as HTTP adapter
    participant K as Idempotency middleware
    participant S as usecases.Service
    participant PG as PostgreSQL
    participant W as Horizon roll
    P->>H: POST /lane-schedules with Idempotency-Key
    H->>K: begin transaction and savepoint
    K->>S: DefineLaneSchedule(laneId, slots)
    S->>PG: read lane_views
    alt lane unknown
        S-->>H: not-found
        H-->>P: 404 problem
    else slots overlap
        S-->>H: overlapping-slots
        H-->>P: 422 problem
    else ok
        S->>PG: save lane_schedules (ACTIVE)
        S-->>K: schedule
        K->>PG: store response, commit
        H-->>P: 201 (200 on replace)
    end
    Note over W,PG: at boot and every GENERATE_INTERVAL
    W->>S: GenerateDepartures
    S->>PG: list ACTIVE schedules, read lane and node views
    S->>PG: for each missing slot save departure and DepartureScheduled outbox row
    W-->>W: log horizon roll scheduled and failed
```

Source: `internal/adapters/inbound/http/server.go`, `idempotency.go`, `internal/application/usecases/schedules.go`, `generate.go`, `cmd/api/main.go` (`horizonRoll`).

## 2. Book on lock, rebook or fail

```mermaid
sequenceDiagram
    autonumber
    participant OS as order-sourcing
    participant KF as Kafka
    participant C as order-sourcing consumer
    participant S as usecases.Service
    participant PG as PostgreSQL
    OS->>KF: FulfillmentLocked (assignmentId, pickupDepartureId, nodeId)
    KF->>C: message
    C->>S: HandleLocked(eventId, input)
    S->>PG: claim processed_events (consumer, eventId)
    alt already claimed
        S-->>C: nothing to do
    else new
        S->>PG: load ExpectedVolume
        S->>PG: load pickup departure
        alt departure has room and is before cutoff
            S->>PG: book one package, save departure and volume
            S->>PG: outbox DepartureCapacityChanged
        else unknown, cancelled, closed or full
            S->>PG: find next SCHEDULED PICKUP from same origin with room
            alt found
                S->>PG: book there, outbox DepartureCapacityChanged and BookingMoved
            else none
                S->>PG: outbox BookingFailed with reason
            end
        end
    end
    C->>KF: commit offset
```

Source: `internal/application/usecases/consumers.go` (`HandleLocked`), `booking.go` (`place`, `rebookOrFail`), `internal/adapters/inbound/kafka/kafka.go`.
A handler error is retried on the same message and dead-lettered after 5 attempts ([Runbook](/docs/operations/runbook)).

## 3. Cancel a departure and rebook its assignments

```mermaid
sequenceDiagram
    autonumber
    actor P as Planner
    participant H as HTTP adapter
    participant S as usecases.Service
    participant PG as PostgreSQL
    P->>H: POST /departures/{departureId}/cancellations with reason
    H->>S: CancelDeparture(id, reason)
    S->>PG: load departure
    alt not SCHEDULED
        S-->>H: illegal-transition
        H-->>P: 409 problem
    else SCHEDULED
        S->>PG: save CANCELLED and outbox DepartureCancelled
        loop each assignment booked on it
            S->>PG: release the package on the cancelled departure
            S->>PG: next pickup with room from same origin
            S->>PG: outbox BookingMoved or BookingFailed
        end
        S-->>H: departure
        H-->>P: 200
    end
```

Source: `internal/application/usecases/departures.go` (`CancelDeparture`), `booking.go` (`cancel`, `moveBookingsOff`). The same path runs for `NodeSuspended`, `NodeRetired` and a revised profile.

## 4. Load lifecycle

```mermaid
sequenceDiagram
    autonumber
    actor O as Operator
    participant H as HTTP adapter
    participant S as usecases.Service
    participant PG as PostgreSQL
    O->>H: POST /loads (departureId)
    H->>S: OpenLoad
    S->>PG: departure must be SCHEDULED and have no load
    S-->>O: 201 load OPEN
    O->>H: POST /loads/{loadId}/packages (trackingNumber)
    H->>S: AddPackage
    S->>PG: tracking number not on another OPEN or CLOSED load, count within capacity
    S->>PG: on LINEHAUL or AIR also book the departure and outbox DepartureCapacityChanged
    S-->>O: 200
    O->>H: POST /loads/{loadId}/closures
    S->>PG: load CLOSED, departure CLOSED, outbox LoadClosed on analytics
    O->>H: POST /loads/{loadId}/dispatches
    S->>PG: load DEPARTED, departure DEPARTED, outbox LoadDeparted and DepartureDeparted
    O->>H: POST /loads/{loadId}/receipts
    S->>PG: load ARRIVED, departure ARRIVED, outbox LoadArrived and DepartureArrived
```

Source: `internal/application/usecases/loads.go`, `internal/domain/load/load.go`. Dispatching an OPEN load answers `409 illegal-transition`; repeating a command that already holds returns the load unchanged.

## 5. Outbox relay

```mermaid
sequenceDiagram
    autonumber
    participant R as Outbox relay
    participant PG as PostgreSQL
    participant KS as Kafka sink
    participant KF as Kafka
    loop every OUTBOX_RELAY_INTERVAL
        R->>PG: claim up to 100 unpublished rows (FOR UPDATE SKIP LOCKED)
        loop each row in id order
            R->>KS: write value with key, content-type and traceparent headers
            KS->>KF: publish to events or analytics topic
            alt published
                R->>PG: set published_at
            else failed
                R->>PG: attempts + 1 and last_error
                Note over R,PG: at OUTBOX_MAX_ATTEMPTS the row is excluded and logged once
            end
        end
        Note over R: a full batch is followed at once by another pass
    end
```

Source: `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`, `internal/adapters/outbound/kafka`.

## 6. Reading through MCP or A2A

```mermaid
sequenceDiagram
    autonumber
    participant A as Agent
    participant M as cmd/mcp or cmd/a2a
    participant S as usecases.Service (departure reads)
    participant PG as PostgreSQL
    A->>M: tools/call list_departures or SendMessage with skill
    M->>S: ListDepartures or GetDeparture
    S->>PG: select from departures
    S-->>M: views
    M-->>A: structured result or in-band problem
```

Source: `cmd/mcp/main.go`, `cmd/a2a/main.go`, `internal/adapters/inbound/mcp`, `internal/adapters/inbound/a2a`. Neither binary can write: the `Service` is built with the departure repository only.
