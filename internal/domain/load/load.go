// Package load is the Load aggregate: the packages physically consolidated
// onto one departure at its origin node.
//
// States: OPEN, CLOSED, DEPARTED, ARRIVED; transitions OPEN -> CLOSED
// (CloseLoad), CLOSED -> DEPARTED (DispatchLoad), DEPARTED -> ARRIVED
// (ReceiveLoad). AddPackage adds a tracking number while OPEN. Invariants: a
// package is on at most one OPEN or CLOSED load (ErrPackageAlreadyLoaded,
// decided with the answer of the repository), the package count never exceeds
// the capacity snapshot of OpenLoad (ErrLoadOverCapacity), a departure has at
// most one load (ErrLoadAlreadyExists, ADR 0002 A19).
package load

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// The Load invariants of the model, by the model's names.
var (
	// ErrPackageAlreadyLoaded: a package is on at most one OPEN or CLOSED load (409).
	ErrPackageAlreadyLoaded = errors.New("package already loaded")
	// ErrLoadOverCapacity: package count <= departure capacity (409).
	ErrLoadOverCapacity = errors.New("load over capacity")
	// ErrLoadAlreadyExists: a departure has at most one load (409).
	ErrLoadAlreadyExists = errors.New("load already exists")
)

// MaxTrackingNumber is the longest accepted tracking number.
const MaxTrackingNumber = 64

// ID identifies a load (loadId).
type ID string

// State is the lifecycle state of a load.
type State string

// The four load states.
const (
	StateOpen     State = "OPEN"
	StateClosed   State = "CLOSED"
	StateDeparted State = "DEPARTED"
	StateArrived  State = "ARRIVED"
)

// Valid reports whether s is one of the four states.
func (s State) Valid() bool {
	return s == StateOpen || s == StateClosed || s == StateDeparted || s == StateArrived
}

// Load is the aggregate root.
type Load struct {
	id          ID
	departureID departure.ID
	kind        departure.Kind
	origin      string
	destination string
	originType  network.NodeType
	destType    network.NodeType
	capacity    int
	packages    []string
	state       State
	departedAt  time.Time
	arrivedAt   time.Time
	version     int64
}

// Snapshot is the persisted form of a load. Restore validates it.
type Snapshot struct {
	ID                ID
	DepartureID       departure.ID
	Kind              departure.Kind
	OriginNodeID      string
	DestinationNodeID string
	OriginType        network.NodeType
	DestinationType   network.NodeType
	CapacityPackages  int
	TrackingNumbers   []string
	State             State
	DepartedAt        time.Time
	ArrivedAt         time.Time
	Version           int64
}

// Open creates an OPEN load for a SCHEDULED departure (OpenLoad), copying kind,
// origin, destination, node types and capacity from it. hasLoad is the
// repository's answer to "does this departure already have a load".
func Open(id ID, d *departure.Departure, hasLoad bool) (*Load, error) {
	if !shared.ValidID(string(id)) {
		return nil, shared.Invalid("loadId %q is not a valid id", id)
	}
	if d.State() != departure.StateScheduled {
		return nil, fmt.Errorf("%w: OpenLoad on departure %s in state %s", shared.ErrIllegalTransition, d.ID(), d.State())
	}
	if hasLoad {
		return nil, fmt.Errorf("%w: departure %s", ErrLoadAlreadyExists, d.ID())
	}
	return &Load{
		id: id, departureID: d.ID(), kind: d.Kind(), origin: d.OriginNodeID(), destination: d.DestinationNodeID(),
		originType: d.OriginType(), destType: d.DestinationType(), capacity: d.Capacity(),
		packages: []string{}, state: StateOpen, version: 1,
	}, nil
}

func (l *Load) illegal(command string) error {
	return fmt.Errorf("%w: %s on load %s in state %s", shared.ErrIllegalTransition, command, l.id, l.state)
}

func (l *Load) header(now time.Time) shared.Header {
	return shared.Header{Version: l.version, At: now.UTC()}
}

// AddPackage adds a tracking number to an OPEN load whose departure is
// SCHEDULED (AddPackage). A tracking number already on this load changes
// nothing and reports added=false. otherActive is the repository's answer to
// "is it on another OPEN or CLOSED load".
func (l *Load) AddPackage(tracking string, otherActive bool, depState departure.State) (added bool, err error) {
	if !shared.ValidTextN(tracking, MaxTrackingNumber) {
		return false, shared.Invalid("trackingNumber must be 1 to %d characters, trimmed, without control characters", MaxTrackingNumber)
	}
	if l.state != StateOpen || depState != departure.StateScheduled {
		return false, fmt.Errorf("%w: AddPackage on load %s in state %s, departure %s", shared.ErrIllegalTransition, l.id, l.state, depState)
	}
	if slices.Contains(l.packages, tracking) {
		return false, nil
	}
	if otherActive {
		return false, fmt.Errorf("%w: %s", ErrPackageAlreadyLoaded, tracking)
	}
	if len(l.packages)+1 > l.capacity {
		return false, fmt.Errorf("%w: load %s holds %d of %d packages", ErrLoadOverCapacity, l.id, len(l.packages), l.capacity)
	}
	l.packages = append(l.packages, tracking)
	l.version++
	return true, nil
}

// Close moves OPEN to CLOSED (CloseLoad) while the departure is SCHEDULED and
// raises LoadClosed.
func (l *Load) Close(depState departure.State, now time.Time) ([]shared.Event, error) {
	if l.state != StateOpen || depState != departure.StateScheduled {
		return nil, fmt.Errorf("%w: CloseLoad on load %s in state %s, departure %s", shared.ErrIllegalTransition, l.id, l.state, depState)
	}
	l.state = StateClosed
	l.version++
	return []shared.Event{LoadClosed{Header: l.header(now), LoadID: l.id, DepartureID: l.departureID, PackageCount: len(l.packages)}}, nil
}

