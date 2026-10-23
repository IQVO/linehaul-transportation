package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// System reasons of the departures this service cancels itself (ADR 0002 A27).
const (
	ReasonNodeSuspended  = "NODE_SUSPENDED"
	ReasonNodeRetired    = "NODE_RETIRED"
	ReasonProfileRevised = "PROFILE_REVISED"
)

// GenerateReport says what one run of GenerateDepartures did.
type GenerateReport struct {
	// Scheduled counts the new departures (lane schedules and pickups).
	Scheduled int
	// Failed counts the schedules and nodes whose unit of work failed.
	Failed int
}

// GenerateDepartures is policy HorizonRoll: it keeps the 14-day horizon of
// every ACTIVE lane schedule and of every pickup-eligible vendor node. A rerun
// schedules nothing new (ADR 0002 A1, A6). One failing schedule or node is
// counted and logged; the rest still run. It returns the context error if ctx ends.
func (s *Service) GenerateDepartures(ctx context.Context) (GenerateReport, error) {
	var rep GenerateReport
	schedules, err := s.Schedules.ListActive(ctx)
	if err != nil {
		return rep, err
	}
	for _, sc := range schedules {
		n, err := s.generateLane(ctx, sc.LaneID())
		rep.add(n, err)
		s.logFailure("lane schedule", sc.LaneID(), err)
	}
	nodes, err := s.Nodes.ListPickupEligible(ctx)
	if err != nil {
		return rep, err
	}
	for _, n := range nodes {
		count, err := s.generatePickups(ctx, n.ID)
		rep.add(count, err)
		s.logFailure("pickups of node", n.ID, err)
	}
	return rep, ctx.Err()
}

func (r *GenerateReport) add(n int, err error) {
	if err != nil {
		r.Failed++
		return
	}
	r.Scheduled += n
}

func (s *Service) logFailure(what, id string, err error) {
	if err != nil {
		s.Logger.Warn("generate departures failed", "what", what, "id", id, "error", err)
	}
}

func countScheduled(evs []shared.Event) int {
	n := 0
	for _, e := range evs {
		if _, ok := e.(departure.DepartureScheduled); ok {
			n++
		}
	}
	return n
}

// generateLane expands one lane schedule in its own unit of work.
func (s *Service) generateLane(ctx context.Context, laneID string) (int, error) {
	count := 0
	err := s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		sc, err := s.Schedules.Get(ctx, laneID)
		if err != nil {
			return nil, err
		}
		lane, err := s.Lanes.Get(ctx, laneID)
		if isNotFound(err) {
			return nil, nil
		}
		origin, oerr := s.Nodes.Get(ctx, lane.Origin)
		dest, derr := s.Nodes.Get(ctx, lane.Destination)
		if err != nil || (oerr != nil && !isNotFound(oerr)) || (derr != nil && !isNotFound(derr)) {
			return nil, errors.Join(err, oerr, derr)
		}
		specs, err := schedule.PlanLane(sc, lane, origin, dest, s.Clock.Now())
		if err != nil {
			s.Logger.Info("lane skipped", "lane", laneID, "reason", err.Error())
			return nil, nil
		}
		evs, err := s.scheduleSpecs(ctx, specs)
		count = countScheduled(evs)
		return evs, err
	})
	return count, err
}

// generatePickups plans the pickups of one node in its own unit of work.
func (s *Service) generatePickups(ctx context.Context, nodeID string) (int, error) {
	count := 0
	err := s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		evs, err := s.ensurePickups(ctx, nodeID)
		count = countScheduled(evs)
		return evs, err
	})
	return count, err
}

// scheduleSpecs schedules every spec that is not stored yet. A spec the domain
// refuses (lane closed, invalid times, validation) is logged and skipped.
func (s *Service) scheduleSpecs(ctx context.Context, specs []departure.Spec) ([]shared.Event, error) {
	var out []shared.Event
	now := s.Clock.Now()
	for _, spec := range specs {
		if _, err := s.Departures.Get(ctx, spec.ID); err == nil {
			continue
		} else if !isNotFound(err) {
			return nil, err
		}
		lane, err := s.Lanes.Get(ctx, spec.LaneID)
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		d, evs, err := departure.Schedule(spec, err == nil && lane.Open, now)
		if err != nil {
			s.Logger.Info("departure not scheduled", "departure", spec.ID, "lane", spec.LaneID, "reason", err.Error())
			continue
		}
		if err := s.Departures.Save(ctx, d); err != nil {
			return nil, err
		}
		out = append(out, evs...)
	}
	return out, nil
}

