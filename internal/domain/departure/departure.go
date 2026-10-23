// Package departure is the Departure aggregate: one scheduled trip on one lane
// with a cutoff, a departure and an arrival instant and a package capacity.
// A PICKUP departure starts at a vendor node.
//
// States: SCHEDULED, CLOSED, DEPARTED, ARRIVED, CANCELLED. Transitions:
// SCHEDULED -> CLOSED (CloseDeparture), CLOSED -> DEPARTED (RecordDeparture),
// DEPARTED -> ARRIVED (RecordArrival), SCHEDULED -> CANCELLED (CancelDeparture).
// BookCapacity, ReleaseCapacity and ChangeDepartureCapacity change the booked
// count or the capacity, not the state. Invariants: cutoffAt <= departsAt <
// arrivesAt (ErrInvalidDepartureTimes), booked <= capacity (ErrDepartureFull),
// the lane is OPEN when scheduling (ErrLaneClosed), no booking after cutoffAt
// (ErrDepartureClosed).
package departure

import (
	"fmt"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// Departure is the aggregate root.
type Departure struct {
	id          ID
	laneID      string
	kind        Kind
	origin      string
	destination string
	originType  network.NodeType
	destType    network.NodeType
	cutoffAt    time.Time
	departsAt   time.Time
	arrivesAt   time.Time
	capacity    int
	booked      int
	state       State
	cancelWhy   string
	cancelledAt time.Time
	departedAt  time.Time
	arrivedAt   time.Time
	version     int64
}

func validTimes(cutoff, departs, arrives time.Time) bool {
	return !cutoff.IsZero() && !cutoff.After(departs) && departs.Before(arrives)
}

func validateRefs(id ID, lane, origin, dest string, k Kind, ot, dt network.NodeType) error {
	for _, f := range []struct{ name, v string }{{"departureId", string(id)}, {"laneId", lane}, {"originNodeId", origin}, {"destinationNodeId", dest}} {
		if !shared.ValidID(f.v) {
			return shared.Invalid("%s %q is not a valid id", f.name, f.v)
		}
	}
	if !k.Valid() {
		return shared.Invalid("kind %q is not PICKUP, LINEHAUL or AIR", k)
	}
	if !ot.Valid() || !dt.Valid() {
		return shared.Invalid("node types %q and %q must be known", ot, dt)
	}
	return nil
}

// Schedule creates a SCHEDULED departure at version 1 and raises
// DepartureScheduled. laneOpen is the LaneView answer for spec.LaneID.
func Schedule(spec Spec, laneOpen bool, now time.Time) (*Departure, []shared.Event, error) {
	if err := validateRefs(spec.ID, spec.LaneID, spec.OriginNodeID, spec.DestinationNodeID, spec.Kind, spec.OriginType, spec.DestinationType); err != nil {
		return nil, nil, err
	}
	if spec.CapacityPackages < 0 || spec.CapacityPackages > shared.MaxInt32 {
		return nil, nil, shared.Invalid("capacityPackages must be between 0 and %d", shared.MaxInt32)
	}
	if !validTimes(spec.CutoffAt, spec.DepartsAt, spec.ArrivesAt) {
		return nil, nil, fmt.Errorf("%w: cutoffAt %s, departsAt %s, arrivesAt %s", ErrInvalidDepartureTimes,
			spec.CutoffAt.Format(time.RFC3339), spec.DepartsAt.Format(time.RFC3339), spec.ArrivesAt.Format(time.RFC3339))
	}
	if !laneOpen {
		return nil, nil, fmt.Errorf("%w: %s", ErrLaneClosed, spec.LaneID)
	}
	d := &Departure{
		id: spec.ID, laneID: spec.LaneID, kind: spec.Kind, origin: spec.OriginNodeID, destination: spec.DestinationNodeID,
		originType: spec.OriginType, destType: spec.DestinationType,
		cutoffAt: spec.CutoffAt.UTC(), departsAt: spec.DepartsAt.UTC(), arrivesAt: spec.ArrivesAt.UTC(),
		capacity: spec.CapacityPackages, state: StateScheduled, version: 1,
	}
	ev := DepartureScheduled{
		Header: d.header(now), DepartureID: d.id, LaneID: d.laneID, Kind: d.kind, OriginNodeID: d.origin,
		DestinationNodeID: d.destination, CutoffAt: d.cutoffAt, DepartsAt: d.departsAt, ArrivesAt: d.arrivesAt,
		CapacityPackages: d.capacity,
	}
	return d, []shared.Event{ev}, nil
}

func (d *Departure) header(now time.Time) shared.Header {
	return shared.Header{Version: d.version, At: now.UTC()}
}

func (d *Departure) illegal(command string) error {
	return fmt.Errorf("%w: %s on departure %s in state %s", shared.ErrIllegalTransition, command, d.id, d.state)
}

func (d *Departure) capacityEvent(now time.Time) shared.Event {
	return DepartureCapacityChanged{
		Header: d.header(now), DepartureID: d.id, CapacityPackages: d.capacity, BookedPackages: d.booked,
		RemainingPackages: d.capacity - d.booked,
	}
}

// Book books one package (BookCapacity) and raises DepartureCapacityChanged.
// It needs a SCHEDULED departure and now <= cutoffAt (ErrDepartureClosed) and a
// free package (ErrDepartureFull).
func (d *Departure) Book(now time.Time) ([]shared.Event, error) {
	if d.state != StateScheduled || now.After(d.cutoffAt) {
		return nil, fmt.Errorf("%w: departure %s is %s, cutoff %s", ErrDepartureClosed, d.id, d.state, d.cutoffAt.Format(time.RFC3339))
	}
	if d.booked+1 > d.capacity {
		return nil, fmt.Errorf("%w: departure %s has %d of %d packages booked", ErrDepartureFull, d.id, d.booked, d.capacity)
	}
	d.booked++
	d.version++
	return []shared.Event{d.capacityEvent(now)}, nil
}

// Release releases one booked package (ReleaseCapacity). It is allowed on a
// SCHEDULED or a CANCELLED departure; on a cancelled one it raises no event
// (ADR 0002 A15). Nothing booked is an illegal transition.
func (d *Departure) Release(now time.Time) ([]shared.Event, error) {
	if d.state != StateScheduled && d.state != StateCancelled {
		return nil, d.illegal("ReleaseCapacity")
	}
	if d.booked == 0 {
		return nil, fmt.Errorf("%w: ReleaseCapacity on departure %s with nothing booked", shared.ErrIllegalTransition, d.id)
	}
	d.booked--
	d.version++
	if d.state == StateCancelled {
		return nil, nil
	}
	return []shared.Event{d.capacityEvent(now)}, nil
}

// ChangeCapacity sets the package capacity (ChangeDepartureCapacity) of a
// SCHEDULED departure. Below the booked count is ErrDepartureFull; the same
// capacity changes nothing and raises nothing.
func (d *Departure) ChangeCapacity(capacity int, now time.Time) ([]shared.Event, error) {
	if d.state != StateScheduled {
		return nil, d.illegal("ChangeDepartureCapacity")
	}
	if capacity < 0 || capacity > shared.MaxInt32 {
		return nil, shared.Invalid("capacityPackages must be between 0 and %d", shared.MaxInt32)
	}
	if capacity < d.booked {
		return nil, fmt.Errorf("%w: capacity %d is below the %d booked packages of departure %s", ErrDepartureFull, capacity, d.booked, d.id)
	}
	if capacity == d.capacity {
		return nil, nil
	}
	d.capacity = capacity
	d.version++
	return []shared.Event{d.capacityEvent(now)}, nil
}

// Close moves SCHEDULED to CLOSED (CloseDeparture). It raises no event.
func (d *Departure) Close() error {
	if d.state != StateScheduled {
		return d.illegal("CloseDeparture")
	}
	d.state = StateClosed
	d.version++
	return nil
}

// RecordDeparture moves CLOSED to DEPARTED and raises DepartureDeparted.
func (d *Departure) RecordDeparture(now time.Time) ([]shared.Event, error) {
	if d.state != StateClosed {
		return nil, d.illegal("RecordDeparture")
	}
	d.state = StateDeparted
	d.departedAt = now.UTC()
	d.version++
	return []shared.Event{DepartureDeparted{Header: d.header(now), DepartureID: d.id, DepartedAt: d.departedAt}}, nil
}

// RecordArrival moves DEPARTED to ARRIVED and raises DepartureArrived.
func (d *Departure) RecordArrival(now time.Time) ([]shared.Event, error) {
	if d.state != StateDeparted {
		return nil, d.illegal("RecordArrival")
	}
	d.state = StateArrived
	d.arrivedAt = now.UTC()
	d.version++
	return []shared.Event{DepartureArrived{Header: d.header(now), DepartureID: d.id, ArrivedAt: d.arrivedAt}}, nil
}

// Cancel moves SCHEDULED to CANCELLED (CancelDeparture) and raises
// DepartureCancelled. The reason is a trimmed text of 1 to 200 characters.
func (d *Departure) Cancel(reason string, now time.Time) ([]shared.Event, error) {
	if d.state != StateScheduled {
		return nil, d.illegal("CancelDeparture")
	}
	if !shared.ValidText(reason) {
		return nil, shared.Invalid("reason must be 1 to %d characters, trimmed, without control characters", shared.MaxText)
	}
	d.state = StateCancelled
	d.cancelWhy = reason
	d.cancelledAt = now.UTC()
	d.version++
	return []shared.Event{DepartureCancelled{Header: d.header(now), DepartureID: d.id, Reason: reason, CancelledAt: d.cancelledAt}}, nil
}

// Accessors.

// ID returns the departureId.
func (d *Departure) ID() ID { return d.id }

// LaneID returns the lane the departure runs on.
func (d *Departure) LaneID() string { return d.laneID }

// Kind returns the departure kind.
func (d *Departure) Kind() Kind { return d.kind }

// OriginNodeID returns the origin node.
func (d *Departure) OriginNodeID() string { return d.origin }

// DestinationNodeID returns the destination node.
func (d *Departure) DestinationNodeID() string { return d.destination }

// OriginType returns the node type of the origin.
func (d *Departure) OriginType() network.NodeType { return d.originType }

// DestinationType returns the node type of the destination.
func (d *Departure) DestinationType() network.NodeType { return d.destType }

// CutoffAt returns the cutoff instant.
func (d *Departure) CutoffAt() time.Time { return d.cutoffAt }

// State returns the lifecycle state.
func (d *Departure) State() State { return d.state }

// Capacity returns the package capacity.
func (d *Departure) Capacity() int { return d.capacity }

// Booked returns the booked packages.
func (d *Departure) Booked() int { return d.booked }

// Remaining returns capacity minus booked.
func (d *Departure) Remaining() int { return d.capacity - d.booked }

// Version returns the optimistic-lock version.
func (d *Departure) Version() int64 { return d.version }

// Snapshot returns the persisted form.
func (d *Departure) Snapshot() Snapshot {
	return Snapshot{
		ID: d.id, LaneID: d.laneID, Kind: d.kind, OriginNodeID: d.origin, DestinationNodeID: d.destination,
		OriginType: d.originType, DestinationType: d.destType, CutoffAt: d.cutoffAt, DepartsAt: d.departsAt, ArrivesAt: d.arrivesAt,
		CapacityPackages: d.capacity, BookedPackages: d.booked, State: d.state, CancelReason: d.cancelWhy,
		CancelledAt: d.cancelledAt, DepartedAt: d.departedAt, ArrivedAt: d.arrivedAt, Version: d.version,
	}
}
