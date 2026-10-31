---
id: entity-relationship
title: Entity-relationship diagram
sidebar_label: Entity-relationship
description: "The Postgres schema of Linehaul Transportation created by the embedded migration: tables, keys, constraints and what each table is."
---

# Entity-relationship diagram

The schema created by `internal/adapters/outbound/postgres/migrations/0001_linehaul_schema.up.sql` (ten tables; golang-migrate adds its own `schema_migrations` bookkeeping table). The context owns
its database and no table is shared with another context. A relationship line is drawn only where a foreign key exists; everything else is a value reference.

## Aggregates

```mermaid
erDiagram
    direction LR
    departures {
        TEXT id PK
        TEXT lane_id "value, no FK"
        TEXT kind "PICKUP, LINEHAUL, AIR"
        TEXT origin_node_id
        TEXT destination_node_id
        TEXT origin_type
        TEXT destination_type
        TIMESTAMPTZ cutoff_at
        TIMESTAMPTZ departs_at
        TIMESTAMPTZ arrives_at
        INTEGER capacity_packages "CHECK ge 0"
        INTEGER booked_packages "CHECK le capacity"
        TEXT state "SCHEDULED, CLOSED, DEPARTED, ARRIVED, CANCELLED"
        TEXT cancel_reason
        TIMESTAMPTZ cancelled_at
        TIMESTAMPTZ departed_at
        TIMESTAMPTZ arrived_at
        BIGINT version "optimistic lock"
    }
    lane_schedules {
        TEXT lane_id PK
        TEXT state "ACTIVE, RETIRED"
        JSONB slots "whole array"
        BIGINT version
    }
    loads ||--o{ load_packages : "FK load_id, cascade"
    loads {
        TEXT id PK
        TEXT departure_id UK "one load per departure"
        TEXT kind
        TEXT origin_node_id
        TEXT destination_node_id
        INTEGER capacity_packages
        TEXT state "OPEN, CLOSED, DEPARTED, ARRIVED"
        TIMESTAMPTZ departed_at
        TIMESTAMPTZ arrived_at
        BIGINT version
    }
    load_packages {
        TEXT load_id PK,FK
        INTEGER position PK
        TEXT tracking_number "unique where active"
        BOOLEAN active "OPEN or CLOSED load"
    }
    expected_volumes {
        TEXT assignment_id PK
        TEXT order_id
        INTEGER group_no
        TEXT node_id
        TEXT departure_id "planned pickup"
        INTEGER release_no
        TEXT booked_on "departure id, nullable"
        TEXT state
        TEXT_ARRAY tracking_numbers "GIN index"
        BIGINT version
    }
```

Source: `0001_linehaul_schema.up.sql` (tables `departures`, `lane_schedules`, `loads`, `load_packages`, `expected_volumes`).

`departures` has `CHECK (cutoff_at <= departs_at AND departs_at < arrives_at)` and indexes on `(cutoff_at, id)`, `(origin_node_id, cutoff_at)` and `(destination_node_id, cutoff_at)` for `GET /departures`.
`load_packages` has the partial unique index `load_packages_active_tracking_key (tracking_number) WHERE active`, which enforces "a package is on at most one OPEN or CLOSED load" (`ErrPackageAlreadyLoaded`).
`loads.departure_id` is unique (`loads_departure_id_key`). `lane_id`, `departure_id` and node ids are values, not foreign keys, because the aggregates are separate consistency boundaries
and the nodes and lanes belong to other contexts.

## Local read models

```mermaid
erDiagram
    direction LR
    node_views {
        TEXT id PK
        TEXT type "node type"
        TEXT operator "NETWORK or VENDOR"
        TEXT timezone
        TEXT status
        INTEGER generation
        JSONB profile "NULL until declared"
    }
    lane_views {
        TEXT id PK
        TEXT origin
        TEXT destination
        TEXT mode "GROUND, AIR"
        INTEGER transit_minutes
        BOOLEAN open
    }
```

Source: `0001_linehaul_schema.up.sql` (`node_views`, `lane_views`), ADR 0003. `lane_views` is indexed on `(origin, id)` and `(destination, id)`. Both tables are replaced as a whole from node-topology events.

## Infrastructure

```mermaid
erDiagram
    direction LR
    outbox_events {
        BIGSERIAL id PK
        TEXT event_id "CloudEvents id, unique with topic"
        TEXT topic
        TEXT event_type "full CloudEvents type"
        TEXT subject
        BYTEA key "Kafka key"
        TEXT dataschema
        BYTEA value "structured CloudEvent"
        JSONB headers "content-type, traceparent"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ published_at "NULL until published"
        INTEGER attempts
        TEXT last_error
    }
    processed_events {
        TEXT consumer PK
        TEXT event_id PK
        TIMESTAMPTZ processed_at
    }
    idempotency_keys {
        TEXT route PK
        TEXT key PK
        TEXT request_hash
        INTEGER response_status
        BYTEA response_body
        TIMESTAMPTZ created_at
    }
```

Source: `0001_linehaul_schema.up.sql` (`outbox_events`, `processed_events`, `idempotency_keys`).

- `outbox_events` is written in the same transaction as the aggregate and drained by the relay; the partial index `idx_outbox_events_unpublished (id) WHERE published_at IS NULL` keeps the scan small;
  `(event_id, topic)` is unique so a retry republishes the same id.
- `processed_events` is the inbox: `(consumer, event_id)` is claimed with `INSERT ... ON CONFLICT DO NOTHING` in the same transaction as the effect, so a rollback un-claims it.
- `idempotency_keys` stores the first response of each `(route, key)`; rows older than 24 hours are deleted by every keyed write.

## Table to aggregate

| Table | Aggregate or role | Notes |
| --- | --- | --- |
| `departures` | **Departure** | One row per departure; `version` guards contended bookings |
| `lane_schedules` | **LaneSchedule** | One row per lane; slots in one JSONB array |
| `loads`, `load_packages` | **Load** | Capacity, nodes and kind are snapshots of the departure |
| `expected_volumes` | **ExpectedVolume** | One row per fulfillment assignment |
| `node_views`, `lane_views` | read models | Fed by node-topology events |
| `outbox_events`, `processed_events`, `idempotency_keys` | infrastructure | Outbox, inbox, REST replay store |
