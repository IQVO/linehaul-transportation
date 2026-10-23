-- linehaul-transportation schema. Time zones: every instant is TIMESTAMPTZ and
-- is read back in UTC; an instant the aggregate has not reached yet is NULL.
-- Enumerations are TEXT with a CHECK, so a wrong value cannot be stored.

-- departures: the Departure aggregate (ADR 0002). booked_packages <=
-- capacity_packages is the capacity invariant, repeated here as a last line of
-- defence.
CREATE TABLE departures (
    id                  TEXT        PRIMARY KEY,
    lane_id             TEXT        NOT NULL,
    kind                TEXT        NOT NULL CHECK (kind IN ('PICKUP', 'LINEHAUL', 'AIR')),
    origin_node_id      TEXT        NOT NULL,
    destination_node_id TEXT        NOT NULL,
    origin_type         TEXT        NOT NULL,
    destination_type    TEXT        NOT NULL,
    cutoff_at           TIMESTAMPTZ NOT NULL,
    departs_at          TIMESTAMPTZ NOT NULL,
    arrives_at          TIMESTAMPTZ NOT NULL,
    capacity_packages   INTEGER     NOT NULL CHECK (capacity_packages >= 0),
    booked_packages     INTEGER     NOT NULL CHECK (booked_packages >= 0 AND booked_packages <= capacity_packages),
    state               TEXT        NOT NULL CHECK (state IN ('SCHEDULED', 'CLOSED', 'DEPARTED', 'ARRIVED', 'CANCELLED')),
    cancel_reason       TEXT,
    cancelled_at        TIMESTAMPTZ,
    departed_at         TIMESTAMPTZ,
    arrived_at          TIMESTAMPTZ,
    version             BIGINT      NOT NULL CHECK (version >= 1),
    CONSTRAINT departures_times_check CHECK (cutoff_at <= departs_at AND departs_at < arrives_at)
);

-- ListDepartures: filters on origin, destination, kind and state, ordered by cutoff_at, id.
CREATE INDEX idx_departures_cutoff ON departures (cutoff_at, id);
CREATE INDEX idx_departures_origin ON departures (origin_node_id, cutoff_at);
CREATE INDEX idx_departures_destination ON departures (destination_node_id, cutoff_at);

-- lane_schedules: the LaneSchedule aggregate, keyed by lane id; the slots are
-- one JSON array (always read and written whole).
CREATE TABLE lane_schedules (
    lane_id TEXT   PRIMARY KEY,
    state   TEXT   NOT NULL CHECK (state IN ('ACTIVE', 'RETIRED')),
    slots   JSONB  NOT NULL,
    version BIGINT NOT NULL CHECK (version >= 1)
);

-- loads: the Load aggregate. A departure has at most ONE load.
CREATE TABLE loads (
    id                  TEXT        PRIMARY KEY,
    departure_id        TEXT        NOT NULL,
    kind                TEXT        NOT NULL,
    origin_node_id      TEXT        NOT NULL,
    destination_node_id TEXT        NOT NULL,
    origin_type         TEXT        NOT NULL,
    destination_type    TEXT        NOT NULL,
    capacity_packages   INTEGER     NOT NULL CHECK (capacity_packages >= 0),
    state               TEXT        NOT NULL CHECK (state IN ('OPEN', 'CLOSED', 'DEPARTED', 'ARRIVED')),
    departed_at         TIMESTAMPTZ,
    arrived_at          TIMESTAMPTZ,
    version             BIGINT      NOT NULL CHECK (version >= 1),
    CONSTRAINT loads_departure_id_key UNIQUE (departure_id)
);

