package network

import "github.com/claudioed/linehaul-transportation/internal/domain/shared"

// Mode is the transport mode of a lane.
type Mode string

// The two lane modes.
const (
	ModeGround Mode = "GROUND"
	ModeAir    Mode = "AIR"
)

// LaneView is the local copy of a node-topology lane.
type LaneView struct {
	ID             string
	Origin         string
	Destination    string
	Mode           Mode
	TransitMinutes int
	Open           bool
}

// NewLaneView builds the OPEN view that LaneOpened announces.
func NewLaneView(id, origin, destination string, mode Mode, transitMinutes int) (LaneView, error) {
	if id == "" || origin == "" || destination == "" {
		return LaneView{}, shared.Invalid("lane %q: id, origin and destination are required", id)
	}
	if mode != ModeGround && mode != ModeAir {
		return LaneView{}, shared.Invalid("lane %s: mode %q is unknown", id, mode)
	}
	if transitMinutes < 1 || transitMinutes > shared.MaxInt32 {
		return LaneView{}, shared.Invalid("lane %s: nominalTransitMinutes %d out of range", id, transitMinutes)
	}
	return LaneView{ID: id, Origin: origin, Destination: destination, Mode: mode, TransitMinutes: transitMinutes, Open: true}, nil
}

// ApplyClosed marks the lane closed and reports whether it was open.
func (l *LaneView) ApplyClosed() bool {
	was := l.Open
	l.Open = false
	return was
}