// Dispatch moves CLOSED to DEPARTED (DispatchLoad) and raises LoadDeparted.
func (l *Load) Dispatch(now time.Time) ([]shared.Event, error) {
	if l.state != StateClosed {
		return nil, l.illegal("DispatchLoad")
	}
	l.state = StateDeparted
	l.departedAt = now.UTC()
	l.version++
	return []shared.Event{LoadDeparted{
		Header: l.header(now), LoadID: l.id, DepartureID: l.departureID, OriginNodeID: l.origin,
		TrackingNumbers: slices.Clone(l.packages), DepartedAt: l.departedAt, DepartureKind: l.kind, NodeType: l.originType,
	}}, nil
}

// Receive moves DEPARTED to ARRIVED (ReceiveLoad) and raises LoadArrived.
func (l *Load) Receive(now time.Time) ([]shared.Event, error) {
	if l.state != StateDeparted {
		return nil, l.illegal("ReceiveLoad")
	}
	l.state = StateArrived
	l.arrivedAt = now.UTC()
	l.version++
	return []shared.Event{LoadArrived{
		Header: l.header(now), LoadID: l.id, DepartureID: l.departureID, DestinationNodeID: l.destination,
		TrackingNumbers: slices.Clone(l.packages), ArrivedAt: l.arrivedAt, DepartureKind: l.kind, NodeType: l.destType,
	}}, nil
}

// Active reports whether the load is OPEN or CLOSED, the states in which its
// packages count toward ErrPackageAlreadyLoaded.
func (l *Load) Active() bool { return l.state == StateOpen || l.state == StateClosed }

// ID returns the loadId.
func (l *Load) ID() ID { return l.id }

// DepartureID returns the departure of the load.
func (l *Load) DepartureID() departure.ID { return l.departureID }

// Kind returns the kind of the load's departure.
func (l *Load) Kind() departure.Kind { return l.kind }

// State returns the lifecycle state.
func (l *Load) State() State { return l.state }

// Capacity returns the capacity snapshot taken at OpenLoad.
func (l *Load) Capacity() int { return l.capacity }

// PackageCount returns the number of packages on the load.
func (l *Load) PackageCount() int { return len(l.packages) }

// Version returns the optimistic-lock version.
func (l *Load) Version() int64 { return l.version }

// Snapshot returns the persisted form.
func (l *Load) Snapshot() Snapshot {
	return Snapshot{
		ID: l.id, DepartureID: l.departureID, Kind: l.kind, OriginNodeID: l.origin, DestinationNodeID: l.destination,
		OriginType: l.originType, DestinationType: l.destType, CapacityPackages: l.capacity,
		TrackingNumbers: slices.Clone(l.packages), State: l.state, DepartedAt: l.departedAt, ArrivedAt: l.arrivedAt, Version: l.version,
	}
}

// Restore rebuilds a load from its persisted form and validates it.
func Restore(s Snapshot) (*Load, error) {
	if err := validateSnapshotRefs(s); err != nil {
		return nil, err
	}
	if s.CapacityPackages < 0 || s.CapacityPackages > shared.MaxInt32 || len(s.TrackingNumbers) > s.CapacityPackages {
		return nil, shared.Corrupt("load %s: %d packages, capacity %d", s.ID, len(s.TrackingNumbers), s.CapacityPackages)
	}
	if err := validatePackages(s); err != nil {
		return nil, err
	}
	departed := s.State == StateDeparted || s.State == StateArrived
	if departed != !s.DepartedAt.IsZero() || (s.State == StateArrived) != !s.ArrivedAt.IsZero() {
		return nil, shared.Corrupt("load %s: departedAt/arrivedAt do not match state %s", s.ID, s.State)
	}
	return &Load{
		id: s.ID, departureID: s.DepartureID, kind: s.Kind, origin: s.OriginNodeID, destination: s.DestinationNodeID,
		originType: s.OriginType, destType: s.DestinationType, capacity: s.CapacityPackages,
		packages: append([]string{}, s.TrackingNumbers...), state: s.State,
		departedAt: shared.UTC(s.DepartedAt), arrivedAt: shared.UTC(s.ArrivedAt), version: s.Version,
	}, nil
}

func validateSnapshotRefs(s Snapshot) error {
	for _, f := range []struct{ name, v string }{{"loadId", string(s.ID)}, {"departureId", string(s.DepartureID)}, {"originNodeId", s.OriginNodeID}, {"destinationNodeId", s.DestinationNodeID}} {
		if !shared.ValidID(f.v) {
			return shared.Corrupt("load %s: %s %q", s.ID, f.name, f.v)
		}
	}
	if !s.Kind.Valid() || !s.OriginType.Valid() || !s.DestinationType.Valid() || !s.State.Valid() {
		return shared.Corrupt("load %s: kind %q, types %q %q or state %q", s.ID, s.Kind, s.OriginType, s.DestinationType, s.State)
	}
	if s.Version < 1 {
		return shared.Corrupt("load %s: version %d", s.ID, s.Version)
	}
	return nil
}

func validatePackages(s Snapshot) error {
	seen := map[string]bool{}
	for _, p := range s.TrackingNumbers {
		if !shared.ValidTextN(p, MaxTrackingNumber) || seen[p] {
			return shared.Corrupt("load %s: tracking number %q is invalid or repeated", s.ID, p)
		}
		seen[p] = true
	}
	return nil
}
