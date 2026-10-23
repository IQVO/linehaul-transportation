package load

import (
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

const entity = "load"

// LoadClosed is raised by Close (analytics only).
type LoadClosed struct {
	shared.Header
	LoadID       ID
	DepartureID  departure.ID
	PackageCount int
}

// EventName returns "LoadClosed".
func (LoadClosed) EventName() string { return "LoadClosed" }

// Entity returns "load".
func (LoadClosed) Entity() string { return entity }

// SubjectID returns the loadId.
func (e LoadClosed) SubjectID() string { return string(e.LoadID) }

// LoadDeparted is raised by Dispatch. DepartureKind and NodeType are gap G3:
// NodeType is the type of the origin node, where the load departed.
type LoadDeparted struct {
	shared.Header
	LoadID          ID
	DepartureID     departure.ID
	OriginNodeID    string
	TrackingNumbers []string
	DepartedAt      time.Time
	DepartureKind   departure.Kind
	NodeType        network.NodeType
}

// EventName returns "LoadDeparted".
func (LoadDeparted) EventName() string { return "LoadDeparted" }

// Entity returns "load".
func (LoadDeparted) Entity() string { return entity }

// SubjectID returns the loadId.
func (e LoadDeparted) SubjectID() string { return string(e.LoadID) }

// LoadArrived is raised by Receive. NodeType is the type of the destination
// node, where the load arrived.
type LoadArrived struct {
	shared.Header
	LoadID            ID
	DepartureID       departure.ID
	DestinationNodeID string
	TrackingNumbers   []string
	ArrivedAt         time.Time
	DepartureKind     departure.Kind
	NodeType          network.NodeType
}

// EventName returns "LoadArrived".
func (LoadArrived) EventName() string { return "LoadArrived" }

// Entity returns "load".
func (LoadArrived) Entity() string { return entity }

// SubjectID returns the loadId.
func (e LoadArrived) SubjectID() string { return string(e.LoadID) }

var (
	_ shared.Event = LoadClosed{}
	_ shared.Event = LoadDeparted{}
	_ shared.Event = LoadArrived{}
)
