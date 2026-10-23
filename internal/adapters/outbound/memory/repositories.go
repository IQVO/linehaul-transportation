package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// checkVersion applies the optimistic-concurrency rule of the ports package:
// a create (version 1) needs the id to be free, an update needs the stored
// version to be version-1.
func checkVersion(stored int64, exists bool, version int64) error {
	if version == 1 {
		if exists {
			return fmt.Errorf("%w: id already stored", ports.ErrConcurrentUpdate)
		}
		return nil
	}
	if !exists || stored != version-1 {
		return fmt.Errorf("%w: stored version is not %d", ports.ErrConcurrentUpdate, version-1)
	}
	return nil
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// DepartureRepository is the in-memory ports.DepartureRepository.
type DepartureRepository struct{ s *Store }

// Get returns the departure, or ports.ErrNotFound.
func (r *DepartureRepository) Get(ctx context.Context, id departure.ID) (*departure.Departure, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.data.departures[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return departure.Restore(snap)
}

func matches(s departure.Snapshot, f repository.DepartureFilter) bool {
	switch {
	case f.Origin != "" && s.OriginNodeID != f.Origin,
		f.Destination != "" && s.DestinationNodeID != f.Destination,
		f.Kind != "" && s.Kind != f.Kind,
		f.State != "" && s.State != f.State,
		!f.CutoffFrom.IsZero() && s.CutoffAt.Before(f.CutoffFrom),
		!f.CutoffTo.IsZero() && !s.CutoffAt.Before(f.CutoffTo),
		!f.CutoffAfter.IsZero() && !s.CutoffAt.After(f.CutoffAfter),
		f.HasRoom && s.BookedPackages >= s.CapacityPackages:
		return false
	}
	return true
}

// List returns the departures matching the filter by cutoffAt, then id.
func (r *DepartureRepository) List(ctx context.Context, f repository.DepartureFilter) ([]*departure.Departure, error) {
	defer r.s.enter(ctx)()
	var snaps []departure.Snapshot
	for _, s := range r.s.data.departures {
		if matches(s, f) {
			snaps = append(snaps, s)
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].CutoffAt.Equal(snaps[j].CutoffAt) {
			return snaps[i].CutoffAt.Before(snaps[j].CutoffAt)
		}
		return snaps[i].ID < snaps[j].ID
	})
	limit := f.Limit
	if limit <= 0 {
		limit = repository.DefaultListLimit
	}
	if len(snaps) > limit {
		snaps = snaps[:limit]
	}
	out := make([]*departure.Departure, 0, len(snaps))
	for _, s := range snaps {
		d, err := departure.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Save persists the departure under the concurrency rule.
func (r *DepartureRepository) Save(ctx context.Context, d *departure.Departure) error {
	defer r.s.enter(ctx)()
	snap := d.Snapshot()
	stored, exists := r.s.data.departures[snap.ID]
	if err := checkVersion(stored.Version, exists, snap.Version); err != nil {
		return err
	}
	r.s.data.departures[snap.ID] = snap
	return nil
}

// LaneScheduleRepository is the in-memory ports.LaneScheduleRepository.
type LaneScheduleRepository struct{ s *Store }

// Get returns the schedule of the lane, or ports.ErrNotFound.
func (r *LaneScheduleRepository) Get(ctx context.Context, laneID string) (*schedule.LaneSchedule, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.data.schedules[laneID]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return schedule.Restore(snap)
}

// ListActive returns every ACTIVE schedule by lane id.
func (r *LaneScheduleRepository) ListActive(ctx context.Context) ([]*schedule.LaneSchedule, error) {
	defer r.s.enter(ctx)()
	out := []*schedule.LaneSchedule{}
	for _, k := range sortedKeys(r.s.data.schedules) {
		snap := r.s.data.schedules[k]
		if snap.State != schedule.StateActive {
			continue
		}
		sc, err := schedule.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// Save persists the schedule under the concurrency rule.
func (r *LaneScheduleRepository) Save(ctx context.Context, sc *schedule.LaneSchedule) error {
	defer r.s.enter(ctx)()
	snap := sc.Snapshot()
	stored, exists := r.s.data.schedules[snap.LaneID]
	if err := checkVersion(stored.Version, exists, snap.Version); err != nil {
		return err
	}
	r.s.data.schedules[snap.LaneID] = snap
	return nil
}

// LoadRepository is the in-memory ports.LoadRepository.
type LoadRepository struct{ s *Store }

// Get returns the load, or ports.ErrNotFound.
func (r *LoadRepository) Get(ctx context.Context, id load.ID) (*load.Load, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.data.loads[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return load.Restore(snap)
}

// GetByDeparture returns the load of a departure, or ports.ErrNotFound.
func (r *LoadRepository) GetByDeparture(ctx context.Context, id departure.ID) (*load.Load, error) {
	defer r.s.enter(ctx)()
	for _, snap := range r.s.data.loads {
		if snap.DepartureID == id {
			return load.Restore(snap)
		}
	}
	return nil, ports.ErrNotFound
}

func activeSnap(s load.Snapshot) bool {
	return s.State == load.StateOpen || s.State == load.StateClosed
}

// FindActiveByTracking returns the OPEN or CLOSED load that holds the tracking number.
func (r *LoadRepository) FindActiveByTracking(ctx context.Context, tn string) (load.ID, error) {
	defer r.s.enter(ctx)()
	for _, snap := range r.s.data.loads {
		if activeSnap(snap) && slices.Contains(snap.TrackingNumbers, tn) {
			return snap.ID, nil
		}
	}
	return "", ports.ErrNotFound
}

// Save persists the load under the concurrency rule and the two unique indexes.
func (r *LoadRepository) Save(ctx context.Context, l *load.Load) error {
	defer r.s.enter(ctx)()
	snap := l.Snapshot()
	stored, exists := r.s.data.loads[snap.ID]
	if err := checkVersion(stored.Version, exists, snap.Version); err != nil {
		return err
	}
	for id, other := range r.s.data.loads {
		if id == snap.ID {
			continue
		}
		if other.DepartureID == snap.DepartureID {
			return fmt.Errorf("%w: departure %s", load.ErrLoadAlreadyExists, snap.DepartureID)
		}
		if activeSnap(snap) && activeSnap(other) {
			for _, tn := range snap.TrackingNumbers {
				if slices.Contains(other.TrackingNumbers, tn) {
					return fmt.Errorf("%w: %s", load.ErrPackageAlreadyLoaded, tn)
				}
			}
		}
	}
	r.s.data.loads[snap.ID] = snap
	return nil
}

// cloneNode copies a node view including its profile.
func cloneNode(n network.NodeView) network.NodeView {
	if n.Profile == nil {
		return n
	}
	p := *n.Profile
	p.Hours = make(map[string]network.Hours, len(n.Profile.Hours))
	for k, v := range n.Profile.Hours {
		p.Hours[k] = v
	}
	p.Holidays = slices.Clone(n.Profile.Holidays)
	p.Cutoffs = make([]network.Cutoff, len(n.Profile.Cutoffs))
	for i, c := range n.Profile.Cutoffs {
		c.Days = slices.Clone(c.Days)
		p.Cutoffs[i] = c
	}
	n.Profile = &p
	return n
}

// NodeViewRepository is the in-memory ports.NodeViewRepository.
type NodeViewRepository struct{ s *Store }

// Get returns the node view, or ports.ErrNotFound.
func (r *NodeViewRepository) Get(ctx context.Context, id string) (network.NodeView, error) {
	defer r.s.enter(ctx)()
	n, ok := r.s.data.nodes[id]
	if !ok {
		return network.NodeView{}, ports.ErrNotFound
	}
	return cloneNode(n), nil
}

// ListPickupEligible returns the views that are pickup eligible, by node id.
func (r *NodeViewRepository) ListPickupEligible(ctx context.Context) ([]network.NodeView, error) {
	defer r.s.enter(ctx)()
	out := []network.NodeView{}
	for _, k := range sortedKeys(r.s.data.nodes) {
		if n := r.s.data.nodes[k]; n.PickupEligible() {
			out = append(out, cloneNode(n))
		}
	}
	return out, nil
}

// Save inserts or replaces the view.
func (r *NodeViewRepository) Save(ctx context.Context, n network.NodeView) error {
	defer r.s.enter(ctx)()
	r.s.data.nodes[n.ID] = cloneNode(n)
	return nil
}

// LaneViewRepository is the in-memory ports.LaneViewRepository.
type LaneViewRepository struct{ s *Store }

// Get returns the lane view, or ports.ErrNotFound.
func (r *LaneViewRepository) Get(ctx context.Context, id string) (network.LaneView, error) {
	defer r.s.enter(ctx)()
	l, ok := r.s.data.lanes[id]
	if !ok {
		return network.LaneView{}, ports.ErrNotFound
	}
	return l, nil
}

// ListByOrigin returns every lane that leaves the node, by lane id.
func (r *LaneViewRepository) ListByOrigin(ctx context.Context, origin string) ([]network.LaneView, error) {
	defer r.s.enter(ctx)()
	out := []network.LaneView{}
	for _, k := range sortedKeys(r.s.data.lanes) {
		if l := r.s.data.lanes[k]; l.Origin == origin {
			out = append(out, l)
		}
	}
	return out, nil
}

// ListByDestination returns every lane that ends at the node, by lane id.
func (r *LaneViewRepository) ListByDestination(ctx context.Context, destination string) ([]network.LaneView, error) {
	defer r.s.enter(ctx)()
	out := []network.LaneView{}
	for _, k := range sortedKeys(r.s.data.lanes) {
		if l := r.s.data.lanes[k]; l.Destination == destination {
			out = append(out, l)
		}
	}
	return out, nil
}

// Save inserts or replaces the view.
func (r *LaneViewRepository) Save(ctx context.Context, l network.LaneView) error {
	defer r.s.enter(ctx)()
	r.s.data.lanes[l.ID] = l
	return nil
}

// VolumeRepository is the in-memory ports.VolumeRepository.
type VolumeRepository struct{ s *Store }

// Get returns the row, or ports.ErrNotFound.
func (r *VolumeRepository) Get(ctx context.Context, id string) (*volume.ExpectedVolume, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.data.volumes[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return volume.Restore(snap)
}

// FindByTracking returns the row that holds the tracking number, or ports.ErrNotFound.
func (r *VolumeRepository) FindByTracking(ctx context.Context, tn string) (*volume.ExpectedVolume, error) {
	defer r.s.enter(ctx)()
	for _, k := range sortedKeys(r.s.data.volumes) {
		if snap := r.s.data.volumes[k]; slices.Contains(snap.TrackingNumbers, tn) {
			return volume.Restore(snap)
		}
	}
	return nil, ports.ErrNotFound
}

// ListBookedOn returns the BOOKED rows whose booking sits on the departure, by assignment id.
func (r *VolumeRepository) ListBookedOn(ctx context.Context, id departure.ID) ([]*volume.ExpectedVolume, error) {
	defer r.s.enter(ctx)()
	out := []*volume.ExpectedVolume{}
	for _, k := range sortedKeys(r.s.data.volumes) {
		snap := r.s.data.volumes[k]
		if snap.State != volume.StateBooked || snap.BookedOn != id {
			continue
		}
		v, err := volume.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Save persists the row under the concurrency rule.
func (r *VolumeRepository) Save(ctx context.Context, v *volume.ExpectedVolume) error {
	defer r.s.enter(ctx)()
	snap := v.Snapshot()
	stored, exists := r.s.data.volumes[snap.AssignmentID]
	if err := checkVersion(stored.Version, exists, snap.Version); err != nil {
		return err
	}
	r.s.data.volumes[snap.AssignmentID] = snap
	return nil
}
