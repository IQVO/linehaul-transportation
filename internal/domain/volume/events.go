package volume

import (
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

const entity = "departure"

// BookingMoved is raised by Rebook (gap G14, contracts-delta E13). It belongs to
// the Departure entity of the CloudEvents type; its subject is the new departure.
type BookingMoved struct {
	shared.Header
	AssignmentID    string
	FromDepartureID departure.ID
	ToDepartureID   departure.ID
}

// EventName returns "BookingMoved".
func (BookingMoved) EventName() string { return "BookingMoved" }

// Entity returns "departure".
func (BookingMoved) Entity() string { return entity }

// SubjectID returns the toDepartureId.
func (e BookingMoved) SubjectID() string { return string(e.ToDepartureID) }

// BookingFailed is raised by Fail (gap G14, contracts-delta E13). Its subject is
// the departure the booking could not be held on.
type BookingFailed struct {
	shared.Header
	AssignmentID string
	DepartureID  departure.ID
	Reason       Reason
}

// EventName returns "BookingFailed".
func (BookingFailed) EventName() string { return "BookingFailed" }

// Entity returns "departure".
func (BookingFailed) Entity() string { return entity }

// SubjectID returns the departureId.
func (e BookingFailed) SubjectID() string { return string(e.DepartureID) }

var (
	_ shared.Event = BookingMoved{}
	_ shared.Event = BookingFailed{}
)
