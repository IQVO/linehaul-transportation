package departure

import (
	"errors"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

// The Departure invariants of the model, by the model's names.
var (
	// ErrInvalidDepartureTimes: cutoffAt <= departsAt < arrivesAt (422).
	ErrInvalidDepartureTimes = errors.New("invalid departure times")
	// ErrDepartureFull: booked <= capacity (409).
	ErrDepartureFull = errors.New("departure full")
	// ErrLaneClosed: the lane is OPEN when scheduling (409).
	ErrLaneClosed = errors.New("lane closed")
	// ErrDepartureClosed: no booking after cutoffAt, and none unless the
	// departure is SCHEDULED (409).
	ErrDepartureClosed = errors.New("departure closed")
)

// ID identifies a departure (departureId).
type ID string

// Kind is the DepartureKind value object.
type Kind string

// The three departure kinds.
const (
	KindPickup   Kind = "PICKUP"
	KindLinehaul Kind = "LINEHAUL"
	KindAir      Kind = "AIR"
)

// Valid reports whether k is PICKUP, LINEHAUL or AIR.
func (k Kind) Valid() bool { return k == KindPickup || k == KindLinehaul || k == KindAir }

// State is the lifecycle state of a departure.
type State string

// The five departure states.
const (
	StateScheduled State = "SCHEDULED"
	StateClosed    State = "CLOSED"
	StateDeparted  State = "DEPARTED"
	StateArrived   State = "ARRIVED"
	StateCancelled State = "CANCELLED"
)

// Valid reports whether s is one of the five states.
func (s State) Valid() bool {
	switch s {
	case StateScheduled, StateClosed, StateDeparted, StateArrived, StateCancelled:
		return true
	}
	return false
}

// Spec is the input of Schedule: everything a new departure needs.
type Spec struct {
	ID                ID
	LaneID            string
	Kind              Kind
	OriginNodeID      string
	DestinationNodeID string
	OriginType        network.NodeType
	DestinationType   network.NodeType
	CutoffAt          time.Time
	DepartsAt         time.Time
	ArrivesAt         time.Time
	CapacityPackages  int
}

// Snapshot is the persisted form of a departure. Restore validates it.
type Snapshot struct {
	ID                ID
	LaneID            string
	Kind              Kind
	OriginNodeID      string
	DestinationNodeID string
	OriginType        network.NodeType
	DestinationType   network.NodeType
	CutoffAt          time.Time
	DepartsAt         time.Time
	ArrivesAt         time.Time
	CapacityPackages  int
	BookedPackages    int
	State             State
	CancelReason      string
	CancelledAt       time.Time
	DepartedAt        time.Time
	ArrivedAt         time.Time
	Version           int64
}
