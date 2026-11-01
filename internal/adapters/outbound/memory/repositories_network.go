package memory

import (
	"context"
	"slices"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

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