// pickupPlan computes the pickup specs of a node, or nil while an input is
// missing: the node, its profile, an outbound lane or the type of the lane's
// destination.
func (s *Service) pickupPlan(ctx context.Context, nodeID string, now time.Time) (specs []departure.Spec, planned bool, err error) {
	node, err := s.Nodes.Get(ctx, nodeID)
	if isNotFound(err) || (err == nil && !node.PickupEligible()) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	lanes, err := s.Lanes.ListByOrigin(ctx, nodeID)
	if err != nil {
		return nil, false, err
	}
	types := map[string]network.NodeType{}
	for _, l := range lanes {
		if n, gerr := s.Nodes.Get(ctx, l.Destination); gerr == nil {
			types[l.Destination] = n.Type
		} else if !isNotFound(gerr) {
			return nil, false, gerr
		}
	}
	lane, ok := network.DefaultOutboundLane(nodeID, lanes, func(id string) network.NodeType { return types[id] })
	if !ok || !types[lane.Destination].Valid() {
		return nil, false, nil
	}
	specs, err = schedule.PlanPickups(schedule.PickupInput{
		Node: node, Lane: lane, DestinationType: types[lane.Destination], PackagesPerOrder: s.PackagesPerOrder,
	}, now)
	return specs, err == nil, err
}

// ensurePickups is policy PickupFromCutoffs for one node, inside the caller's
// unit of work: it schedules the missing pickups of the horizon and cancels the
// future unbooked ones that the declared cutoffs no longer produce (ADR 0002
// A7 to A11). Anything missing (node, profile, lane) makes it a no-op.
func (s *Service) ensurePickups(ctx context.Context, nodeID string) ([]shared.Event, error) {
	now := s.Clock.Now()
	specs, planned, err := s.pickupPlan(ctx, nodeID, now)
	if err != nil || !planned {
		return nil, err
	}
	evs, err := s.scheduleSpecs(ctx, specs)
	if err != nil {
		return nil, err
	}
	stale, err := s.stalePickups(ctx, nodeID, specs, now)
	if err != nil || len(stale) == 0 {
		return evs, err
	}
	b := s.newBookingSet(ctx)
	if err := b.cancel(stale, ReasonProfileRevised, false); err != nil {
		return nil, err
	}
	return append(evs, b.events...), nil
}

// stalePickups returns the future SCHEDULED pickups of the node that are not in
// the plan and hold no booking.
func (s *Service) stalePickups(ctx context.Context, nodeID string, plan []departure.Spec, now time.Time) ([]departure.ID, error) {
	wanted := map[departure.ID]bool{}
	for _, sp := range plan {
		wanted[sp.ID] = true
	}
	future, err := s.Departures.List(ctx, repository.DepartureFilter{
		Origin: nodeID, Kind: departure.KindPickup, State: departure.StateScheduled, CutoffAfter: now, Limit: repository.MaxListLimit,
	})
	if err != nil {
		return nil, err
	}
	var stale []departure.ID
	for _, d := range future {
		if !wanted[d.ID()] && d.Booked() == 0 {
			stale = append(stale, d.ID())
		}
	}
	return stale, nil
}

// cancelPickupsOf cancels every future SCHEDULED pickup of a node, rebooking
// the assignments booked on them (NodeSuspended, NodeRetired).
func (s *Service) cancelPickupsOf(ctx context.Context, nodeID, reason string) ([]shared.Event, error) {
	b := s.newBookingSet(ctx)
	future, err := s.Departures.List(ctx, repository.DepartureFilter{
		Origin: nodeID, Kind: departure.KindPickup, State: departure.StateScheduled, CutoffAfter: b.now, Limit: repository.MaxListLimit,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]departure.ID, 0, len(future))
	for _, d := range future {
		ids = append(ids, d.ID())
	}
	err = b.cancel(ids, reason, false)
	return b.events, err
}
