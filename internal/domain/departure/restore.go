package departure

import "github.com/claudioed/linehaul-transportation/internal/domain/shared"

// Restore rebuilds a departure from its persisted form and validates every
// invariant; a broken row is shared.ErrInvalidSnapshot.
func Restore(s Snapshot) (*Departure, error) {
	if err := validateRefs(s.ID, s.LaneID, s.OriginNodeID, s.DestinationNodeID, s.Kind, s.OriginType, s.DestinationType); err != nil {
		return nil, shared.Corrupt("%v", err)
	}
	if !s.State.Valid() {
		return nil, shared.Corrupt("departure %s: state %q", s.ID, s.State)
	}
	if !validTimes(s.CutoffAt, s.DepartsAt, s.ArrivesAt) {
		return nil, shared.Corrupt("departure %s: times are not cutoff <= departs < arrives", s.ID)
	}
	if s.CapacityPackages < 0 || s.CapacityPackages > shared.MaxInt32 || s.BookedPackages < 0 || s.BookedPackages > s.CapacityPackages {
		return nil, shared.Corrupt("departure %s: booked %d capacity %d", s.ID, s.BookedPackages, s.CapacityPackages)
	}
	if s.Version < 1 {
		return nil, shared.Corrupt("departure %s: version %d", s.ID, s.Version)
	}
	if err := restoreStateFields(s); err != nil {
		return nil, err
	}
	return &Departure{
		id: s.ID, laneID: s.LaneID, kind: s.Kind, origin: s.OriginNodeID, destination: s.DestinationNodeID,
		originType: s.OriginType, destType: s.DestinationType,
		cutoffAt: s.CutoffAt.UTC(), departsAt: s.DepartsAt.UTC(), arrivesAt: s.ArrivesAt.UTC(),
		capacity: s.CapacityPackages, booked: s.BookedPackages, state: s.State, cancelWhy: s.CancelReason,
		cancelledAt: shared.UTC(s.CancelledAt), departedAt: shared.UTC(s.DepartedAt), arrivedAt: shared.UTC(s.ArrivedAt),
		version: s.Version,
	}, nil
}

// restoreStateFields checks that the timestamps present match the state: a
// cancelled departure has its reason and instant, a departed one its departure
// instant, an arrived one both of its instants, and nothing else has them.
func restoreStateFields(s Snapshot) error {
	cancelled := s.State == StateCancelled
	departed := s.State == StateDeparted || s.State == StateArrived
	arrived := s.State == StateArrived
	checks := []struct {
		name string
		want bool
		got  bool
	}{
		{"cancelReason", cancelled, s.CancelReason != ""},
		{"cancelledAt", cancelled, !s.CancelledAt.IsZero()},
		{"departedAt", departed, !s.DepartedAt.IsZero()},
		{"arrivedAt", arrived, !s.ArrivedAt.IsZero()},
	}
	for _, c := range checks {
		if c.want != c.got {
			return shared.Corrupt("departure %s: %s present=%v in state %s", s.ID, c.name, c.got, s.State)
		}
	}
	if cancelled && !shared.ValidText(s.CancelReason) {
		return shared.Corrupt("departure %s: cancel reason", s.ID)
	}
	return nil
}
