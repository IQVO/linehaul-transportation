package usecases

import (
	"context"

	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// The consumer names of the inbox (processed_events.consumer).
const (
	ConsumerNodeTopology  = "node-topology"
	ConsumerOrderSourcing = "order-sourcing"
	ConsumerShipping      = "shipping-and-tracking"
)

// NodeRegisteredInput is what NodeRegistered tells this context.
type NodeRegisteredInput struct {
	NodeID   string
	NodeType string
	Operator string
	Timezone string
}

// ProfileDeclaredInput is what NodeOperationalProfileDeclared tells this context.
type ProfileDeclaredInput struct {
	NodeID  string
	Profile network.Profile
}

// NodeInput names a node (NodeActivated, NodeSuspended, NodeRetired).
type NodeInput struct{ NodeID string }

// LaneOpenedInput is what LaneOpened tells this context.
type LaneOpenedInput struct {
	LaneID         string
	OriginNodeID   string
	DestNodeID     string
	Mode           string
	TransitMinutes int
}

// LaneInput names a lane (LaneClosed).
type LaneInput struct{ LaneID string }

// ReleasedInput is what FulfillmentReleasedToNode tells this context.
type ReleasedInput struct {
	AssignmentID string
	ReleaseNo    int
	Facts        volume.Facts
}

// ReassignedInput is what FulfillmentReassigned tells this context.
type ReassignedInput struct {
	AssignmentID string
	ToNodeID     string
}

// LockedInput is what FulfillmentLocked tells this context.
type LockedInput struct {
	AssignmentID string
	Facts        volume.Facts
}

// AssignmentInput names an assignment (FulfillmentCancelled).
type AssignmentInput struct{ AssignmentID string }

// LabelIssuedInput is what LabelIssued tells this context.
type LabelIssuedInput struct {
	AssignmentID   string
	TrackingNumber string
}

// LabelVoidedInput is what LabelVoided tells this context.
type LabelVoidedInput struct{ TrackingNumber string }

func (s *Service) nodeView(ctx context.Context, id string) (network.NodeView, error) {
	n, err := s.Nodes.Get(ctx, id)
	if isNotFound(err) {
		if !shared.ValidID(id) {
			return n, shared.Invalid("nodeId %q is not a valid id", id)
		}
		return network.NewNodeView(id), nil
	}
	return n, err
}

// saveNodeAndPlan stores the node view and re-plans its pickups.
func (s *Service) saveNodeAndPlan(ctx context.Context, n network.NodeView) ([]shared.Event, error) {
	if err := s.Nodes.Save(ctx, n); err != nil {
		return nil, err
	}
	return s.ensurePickups(ctx, n.ID)
}

// HandleNodeRegistered applies NodeRegistered.
func (s *Service) HandleNodeRegistered(ctx context.Context, eventID string, in NodeRegisteredInput) error {
	return s.consume(ctx, ConsumerNodeTopology, eventID, func(ctx context.Context) ([]shared.Event, error) {
		n, err := s.nodeView(ctx, in.NodeID)
		if err != nil {
			return nil, err
		}
		if err := n.ApplyRegistered(network.NodeType(in.NodeType), network.Operator(in.Operator), in.Timezone); err != nil {
			return nil, err
		}
		evs, err := s.saveNodeAndPlan(ctx, n)
		if err != nil {
			return nil, err
		}
		more, err := s.planLanesEndingAt(ctx, n.ID)
		return append(evs, more...), err
	})
}

// planLanesEndingAt re-plans the pickups of the origins of the lanes that end at
// a node: its type was missing from them until it registered.
func (s *Service) planLanesEndingAt(ctx context.Context, nodeID string) ([]shared.Event, error) {
	lanes, err := s.Lanes.ListByDestination(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	var out []shared.Event
	for _, l := range lanes {
		evs, err := s.ensurePickups(ctx, l.Origin)
		if err != nil {
			return nil, err
		}
		out = append(out, evs...)
	}
	return out, nil
}

// HandleProfileDeclared applies NodeOperationalProfileDeclared (ADR 0002 A11).
func (s *Service) HandleProfileDeclared(ctx context.Context, eventID string, in ProfileDeclaredInput) error {
	return s.consume(ctx, ConsumerNodeTopology, eventID, func(ctx context.Context) ([]shared.Event, error) {
		n, err := s.nodeView(ctx, in.NodeID)
		if err != nil {
			return nil, err
		}
		applied, err := n.ApplyProfile(in.Profile)
		if err != nil || !applied {
			return nil, err
		}
		return s.saveNodeAndPlan(ctx, n)
	})
}

// HandleNodeActivated applies NodeActivated and generates the pickups (ADR 0002 A10).
func (s *Service) HandleNodeActivated(ctx context.Context, eventID string, in NodeInput) error {
	return s.consume(ctx, ConsumerNodeTopology, eventID, func(ctx context.Context) ([]shared.Event, error) {
		n, err := s.nodeView(ctx, in.NodeID)
		if err != nil {
			return nil, err
		}
		n.ApplyActivated()
		return s.saveNodeAndPlan(ctx, n)
	})
}

// HandleNodeSuspended applies NodeSuspended and cancels the future pickups.
func (s *Service) HandleNodeSuspended(ctx context.Context, eventID string, in NodeInput) error {
	return s.leaveService(ctx, eventID, in, (*network.NodeView).ApplySuspended, ReasonNodeSuspended)
}

// HandleNodeRetired applies NodeRetired and cancels the future pickups.
func (s *Service) HandleNodeRetired(ctx context.Context, eventID string, in NodeInput) error {
	return s.leaveService(ctx, eventID, in, (*network.NodeView).ApplyRetired, ReasonNodeRetired)
}

func (s *Service) leaveService(ctx context.Context, eventID string, in NodeInput, apply func(*network.NodeView) bool, reason string) error {
	return s.consume(ctx, ConsumerNodeTopology, eventID, func(ctx context.Context) ([]shared.Event, error) {
		n, err := s.nodeView(ctx, in.NodeID)
		if err != nil {
			return nil, err
		}
		changed := apply(&n)
		if err := s.Nodes.Save(ctx, n); err != nil || !changed {
			return nil, err
		}
		return s.cancelPickupsOf(ctx, n.ID, reason)
	})
}

// HandleLaneOpened applies LaneOpened and re-plans the pickups of the lane's origin.
func (s *Service) HandleLaneOpened(ctx context.Context, eventID string, in LaneOpenedInput) error {
	return s.consume(ctx, ConsumerNodeTopology, eventID, func(ctx context.Context) ([]shared.Event, error) {
		lane, err := network.NewLaneView(in.LaneID, in.OriginNodeID, in.DestNodeID, network.Mode(in.Mode), in.TransitMinutes)
		if err != nil {
			return nil, err
		}
		if known, gerr := s.Lanes.Get(ctx, in.LaneID); gerr == nil {
			lane.Open = known.Open
		} else if !isNotFound(gerr) {
			return nil, gerr
		}
		if err := s.Lanes.Save(ctx, lane); err != nil {
			return nil, err
		}
		return s.ensurePickups(ctx, lane.Origin)
	})
}

// HandleLaneClosed applies LaneClosed: no new departure is scheduled on the lane (ADR 0002 A12).
func (s *Service) HandleLaneClosed(ctx context.Context, eventID string, in LaneInput) error {
	return s.consume(ctx, ConsumerNodeTopology, eventID, func(ctx context.Context) ([]shared.Event, error) {
		lane, err := s.Lanes.Get(ctx, in.LaneID)
		if isNotFound(err) {
			return nil, nil
		}
		if err != nil || !lane.ApplyClosed() {
			return nil, err
		}
		return nil, s.Lanes.Save(ctx, lane)
	})
}

// volumeRow loads the ExpectedVolume of an assignment, or a new UNKNOWN one.
func (s *Service) volumeRow(ctx context.Context, assignmentID string) (vol *volume.ExpectedVolume, created bool, err error) {
	vol, err = s.Volumes.Get(ctx, assignmentID)
	if isNotFound(err) {
		vol, err = volume.Unknown(assignmentID)
		return vol, true, err
	}
	return vol, false, err
}

// HandleReleased applies FulfillmentReleasedToNode.
func (s *Service) HandleReleased(ctx context.Context, eventID string, in ReleasedInput) error {
	return s.consume(ctx, ConsumerOrderSourcing, eventID, func(ctx context.Context) ([]shared.Event, error) {
		vol, _, err := s.volumeRow(ctx, in.AssignmentID)
		if err != nil {
			return nil, err
		}
		changed, err := vol.Release(in.Facts, in.ReleaseNo)
		return nil, s.saveIf(ctx, vol, changed, err)
	})
}

// HandleReassigned applies FulfillmentReassigned: a view update only (ADR 0002 A16).
func (s *Service) HandleReassigned(ctx context.Context, eventID string, in ReassignedInput) error {
	return s.consume(ctx, ConsumerOrderSourcing, eventID, func(ctx context.Context) ([]shared.Event, error) {
		vol, _, err := s.volumeRow(ctx, in.AssignmentID)
		if err != nil {
			return nil, err
		}
		changed, err := vol.Reassign(in.ToNodeID)
		return nil, s.saveIf(ctx, vol, changed, err)
	})
}

// HandleCancelled applies FulfillmentCancelled: a view update only (ADR 0002 A16).
func (s *Service) HandleCancelled(ctx context.Context, eventID string, in AssignmentInput) error {
	return s.consume(ctx, ConsumerOrderSourcing, eventID, func(ctx context.Context) ([]shared.Event, error) {
		vol, _, err := s.volumeRow(ctx, in.AssignmentID)
		if err != nil {
			return nil, err
		}
		return nil, s.saveIf(ctx, vol, vol.Cancel(), nil)
	})
}

// HandleLocked applies FulfillmentLocked, policy BookOnLock: it books one
// package on the pickup departure, or rebooks onto the next one from the same
// origin (BookingMoved), or reports BookingFailed (ADR 0002 A13, A14).
func (s *Service) HandleLocked(ctx context.Context, eventID string, in LockedInput) error {
	return s.consume(ctx, ConsumerOrderSourcing, eventID, func(ctx context.Context) ([]shared.Event, error) {
		vol, _, err := s.volumeRow(ctx, in.AssignmentID)
		if err != nil {
			return nil, err
		}
		need, err := vol.Lock(in.Facts)
		if err != nil || !need {
			return nil, err
		}
		b := s.newBookingSet(ctx)
		if err := b.place(vol, in.Facts.DepartureID, in.Facts.NodeID); err != nil {
			return nil, err
		}
		return b.events, s.Volumes.Save(ctx, vol)
	})
}

// HandleLabelIssued applies LabelIssued (ADR 0002 A18).
func (s *Service) HandleLabelIssued(ctx context.Context, eventID string, in LabelIssuedInput) error {
	return s.consume(ctx, ConsumerShipping, eventID, func(ctx context.Context) ([]shared.Event, error) {
		vol, _, err := s.volumeRow(ctx, in.AssignmentID)
		if err != nil {
			return nil, err
		}
		added, err := vol.AddTracking(in.TrackingNumber)
		return nil, s.saveIf(ctx, vol, added, err)
	})
}

// HandleLabelVoided applies LabelVoided: the tracking number leaves the
// projection, the booking stays (ADR 0002 A18).
func (s *Service) HandleLabelVoided(ctx context.Context, eventID string, in LabelVoidedInput) error {
	return s.consume(ctx, ConsumerShipping, eventID, func(ctx context.Context) ([]shared.Event, error) {
		vol, err := s.Volumes.FindByTracking(ctx, in.TrackingNumber)
		if isNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return nil, s.saveIf(ctx, vol, vol.RemoveTracking(in.TrackingNumber), nil)
	})
}

// saveIf saves the row when changed, passing an earlier error through.
func (s *Service) saveIf(ctx context.Context, vol *volume.ExpectedVolume, changed bool, err error) error {
	if err != nil || !changed {
		return err
	}
	return s.Volumes.Save(ctx, vol)
}
