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
	Limit   int
}