-- load_packages: the tracking numbers of a load, in the order they were added.
-- active is true while the load is OPEN or CLOSED; the partial unique index is
-- the invariant "a package is on at most one active load" (ErrPackageAlreadyLoaded).
CREATE TABLE load_packages (
    load_id         TEXT    NOT NULL REFERENCES loads (id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    tracking_number TEXT    NOT NULL,
    active          BOOLEAN NOT NULL,
    PRIMARY KEY (load_id, position)
);

CREATE UNIQUE INDEX load_packages_active_tracking_key ON load_packages (tracking_number) WHERE active;
CREATE INDEX idx_load_packages_tracking ON load_packages (tracking_number);

-- node_views and lane_views: the local copies of node-topology facts (ADR 0003).
-- The profile is the declared profile document, NULL until one is declared.
CREATE TABLE node_views (
    id         TEXT    PRIMARY KEY,
    type       TEXT    NOT NULL,
    operator   TEXT    NOT NULL,
    timezone   TEXT    NOT NULL,
    status     TEXT    NOT NULL,
    generation INTEGER NOT NULL,
    profile    JSONB
);

CREATE TABLE lane_views (
    id              TEXT    PRIMARY KEY,
    origin          TEXT    NOT NULL,
    destination     TEXT    NOT NULL,
    mode            TEXT    NOT NULL,
    transit_minutes INTEGER NOT NULL,
    open            BOOLEAN NOT NULL
);

CREATE INDEX idx_lane_views_origin ON lane_views (origin, id);
CREATE INDEX idx_lane_views_destination ON lane_views (destination, id);

-- expected_volumes: one row per fulfillment assignment (ExpectedVolume).
-- version is the version of the stored row; the tracking numbers are an array
-- in arrival order, searched through the GIN index.
CREATE TABLE expected_volumes (
    assignment_id    TEXT    PRIMARY KEY,
    order_id         TEXT    NOT NULL,
    group_no         INTEGER NOT NULL,
    node_id          TEXT    NOT NULL,
    departure_id     TEXT    NOT NULL,
    release_no       INTEGER NOT NULL,
    booked_on        TEXT,
    state            TEXT    NOT NULL,
    tracking_numbers TEXT[]  NOT NULL DEFAULT '{}',
    version          BIGINT  NOT NULL CHECK (version >= 1)
);

CREATE INDEX idx_expected_volumes_booked_on ON expected_volumes (booked_on) WHERE booked_on IS NOT NULL;
CREATE INDEX idx_expected_volumes_tracking ON expected_volumes USING GIN (tracking_numbers);


-- outbox_events: the transactional outbox, with the (event_id, topic) identity. One row
-- per already-encoded Kafka message, inserted in the SAME transaction as the
-- aggregate rows; the relay drains unpublished rows to Kafka.
--   event_id    the CloudEvents `id`, minted once at encode time and
--               persisted: a relay retry republishes the same id.
--   event_type  the FULL CloudEvents `type`
--               (com.retailnetwork.logistics.linehaul-transportation.<entity>.<EventName>).
--   subject     the CloudEvents `subject` (the aggregate id).
--   key         the Kafka message key (the aggregate id).
--   dataschema  the CloudEvents `dataschema` URN.
--   value       the structured-mode CloudEvents JSON bytes.
--   headers     [{"key":..,"value":..}] Kafka headers (content-type).
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

-- The relay only ever scans the unpublished tail; keep that scan tiny.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- processed_events: idempotency guard of the inbound consumers. (consumer,
-- event_id) is claimed with INSERT ... ON CONFLICT DO NOTHING in the SAME
-- transaction as the effect, so a rollback un-claims it.
CREATE TABLE processed_events (
    consumer     TEXT        NOT NULL,
    event_id     TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- idempotency_keys: the fleet Idempotency-Key middleware (the REST POSTs).
-- The middleware INSERTs a bare row inside its own transaction before calling
-- the handler, then UPDATEs it with the outcome and commits, all in ONE
-- transaction shared with the use case's own writes, so a COMMITTED row
-- always has a status_code.
CREATE TABLE idempotency_keys (
    key              TEXT PRIMARY KEY,
    method           TEXT        NOT NULL,
    path             TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    status_code      INTEGER,
    response_body    BYTEA,
    response_headers JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
