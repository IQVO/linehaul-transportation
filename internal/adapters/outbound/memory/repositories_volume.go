package memory

import (
	"context"
	"slices"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

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
