---
id: class-diagram
title: Class diagrams
sidebar_label: Class diagrams
description: "UML class diagrams of the Linehaul Transportation domain types and of the hexagonal ports with the adapters that exist."
---

# Class diagrams

Two diagrams: the domain types, and the ports with the adapters that implement them. Unexported fields are shown where they carry an invariant.

## Domain model

```mermaid
classDiagram
    direction LR
    class Departure {
        +ID id
        +string laneId
        +Kind kind
        +string originNodeId
        +string destinationNodeId
        +time cutoffAt
        +time departsAt
        +time arrivesAt
        +int capacity
        +int booked
        +State state
        +int64 version
        +Book(now)
        +Release(now)
        +ChangeCapacity(capacity, now)
        +Close()
        +RecordDeparture(now)
        +RecordArrival(now)
        +Cancel(reason, now)
    }
    class LaneSchedule {
        +string laneId
        +State state
        +Slot[] slots
        +int64 version
        +Redefine(slots)
        +Retire()
    }
    class Slot {
        +Weekday day
        +string cutoff
        +string departs
        +string arrives
        +int capacityPackages
    }
    class Load {
        +ID id
        +departure.ID departureId
        +Kind kind
        +int capacity
        +string[] packages
        +State state
        +int64 version
        +AddPackage(tracking, otherActive, depState)
        +Close(depState, now)
        +Dispatch(now)
        +Receive(now)
    }
    class ExpectedVolume {
        +string assignmentId
        +Facts facts
        +int releaseNo
        +departure.ID bookedOn
        +string[] trackings
        +State state
        +Release(f, no)
        +Lock(f)
        +Book(on)
        +Rebook(from, to, now)
        +Fail(on, reason, now)
        +AddTracking(tn)
    }
    class NodeView {
        +string id
        +NodeType type
        +Operator operator
        +string timezone
        +Status status
        +Profile profile
        +PickupEligible()
    }
    class LaneView {
        +string id
        +string origin
        +string destination
        +Mode mode
        +int transitMinutes
        +bool open
    }
    class Event {
        <<interface>>
        +EventName()
        +Entity()
        +SubjectID()
    }
    LaneSchedule "1" *-- "1..100" Slot
    Load "0..1" --> "1" Departure : departureId
    ExpectedVolume "0..1" --> "0..1" Departure : bookedOn
    Departure --> LaneView : laneId
    Departure --> NodeView : origin, destination
    Departure ..> Event : raises
    Load ..> Event : raises
    ExpectedVolume ..> Event : raises
```

Source: `internal/domain/departure/departure.go`, `internal/domain/schedule/schedule.go`, `internal/domain/load/load.go`, `internal/domain/volume/volume.go`,
`internal/domain/network/node.go`, `lane.go`, `internal/domain/shared/shared.go`.

Enumerations: `departure.Kind` = `PICKUP`, `LINEHAUL`, `AIR`; `departure.State` = `SCHEDULED`, `CLOSED`, `DEPARTED`, `ARRIVED`, `CANCELLED`; `load.State` = `OPEN`, `CLOSED`, `DEPARTED`, `ARRIVED`;
`schedule.State` = `ACTIVE`, `RETIRED`; `volume.State` = `UNKNOWN`, `RELEASED`, `BOOKED`, `FAILED`, `CANCELLED`; `network.Mode` = `GROUND`, `AIR`; `network.Operator` = `NETWORK`, `VENDOR`.
Pure planning functions: `schedule.PlanLane`, `schedule.PlanPickups`. Domain events: `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`, `DepartureDeparted`, `DepartureArrived`, `LoadClosed`, `LoadDeparted`, `LoadArrived`,
`BookingMoved`, `BookingFailed`.

## Ports and adapters

```mermaid
classDiagram
    direction LR
    class Service {
        <<usecases>>
        +DefineLaneSchedule()
        +RetireLaneSchedule()
        +CancelDeparture()
        +ChangeDepartureCapacity()
        +ListDepartures()
        +GetDeparture()
        +OpenLoad()
        +AddPackage()
        +CloseLoad()
        +DispatchLoad()
        +ReceiveLoad()
        +GenerateDepartures()
        +Handle...()
    }
    class UnitOfWork {
        <<port>>
    }
    class DepartureRepository {
        <<port>>
    }
    class LaneScheduleRepository {
        <<port>>
    }
    class LoadRepository {
        <<port>>
    }
    class NodeViewRepository {
        <<port>>
    }
    class LaneViewRepository {
        <<port>>
    }
    class VolumeRepository {
        <<port>>
    }
    class ProcessedEvents {
        <<port>>
    }
    class EventOutbox {
        <<port>>
    }
    class Clock {
        <<port>>
    }
    class IDGenerator {
        <<port>>
    }
    class LoadMetrics {
        <<port>>
    }
    class HTTPServer {
        <<inbound adapter>>
    }
    class KafkaConsumers {
        <<inbound adapter>>
    }
    class MCPServer {
        <<inbound adapter>>
    }
    class A2AExecutor {
        <<inbound adapter>>
    }
    class Postgres {
        <<outbound adapter>>
    }
    class Memory {
        <<test double>>
    }
    class OutboxRelay {
        <<outbound adapter>>
    }
    class KafkaSink {
        <<outbound adapter>>
    }
    class Telemetry {
        <<outbound adapter>>
    }
    HTTPServer --> Service
    KafkaConsumers --> Service
    MCPServer --> Service : reads only
    A2AExecutor --> Service : reads only
    Service --> UnitOfWork
    Service --> DepartureRepository
    Service --> LaneScheduleRepository
    Service --> LoadRepository
    Service --> NodeViewRepository
    Service --> LaneViewRepository
    Service --> VolumeRepository
    Service --> ProcessedEvents
    Service --> EventOutbox
    Service --> Clock
    Service --> IDGenerator
    Service --> LoadMetrics
    Postgres ..|> UnitOfWork
    Postgres ..|> DepartureRepository
    Postgres ..|> LaneScheduleRepository
    Postgres ..|> LoadRepository
    Postgres ..|> NodeViewRepository
    Postgres ..|> LaneViewRepository
    Postgres ..|> VolumeRepository
    Postgres ..|> ProcessedEvents
    Postgres ..|> EventOutbox
    Memory ..|> DepartureRepository
    Telemetry ..|> LoadMetrics
    OutboxRelay --> Postgres : drains outbox_events
    OutboxRelay --> KafkaSink
```

Source: `internal/application/ports/ports.go` (the port interfaces), `internal/application/usecases/service.go` (`Deps`), `cmd/api/main.go` (`buildApp`), `cmd/mcp/main.go`, `cmd/a2a/main.go`,
`internal/adapters/**`. `Clock` and `IDGenerator` are implemented by `outbound/clock` and `outbound/ids`; `Memory` stands for `outbound/memory`, which implements all repository ports for tests only and is never
imported by `cmd/**`. `cmd/mcp` and `cmd/a2a` give the `Service` only the departure repository.
