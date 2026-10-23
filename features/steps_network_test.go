package features_test

import (
	"fmt"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

func registerNetworkSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a vendor fulfillment center "([^"]*)" with a pickup cutoff at "([^"]*)" on weekdays$`, w.vendorFC)
	sc.Step(`^an active (sortation center|delivery station) "([^"]*)"$`, w.activeNode)
	sc.Step(`^an open (GROUND|AIR) lane "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.openLane)
	sc.Step(`^the lane "([^"]*)" is closed$`, w.closeLane)
	sc.Step(`^the node "([^"]*)" is (activated|suspended|retired)$`, w.nodeTransition)
	sc.Step(`^the activation of "([^"]*)" is delivered twice with the same event id$`, w.activateTwice)
	sc.Step(`^the profile of "([^"]*)" is revised to revision (\d+) with no cutoff$`, w.reviseProfile)
	sc.Step(`^the node "([^"]*)" has pickup departures toward "([^"]*)"$`, w.hasPickups)
	sc.Step(`^the node "([^"]*)" has no scheduled pickup departures$`, w.hasNoPickups)
	sc.Step(`^every pickup departure of "([^"]*)" is cancelled because "([^"]*)"$`, w.pickupsCancelledBecause)
	sc.Step(`^the departures from "([^"]*)" were scheduled once$`, w.scheduledOnce)
}

func weekdayProfile(revision int, cutoffs []network.Cutoff) network.Profile {
	hours := map[string]network.Hours{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday"} {
		hours[d] = network.Hours{Start: "06:00", End: "22:00"}
	}
	return network.Profile{
		Revision: revision, Timezone: nodeZone, Hours: hours, Cutoffs: cutoffs,
		Cap: network.ThroughputCap{Value: 10, TimeUnit: network.CapHour},
	}
}

func (w *world) vendorFC(id, cutoff string) {
	weekdays := []string{"monday", "tuesday", "wednesday", "thursday", "friday"}
	w.run(func() error {
		if err := w.svc.HandleNodeRegistered(w.ctx(), w.eventID(), usecases.NodeRegisteredInput{
			NodeID: id, NodeType: string(network.FulfillmentCenter), Operator: string(network.OperatorVendor), Timezone: nodeZone,
		}); err != nil {
			return err
		}
		return w.svc.HandleProfileDeclared(w.ctx(), w.eventID(), usecases.ProfileDeclaredInput{
			NodeID: id, Profile: weekdayProfile(1, []network.Cutoff{{LocalTime: cutoff, Days: weekdays, MaxOrders: 40}}),
		})
	})
}

func (w *world) activeNode(kind, id string) {
	typ := network.SortationCenter
	if kind == "delivery station" {
		typ = network.DeliveryStation
	}
	w.run(func() error {
		if err := w.svc.HandleNodeRegistered(w.ctx(), w.eventID(), usecases.NodeRegisteredInput{
			NodeID: id, NodeType: string(typ), Operator: string(network.OperatorNetwork), Timezone: nodeZone,
		}); err != nil {
			return err
		}
		return w.svc.HandleNodeActivated(w.ctx(), w.eventID(), usecases.NodeInput{NodeID: id})
	})
}

func (w *world) openLane(mode, id, origin, dest string) {
	w.lanes[id] = network.LaneView{ID: id, Origin: origin, Destination: dest, Open: true}
	w.run(func() error {
		return w.svc.HandleLaneOpened(w.ctx(), w.eventID(), usecases.LaneOpenedInput{
			LaneID: id, OriginNodeID: origin, DestNodeID: dest, Mode: mode, TransitMinutes: 120,
		})
	})
}

func (w *world) closeLane(id string) {
	w.run(func() error { return w.svc.HandleLaneClosed(w.ctx(), w.eventID(), usecases.LaneInput{LaneID: id}) })
}

func (w *world) nodeTransition(id, what string) {
	in := usecases.NodeInput{NodeID: id}
	w.run(func() error {
		switch what {
		case "activated":
			return w.svc.HandleNodeActivated(w.ctx(), w.eventID(), in)
		case "suspended":
			return w.svc.HandleNodeSuspended(w.ctx(), w.eventID(), in)
		default:
			return w.svc.HandleNodeRetired(w.ctx(), w.eventID(), in)
		}
	})
}

func (w *world) activateTwice(id string) {
	eventID := w.eventID()
	w.run(func() error { return w.svc.HandleNodeActivated(w.ctx(), eventID, usecases.NodeInput{NodeID: id}) })
	if w.lastErr != nil {
		return
	}
	first := len(w.published())
	w.run(func() error { return w.svc.HandleNodeActivated(w.ctx(), eventID, usecases.NodeInput{NodeID: id}) })
	if len(w.published()) != 0 {
		w.lastErr = fmt.Errorf("the redelivery published %d events (first delivery: %d)", len(w.published()), first)
	}
}

func (w *world) reviseProfile(id, revision string) {
	n, _ := strconv.Atoi(revision)
	w.run(func() error {
		return w.svc.HandleProfileDeclared(w.ctx(), w.eventID(), usecases.ProfileDeclaredInput{NodeID: id, Profile: weekdayProfile(n, nil)})
	})
}

func (w *world) hasPickups(origin, dest string) error {
	ds, err := w.departuresOf(origin, isKind(departure.KindPickup))
	if err != nil {
		return err
	}
	scheduled := 0
	for _, d := range ds {
		if d.State == string(departure.StateScheduled) {
			scheduled++
			if d.DestinationNodeID != dest {
				return fmt.Errorf("pickup %s goes to %s, want %s", d.DepartureID, d.DestinationNodeID, dest)
			}
		}
	}
	if scheduled == 0 {
		return fmt.Errorf("node %s has no scheduled pickup departure", origin)
	}
	return nil
}

func (w *world) hasNoPickups(origin string) error {
	ds, err := w.departuresOf(origin, func(d views.Departure) bool {
		return d.Kind == string(departure.KindPickup) && d.State == string(departure.StateScheduled)
	})
	if err != nil {
		return err
	}
	if len(ds) != 0 {
		return fmt.Errorf("node %s still has %d scheduled pickups", origin, len(ds))
	}
	return nil
}

func (w *world) pickupsCancelledBecause(origin, reason string) error {
	ds, err := w.departuresOf(origin, isKind(departure.KindPickup))
	if err != nil {
		return err
	}
	if len(ds) == 0 {
		return fmt.Errorf("node %s has no pickup at all", origin)
	}
	for _, d := range ds {
		if d.State != string(departure.StateCancelled) || d.CancelReason != reason {
			return fmt.Errorf("pickup %s is %s (%q), want CANCELLED because %q", d.DepartureID, d.State, d.CancelReason, reason)
		}
	}
	return nil
}

func (w *world) scheduledOnce(origin string) error {
	ds, err := w.departuresOf(origin, func(views.Departure) bool { return true })
	if err != nil {
		return err
	}
	n := 0
	for _, e := range w.store.Outbox().Events() {
		if e.EventName() == "DepartureScheduled" {
			n++
		}
	}
	if len(ds) == 0 || n != len(ds) {
		return fmt.Errorf("%d departures stored but %d DepartureScheduled events", len(ds), n)
	}
	return nil
}
