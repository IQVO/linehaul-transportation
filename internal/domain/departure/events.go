package departure

import (
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

const entity = "departure"

// DepartureScheduled is raised by Schedule. Its fields are the model's data list.
type DepartureScheduled struct {
	shared.Header
	DepartureID       ID
	LaneID            string
	Kind              Kind
	OriginNodeID      string
	DestinationNodeID string
	CutoffAt          time.Time
	DepartsAt         time.Time
	ArrivesAt         time.Time
	CapacityPackages  int
}

// EventName returns "DepartureScheduled".
func (DepartureScheduled) EventName() string { return "DepartureScheduled" }

// Entity returns "departure".
func (DepartureScheduled) Entity() string { return entity }

// SubjectID returns the departureId.
func (e DepartureScheduled) SubjectID() string { return string(e.DepartureID) }

// DepartureCapacityChanged is raised by Book, Release and ChangeCapacity.
type DepartureCapacityChanged struct {
	shared.Header
	DepartureID       ID
	CapacityPackages  int
	BookedPackages    int
	RemainingPackages int
}

// EventName returns "DepartureCapacityChanged".
func (DepartureCapacityChanged) EventName() string { return "DepartureCapacityChanged" }

// Entity returns "departure".
func (DepartureCapacityChanged) Entity() string { return entity }

// SubjectID returns the departureId.
func (e DepartureCapacityChanged) SubjectID() string { return string(e.DepartureID) }

// DepartureCancelled is raised by Cancel.
type DepartureCancelled struct {
	shared.Header
	DepartureID ID
	Reason      string
	CancelledAt time.Time
}

// EventName returns "DepartureCancelled".
func (DepartureCancelled) EventName() string { return "DepartureCancelled" }

// Entity returns "departure".
func (DepartureCancelled) Entity() string { return entity }

// SubjectID returns the departureId.
func (e DepartureCancelled) SubjectID() string { return string(e.DepartureID) }

// DepartureDeparted is raised by RecordDeparture (analytics only).
type DepartureDeparted struct {
	shared.Header
	DepartureID ID
	DepartedAt  time.Time
}

// EventName returns "DepartureDeparted".
func (DepartureDeparted) EventName() string { return "DepartureDeparted" }

// Entity returns "departure".
func (DepartureDeparted) Entity() string { return entity }

// SubjectID returns the departureId.
func (e DepartureDeparted) SubjectID() string { return string(e.DepartureID) }

// DepartureArrived is raised by RecordArrival (analytics only).
type DepartureArrived struct {
	shared.Header
	DepartureID ID
	ArrivedAt   time.Time
}

// EventName returns "DepartureArrived".
func (DepartureArrived) EventName() string { return "DepartureArrived" }

// Entity returns "departure".
func (DepartureArrived) Entity() string { return entity }

// SubjectID returns the departureId.
func (e DepartureArrived) SubjectID() string { return string(e.DepartureID) }

var (
	_ shared.Event = DepartureScheduled{}
	_ shared.Event = DepartureCapacityChanged{}
	_ shared.Event = DepartureCancelled{}
	_ shared.Event = DepartureDeparted{}
	_ shared.Event = DepartureArrived{}
)
