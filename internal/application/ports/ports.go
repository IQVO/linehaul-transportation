// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// two sentinels of errors.go and the filters of internal/application/repository.
//
// Optimistic concurrency: every Save of a versioned aggregate (Departure,
// LaneSchedule, Load, ExpectedVolume) expects the stored version to be
// aggregate.Version()-1; a create (Version() 1) expects that the id is not
// stored. Any mismatch, including an update of a row that no longer exists, is
// ErrConcurrentUpdate. The two copies of node-topology facts (NodeView,
// LaneView) are plain upserts: a single consumer writes them. Inside a
// UnitOfWork every repository joins the transaction carried by the ctx the
// unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// DepartureRepository persists Departure aggregates.
type DepartureRepository interface {
	// Get returns the departure, or ErrNotFound.
	Get(ctx context.Context, id departure.ID) (*departure.Departure, error)
	// List returns the departures matching filter in the order of the filter's
	// comment; no match is an empty list, never an error.
	List(ctx context.Context, filter repository.DepartureFilter) ([]*departure.Departure, error)
	// Save persists the departure under the concurrency rule of the package
	// comment.
	Save(ctx context.Context, d *departure.Departure) error
}

// LaneScheduleRepository persists LaneSchedule aggregates keyed by laneId.
type LaneScheduleRepository interface {
	// Get returns the schedule of the lane, or ErrNotFound.
	Get(ctx context.Context, laneID string) (*schedule.LaneSchedule, error)
	// ListActive returns every ACTIVE schedule ordered by laneId.
	ListActive(ctx context.Context) ([]*schedule.LaneSchedule, error)
	// Save persists the schedule under the concurrency rule of the package comment.
	Save(ctx context.Context, s *schedule.LaneSchedule) error
}

// LoadRepository persists Load aggregates.
type LoadRepository interface {
	// Get returns the load, or ErrNotFound.
	Get(ctx context.Context, id load.ID) (*load.Load, error)
	// GetByDeparture returns the load of a departure, or ErrNotFound.
	GetByDeparture(ctx context.Context, id departure.ID) (*load.Load, error)
	// FindActiveByTracking returns the id of the OPEN or CLOSED load that holds
	// the tracking number, or ErrNotFound.
	FindActiveByTracking(ctx context.Context, trackingNumber string) (load.ID, error)
	// Save persists the load under the concurrency rule of the package
	// comment. Saving a load whose tracking number is on another OPEN or
	// CLOSED load is load.ErrPackageAlreadyLoaded (unique index); a second
	// load for the same departure is load.ErrLoadAlreadyExists (unique index).
	Save(ctx context.Context, l *load.Load) error
}

// NodeViewRepository stores NodeView, the local copy of node-topology's nodes.
type NodeViewRepository interface {
	// Get returns the node view, or ErrNotFound.
	Get(ctx context.Context, id string) (network.NodeView, error)
	// ListPickupEligible returns the views for which PickupEligible is true,
	// ordered by node id.
	ListPickupEligible(ctx context.Context) ([]network.NodeView, error)
	// Save inserts or replaces the view.
	Save(ctx context.Context, n network.NodeView) error
}

// LaneViewRepository stores LaneView, the local copy of node-topology's lanes.
type LaneViewRepository interface {
	// Get returns the lane view, or ErrNotFound.
	Get(ctx context.Context, id string) (network.LaneView, error)
	// ListByOrigin returns every lane (open and closed) that leaves the node,
	// ordered by lane id.
	ListByOrigin(ctx context.Context, originNodeID string) ([]network.LaneView, error)
	// ListByDestination returns every lane (open and closed) that ends at the
	// node, ordered by lane id.
	ListByDestination(ctx context.Context, destinationNodeID string) ([]network.LaneView, error)
	// Save inserts or replaces the view.
	Save(ctx context.Context, l network.LaneView) error
}

// VolumeRepository persists ExpectedVolume rows keyed by assignmentId.
type VolumeRepository interface {
	// Get returns the row, or ErrNotFound.
	Get(ctx context.Context, assignmentID string) (*volume.ExpectedVolume, error)
	// FindByTracking returns the row that holds the tracking number, or ErrNotFound.
	FindByTracking(ctx context.Context, trackingNumber string) (*volume.ExpectedVolume, error)
	// ListBookedOn returns the BOOKED rows whose booking sits on the departure,
	// ordered by assignment id.
	ListBookedOn(ctx context.Context, id departure.ID) ([]*volume.ExpectedVolume, error)
	// Save persists the row under the concurrency rule of the package comment.
	Save(ctx context.Context, v *volume.ExpectedVolume) error
}

// ProcessedEvents is the inbox: it claims a CloudEvents id for one consumer.
type ProcessedEvents interface {
	// Claim records (consumer, eventID) and reports whether this call was the
	// first to do so. A false answer means the event was handled before.
	Claim(ctx context.Context, consumer, eventID string, at time.Time) (first bool, err error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls, so the aggregates and their raised events commit or roll back
// together. The adapter encodes each event into its CloudEvents envelope (the
// id is minted once, at append) and stores one row per event in order; the
// relay drains the rows to Kafka afterwards. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or
// rolls back together when fn returns an error (or panics).
//
//   - fn MUST use the ctx passed to it for every call that should participate;
//     the unit travels in that ctx.
//   - Do is re-entrant: when ctx already carries an open unit, Do joins it and
//     the outermost Do owns commit and rollback.
//   - The error fn returns is returned from Do unchanged (errors.Is keeps
//     working); a commit failure is returned wrapped.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the opaque ids of new loads.
type IDGenerator interface {
	NewLoadID() load.ID
}
