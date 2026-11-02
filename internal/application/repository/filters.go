// Package repository holds the non-interface vocabulary of the repository
// ports: the list filters. It has no behaviour; the interfaces are in
// internal/application/ports.
package repository

import (
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

// DefaultListLimit and MaxListLimit bound ListDepartures (ADR 0002 A24).
const (
	DefaultListLimit = 500
	MaxListLimit     = 1000
)

// DefaultPageLimit and MaxPageLimit bound the paged GET /departures (fleet REST
// guideline section 3.2).
const (
	DefaultPageLimit = 100
	MaxPageLimit     = 500
)

// DepartureFilter narrows a departure list. A zero field does not filter; the
// set fields combine with AND. The order is always cutoffAt ascending, then
// departureId ascending. Limit 0 means DefaultListLimit.
type DepartureFilter struct {
	Origin      string
	Destination string
	Kind        departure.Kind
	State       departure.State
	// CutoffFrom is inclusive, CutoffTo exclusive, CutoffAfter exclusive.
	CutoffFrom  time.Time
	CutoffTo    time.Time
	CutoffAfter time.Time
	// HasRoom keeps only departures with booked < capacity.
	HasRoom bool
	// After keeps only departures strictly after this keyset position in the
	// (cutoffAt, departureId) order; nil starts at the first row.
	After *DepartureCursor
	Limit int
}

// DepartureCursor is the keyset position of a departure list: the cutoffAt
// and the id of the last row of the previous page.
type DepartureCursor struct {
	CutoffAt time.Time
	ID       string
}
