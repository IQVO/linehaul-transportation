// Package volume is the ExpectedVolume read model: one row per order-sourcing
// assignment, built from FulfillmentReleasedToNode, FulfillmentReassigned,
// FulfillmentLocked, FulfillmentCancelled and the shipping labels. It decides
// nothing about the source contexts; its one rule of its own is the once-only
// booking record that makes BookOnLock idempotent and tells the rebooking which
// assignments sit on a cancelled departure (ADR 0002 A13 to A18, ADR 0003).
//
// It raises BookingMoved and BookingFailed (gap G14).
package volume

import (
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// State is the state of an assignment as this context sees it.
type State string

// The states of an expected volume.
const (
	// StateUnknown: only labels (or nothing yet) were seen.
	StateUnknown State = "UNKNOWN"
	// StateReleased: released to a node, not locked yet.
	StateReleased State = "RELEASED"
	// StateBooked: one package is booked on BookedOn.
	StateBooked State = "BOOKED"
	// StateFailed: locked, but no departure could hold the booking.
	StateFailed State = "FAILED"
	// StateCancelled: cancelled by order-sourcing before the lock.
	StateCancelled State = "CANCELLED"
)

// Valid reports whether s is one of the five states.
func (s State) Valid() bool {
	switch s {
	case StateUnknown, StateReleased, StateBooked, StateFailed, StateCancelled:
		return true
	}
	return false
}

// Reason says why a booking could not be held (BookingFailed.reason).
type Reason string

// The reasons of BookingFailed (ADR 0002 A14).
const (
	ReasonDepartureCancelled Reason = "DEPARTURE_CANCELLED"
	ReasonDepartureClosed    Reason = "DEPARTURE_CLOSED"
	ReasonDepartureFull      Reason = "DEPARTURE_FULL"
	ReasonDepartureUnknown   Reason = "DEPARTURE_UNKNOWN"
)

// Valid reports whether r is one of the four reasons.
func (r Reason) Valid() bool {
	return r == ReasonDepartureCancelled || r == ReasonDepartureClosed || r == ReasonDepartureFull || r == ReasonDepartureUnknown
}

// MaxTrackingNumbers bounds the labels kept per assignment.
const MaxTrackingNumbers = 1000

// Facts are what order-sourcing says about an assignment.
type Facts struct {
	OrderID     string
	GroupNo     int
	NodeID      string
	DepartureID departure.ID
}

func (f Facts) validate() error {
	if !shared.ValidID(f.OrderID) || !shared.ValidID(f.NodeID) || !shared.ValidID(string(f.DepartureID)) {
		return shared.Invalid("orderId %q, nodeId %q and pickupDepartureId %q must be valid ids", f.OrderID, f.NodeID, f.DepartureID)
	}
	if f.GroupNo < 1 || f.GroupNo > shared.MaxInt32 {
		return shared.Invalid("groupNo %d out of range", f.GroupNo)
	}
	return nil
}

// ExpectedVolume is the projection of one assignment.
type ExpectedVolume struct {
	assignmentID string
	facts        Facts
	releaseNo    int
	bookedOn     departure.ID
	state        State
	trackings    []string
	// version is the stored version; dirty says the row changed since, so the
	// next Save writes version+1 however many commands ran in the unit of work
	// (a new row is version 0, so its first Save is a create at version 1).
	version int64
	dirty   bool
}

// Snapshot is the persisted form of an expected volume. Restore validates it.
type Snapshot struct {
	AssignmentID    string
	OrderID         string
	GroupNo         int
	NodeID          string
	DepartureID     departure.ID
	ReleaseNo       int
	BookedOn        departure.ID
	State           State
	TrackingNumbers []string
	Version         int64
}

// Unknown returns the empty row of an assignment nothing is known about yet.
func Unknown(assignmentID string) (*ExpectedVolume, error) {
	if !shared.ValidID(assignmentID) {
		return nil, shared.Invalid("assignmentId %q is not a valid id", assignmentID)
	}
	return &ExpectedVolume{assignmentID: assignmentID, state: StateUnknown, trackings: []string{}}, nil
}

func (v *ExpectedVolume) touch() { v.dirty = true }

func (v *ExpectedVolume) header(now time.Time) shared.Header {
	return shared.Header{Version: v.Version(), At: now.UTC()}
}

// Release applies FulfillmentReleasedToNode. It is ignored (false) for a
// cancelled, booked or failed assignment and for a releaseNo older than the
// stored one.
func (v *ExpectedVolume) Release(f Facts, releaseNo int) (bool, error) {
	if err := f.validate(); err != nil {
		return false, err
	}
	if releaseNo < 1 || releaseNo > shared.MaxInt32 {
		return false, shared.Invalid("releaseNo %d out of range", releaseNo)
	}
	if (v.state != StateUnknown && v.state != StateReleased) || releaseNo < v.releaseNo {
		return false, nil
	}
	v.facts, v.releaseNo, v.state = f, releaseNo, StateReleased
	v.touch()
	return true, nil
}

// Reassign applies FulfillmentReassigned to a RELEASED assignment: the node
// changes and the planned departure is forgotten until the next release.
func (v *ExpectedVolume) Reassign(toNodeID string) (bool, error) {
	if !shared.ValidID(toNodeID) {
		return false, shared.Invalid("toNodeId %q is not a valid id", toNodeID)
	}
	if v.state != StateReleased {
		return false, nil
	}
	v.facts.NodeID, v.facts.DepartureID = toNodeID, ""
	v.touch()
	return true, nil
}

// Cancel applies FulfillmentCancelled. Cancelling a CANCELLED assignment
// changes nothing (false). A BOOKED one is marked CANCELLED too, and its
// booking is kept in place (ADR 0002 A16).
func (v *ExpectedVolume) Cancel() bool {
	if v.state == StateCancelled {
		return false
	}
	v.state = StateCancelled
	v.touch()
	return true
}

// Lock applies the facts of FulfillmentLocked and reports whether the lock
// needs a booking: not when the assignment is CANCELLED or already BOOKED.
func (v *ExpectedVolume) Lock(f Facts) (needsBooking bool, err error) {
	if err := f.validate(); err != nil {
		return false, err
	}
	if v.state == StateCancelled || v.state == StateBooked {
		return false, nil
	}
	v.facts = f
	v.touch()
	return true, nil
}

// Book records the booking of one package on a departure (BookOnLock). A
// second booking is an illegal transition: the record is once-only.
func (v *ExpectedVolume) Book(on departure.ID) error {
	if v.state == StateBooked || v.state == StateCancelled {
		return fmt.Errorf("%w: Book on assignment %s in state %s", shared.ErrIllegalTransition, v.assignmentID, v.state)
	}
	if !shared.ValidID(string(on)) {
		return shared.Invalid("departureId %q is not a valid id", on)
	}
	v.state, v.bookedOn = StateBooked, on
	v.touch()
	return nil
}

// Rebook moves the booking from one departure to another and raises
// BookingMoved. On a BOOKED assignment from must be the departure it sits on.
func (v *ExpectedVolume) Rebook(from, to departure.ID, now time.Time) ([]shared.Event, error) {
	if v.state == StateCancelled || (v.state == StateBooked && v.bookedOn != from) {
		return nil, fmt.Errorf("%w: Rebook of assignment %s in state %s from %s", shared.ErrIllegalTransition, v.assignmentID, v.state, from)
	}
	if !shared.ValidID(string(from)) || !shared.ValidID(string(to)) {
		return nil, shared.Invalid("departure ids %q and %q must be valid", from, to)
	}
	v.state, v.bookedOn = StateBooked, to
	v.touch()
	return []shared.Event{BookingMoved{Header: v.header(now), AssignmentID: v.assignmentID, FromDepartureID: from, ToDepartureID: to}}, nil
}

// Fail records that no departure can hold the booking and raises BookingFailed.
func (v *ExpectedVolume) Fail(on departure.ID, reason Reason, now time.Time) ([]shared.Event, error) {
	if v.state == StateCancelled {
		return nil, fmt.Errorf("%w: Fail of assignment %s in state %s", shared.ErrIllegalTransition, v.assignmentID, v.state)
	}
	if !reason.Valid() || !shared.ValidID(string(on)) {
		return nil, shared.Invalid("reason %q or departure %q is invalid", reason, on)
	}
	v.state, v.bookedOn = StateFailed, ""
	v.touch()
	return []shared.Event{BookingFailed{Header: v.header(now), AssignmentID: v.assignmentID, DepartureID: on, Reason: reason}}, nil
}

// AddTracking records a label's tracking number; a known one changes nothing.
func (v *ExpectedVolume) AddTracking(tn string) (bool, error) {
	if !shared.ValidTextN(tn, 64) {
		return false, shared.Invalid("trackingNumber must be 1 to 64 characters, trimmed, without control characters")
	}
	if slices.Contains(v.trackings, tn) {
		return false, nil
	}
	if len(v.trackings) >= MaxTrackingNumbers {
		return false, shared.Invalid("an assignment keeps at most %d tracking numbers", MaxTrackingNumbers)
	}
	v.trackings = append(v.trackings, tn)
	v.touch()
	return true, nil
}

// RemoveTracking forgets a voided label's tracking number and reports whether it was known.
func (v *ExpectedVolume) RemoveTracking(tn string) bool {
	i := slices.Index(v.trackings, tn)
	if i < 0 {
		return false
	}
	v.trackings = slices.Delete(v.trackings, i, i+1)
	v.touch()
	return true
}

// AssignmentID returns the assignmentId.
func (v *ExpectedVolume) AssignmentID() string { return v.assignmentID }

// State returns the state.
func (v *ExpectedVolume) State() State { return v.state }

// NodeID returns the node of the assignment ("" while unknown).
func (v *ExpectedVolume) NodeID() string { return v.facts.NodeID }

// PlannedDepartureID returns the pickup departure order-sourcing named.
func (v *ExpectedVolume) PlannedDepartureID() departure.ID { return v.facts.DepartureID }

// BookedOn returns the departure holding the booking ("" when none).
func (v *ExpectedVolume) BookedOn() departure.ID { return v.bookedOn }

// TrackingNumbers returns a copy of the tracking numbers.
func (v *ExpectedVolume) TrackingNumbers() []string { return slices.Clone(v.trackings) }

// Version returns the version the next Save writes: the stored one, plus one
// when the row changed since it was loaded.
func (v *ExpectedVolume) Version() int64 {
	if v.dirty {
		return v.version + 1
	}
	return v.version
}

// Snapshot returns the persisted form.
func (v *ExpectedVolume) Snapshot() Snapshot {
	return Snapshot{
		AssignmentID: v.assignmentID, OrderID: v.facts.OrderID, GroupNo: v.facts.GroupNo, NodeID: v.facts.NodeID,
		DepartureID: v.facts.DepartureID, ReleaseNo: v.releaseNo, BookedOn: v.bookedOn, State: v.state,
		TrackingNumbers: slices.Clone(v.trackings), Version: v.Version(),
	}
}

// Restore rebuilds an expected volume from its persisted form and validates it.
func Restore(s Snapshot) (*ExpectedVolume, error) {
	if !shared.ValidID(s.AssignmentID) || !s.State.Valid() || s.Version < 1 {
		return nil, shared.Corrupt("expected volume %q: id, state %q or version %d", s.AssignmentID, s.State, s.Version)
	}
	if (s.State == StateBooked) != (s.BookedOn != "") && s.State != StateCancelled {
		return nil, shared.Corrupt("expected volume %s: bookedOn %q does not match state %s", s.AssignmentID, s.BookedOn, s.State)
	}
	if s.BookedOn != "" && !shared.ValidID(string(s.BookedOn)) {
		return nil, shared.Corrupt("expected volume %s: bookedOn %q", s.AssignmentID, s.BookedOn)
	}
	if s.ReleaseNo < 0 || s.ReleaseNo > shared.MaxInt32 || len(s.TrackingNumbers) > MaxTrackingNumbers {
		return nil, shared.Corrupt("expected volume %s: releaseNo %d or %d tracking numbers", s.AssignmentID, s.ReleaseNo, len(s.TrackingNumbers))
	}
	for _, tn := range s.TrackingNumbers {
		if !shared.ValidTextN(tn, 64) {
			return nil, shared.Corrupt("expected volume %s: tracking number %q", s.AssignmentID, tn)
		}
	}
	return &ExpectedVolume{
		assignmentID: s.AssignmentID,
		facts:        Facts{OrderID: s.OrderID, GroupNo: s.GroupNo, NodeID: s.NodeID, DepartureID: s.DepartureID},
		releaseNo:    s.ReleaseNo, bookedOn: s.BookedOn, state: s.State, trackings: append([]string{}, s.TrackingNumbers...), version: s.Version,
	}, nil
}
