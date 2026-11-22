package features_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// rulesState is what the documentation-derived scenarios remember besides the
// shared world: the last list and the pages of the last paged walk.
type rulesState struct {
	list  []views.Departure
	pages []int
	seen  []string
}

// registerRulesSteps adds the vocabulary of the documentation-derived rule
// scenarios (booking, pickup, schedule, load, projection and read rules).
func registerRulesSteps(sc *godog.ScenarioContext, w *world) {
	registerRulesNetworkSteps(sc, w)
	registerRulesDepartureSteps(sc, w)
	registerRulesEventSteps(sc, w)
	registerRulesLoadSteps(sc, w)
	registerRulesReadSteps(sc, w)
}

func registerRulesNetworkSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^an active (sortation center|delivery station|air hub) "([^"]*)" in zone "([^"]*)"$`, w.activeNodeIn)
	sc.Step(`^an active air hub "([^"]*)"$`, func(id string) { w.activeNodeIn("air hub", id, nodeZone) })
	sc.Step(`^a (vendor|network)-operated fulfillment center "([^"]*)" is registered in zone "([^"]*)"$`, w.registerFC)
	sc.Step(`^"([^"]*)" declares profile revision (\d+) opening weekdays from "([^"]*)" to "([^"]*)" with a cap of (\d+) orders per (HOUR|DAY)(?: and holidays "([^"]*)")? and the cutoffs:$`, w.declareProfile)
	sc.Step(`^an open (GROUND|AIR) lane "([^"]*)" from "([^"]*)" to "([^"]*)" taking (\d+) minutes$`, w.openLaneTaking)
	sc.Step(`^a node "([^"]*)" of type "([^"]*)" run by "([^"]*)" is registered$`, w.registerRaw)
	sc.Step(`^a lane "([^"]*)" from "([^"]*)" to "([^"]*)" in mode "([^"]*)" taking (-?\d+) minutes is opened$`, w.openLaneRaw)
	sc.Step(`^the service books (\d+) packages per order$`, func(n int) { w.svc.PackagesPerOrder = n })
	sc.Step(`^the node "([^"]*)" has (\d+) scheduled pickup departures?$`, w.scheduledPickupCount)
	sc.Step(`^the node "([^"]*)" has (\d+) pickup departures cancelled because "([^"]*)"$`, w.cancelledPickupCount)
	sc.Step(`^the earliest scheduled pickup of "([^"]*)" is cut off at "([^"]*)"$`, w.earliestPickup)
	sc.Step(`^the lane "([^"]*)" has (\d+) (LINEHAUL|AIR|PICKUP) departures?$`, w.laneDepartureCount)
	sc.Step(`^the lane "([^"]*)" has a departure cutting off at "([^"]*)"$`, w.laneHasCutoff)
	sc.Step(`^the lane "([^"]*)" has no departure cutting off at "([^"]*)"$`, w.laneHasNoCutoff)
	sc.Step(`^the departure "([^"]*)" is the one on lane "([^"]*)" cutting off at "([^"]*)"$`, w.aliasByCutoff)
	sc.Step(`^the departure "([^"]*)" leaves at "([^"]*)" and arrives at "([^"]*)"$`, w.leavesAndArrives)
	sc.Step(`^the departure "([^"]*)" has capacity (\d+)$`, w.hasCapacity)
}

func nodeTypeOf(kind string) network.NodeType {
	switch kind {
	case "delivery station":
		return network.DeliveryStation
	case "air hub":
		return network.AirHub
	}
	return network.SortationCenter
}

func (w *world) activeNodeIn(kind, id, zone string) {
	w.run(func() error {
		if err := w.svc.HandleNodeRegistered(w.ctx(), w.eventID(), usecases.NodeRegisteredInput{
			NodeID: id, NodeType: string(nodeTypeOf(kind)), Operator: string(network.OperatorNetwork), Timezone: zone,
		}); err != nil {
			return err
		}
		return w.svc.HandleNodeActivated(w.ctx(), w.eventID(), usecases.NodeInput{NodeID: id})
	})
}

func (w *world) registerFC(operator, id, zone string) {
	op := network.OperatorVendor
	if operator == "network" {
		op = network.OperatorNetwork
	}
	w.run(func() error {
		return w.svc.HandleNodeRegistered(w.ctx(), w.eventID(), usecases.NodeRegisteredInput{
			NodeID: id, NodeType: string(network.FulfillmentCenter), Operator: string(op), Timezone: zone,
		})
	})
}

func (w *world) registerRaw(id, nodeType, operator string) {
	w.run(func() error {
		return w.svc.HandleNodeRegistered(w.ctx(), w.eventID(), usecases.NodeRegisteredInput{
			NodeID: id, NodeType: nodeType, Operator: operator, Timezone: nodeZone,
		})
	})
}

// declareProfile delivers NodeOperationalProfileDeclared with weekday opening
// hours, a throughput cap, optional holidays and the cutoffs of the table
// (columns localTime, days as a comma list, maxOrders).
func (w *world) declareProfile(node, revision, from, to, capValue, unit, holidays string, tbl *godog.Table) error {
	n, err := w.svc.Nodes.Get(w.ctx(), node)
	if err != nil {
		return fmt.Errorf("node %s must be registered first: %w", node, err)
	}
	rev, _ := strconv.Atoi(revision)
	value, _ := strconv.Atoi(capValue)
	hours := map[string]network.Hours{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday"} {
		hours[d] = network.Hours{Start: from, End: to}
	}
	var cutoffs []network.Cutoff
	for _, row := range tbl.Rows[1:] {
		c := network.Cutoff{LocalTime: row.Cells[0].Value}
		if days := strings.TrimSpace(row.Cells[1].Value); days != "" {
			c.Days = strings.Split(days, ",")
		}
		if max := strings.TrimSpace(row.Cells[2].Value); max != "" {
			c.MaxOrders, _ = strconv.Atoi(max)
		}
		cutoffs = append(cutoffs, c)
	}
	p := network.Profile{Revision: rev, Timezone: n.Timezone, Hours: hours, Cutoffs: cutoffs, Cap: network.ThroughputCap{Value: value, TimeUnit: unit}}
	if holidays != "" {
		p.Holidays = strings.Split(holidays, ",")
	}
	w.run(func() error {
		return w.svc.HandleProfileDeclared(w.ctx(), w.eventID(), usecases.ProfileDeclaredInput{NodeID: node, Profile: p})
	})
	return nil
}

func (w *world) openLaneTaking(mode, id, origin, dest, minutes string) {
	n, _ := strconv.Atoi(minutes)
	w.lanes[id] = network.LaneView{ID: id, Origin: origin, Destination: dest, Mode: network.Mode(mode), TransitMinutes: n, Open: true}
	w.run(func() error {
		return w.svc.HandleLaneOpened(w.ctx(), w.eventID(), usecases.LaneOpenedInput{
			LaneID: id, OriginNodeID: origin, DestNodeID: dest, Mode: mode, TransitMinutes: n,
		})
	})
}

func (w *world) openLaneRaw(id, origin, dest, mode, minutes string) {
	n, _ := strconv.Atoi(minutes)
	w.run(func() error {
		return w.svc.HandleLaneOpened(w.ctx(), w.eventID(), usecases.LaneOpenedInput{
			LaneID: id, OriginNodeID: origin, DestNodeID: dest, Mode: mode, TransitMinutes: n,
		})
	})
}

func mustTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return t, fmt.Errorf("%q is not an RFC 3339 instant: %w", s, err)
	}
	return t, nil
}

func (w *world) scheduledPickupCount(origin, count string) error {
	want, _ := strconv.Atoi(count)
	ds, err := w.departuresOf(origin, func(d views.Departure) bool {
		return d.Kind == string(departure.KindPickup) && d.State == string(departure.StateScheduled)
	})
	if err != nil {
		return err
	}
	if len(ds) != want {
		return fmt.Errorf("node %s has %d scheduled pickups, want %d", origin, len(ds), want)
	}
	return nil
}

func (w *world) cancelledPickupCount(origin, count, reason string) error {
	want, _ := strconv.Atoi(count)
	ds, err := w.departuresOf(origin, func(d views.Departure) bool {
		return d.Kind == string(departure.KindPickup) && d.State == string(departure.StateCancelled) && d.CancelReason == reason
	})
	if err != nil {
		return err
	}
	if len(ds) != want {
		return fmt.Errorf("node %s has %d pickups cancelled because %q, want %d", origin, len(ds), reason, want)
	}
	return nil
}

func (w *world) earliestPickup(origin, instant string) error {
	want, err := mustTime(instant)
	if err != nil {
		return err
	}
	ds, err := w.departuresOf(origin, func(d views.Departure) bool {
		return d.Kind == string(departure.KindPickup) && d.State == string(departure.StateScheduled)
	})
	if err != nil {
		return err
	}
	if len(ds) == 0 {
		return fmt.Errorf("node %s has no scheduled pickup", origin)
	}
	if !ds[0].CutoffAt.Equal(want) {
		return fmt.Errorf("the earliest pickup of %s is cut off at %s, want %s", origin, ds[0].CutoffAt.Format(time.RFC3339), instant)
	}
	return nil
}

// onLane lists the departures of a lane (any state, any kind) by cutoff.
func (w *world) onLane(laneAlias string) ([]views.Departure, error) {
	lane, ok := w.lanes[laneAlias]
	if !ok {
		return nil, fmt.Errorf("the scenario never opened lane %q", laneAlias)
	}
	return w.departuresOf(lane.Origin, func(d views.Departure) bool { return d.LaneID == laneAlias })
}

func (w *world) laneDepartureCount(laneAlias, count, kind string) error {
	want, _ := strconv.Atoi(count)
	ds, err := w.onLane(laneAlias)
	if err != nil {
		return err
	}
	got := 0
	for _, d := range ds {
		if d.Kind == kind {
			got++
		}
	}
	if got != want {
		return fmt.Errorf("lane %s has %d %s departures, want %d", laneAlias, got, kind, want)
	}
	return nil
}

func (w *world) findByCutoff(laneAlias, instant string) (*views.Departure, error) {
	at, err := mustTime(instant)
	if err != nil {
		return nil, err
	}
	ds, err := w.onLane(laneAlias)
	if err != nil {
		return nil, err
	}
	for i := range ds {
		if ds[i].CutoffAt.Equal(at) {
			return &ds[i], nil
		}
	}
	return nil, nil
}

func (w *world) laneHasCutoff(laneAlias, instant string) error {
	d, err := w.findByCutoff(laneAlias, instant)
	if err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("lane %s has no departure cutting off at %s", laneAlias, instant)
	}
	return nil
}

func (w *world) laneHasNoCutoff(laneAlias, instant string) error {
	d, err := w.findByCutoff(laneAlias, instant)
	if err != nil {
		return err
	}
	if d != nil {
		return fmt.Errorf("lane %s has departure %s cutting off at %s, want none", laneAlias, d.DepartureID, instant)
	}
	return nil
}

func (w *world) aliasByCutoff(alias, laneAlias, instant string) error {
	d, err := w.findByCutoff(laneAlias, instant)
	if err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("lane %s has no departure cutting off at %s", laneAlias, instant)
	}
	w.departures[alias] = d.DepartureID
	return nil
}

func (w *world) leavesAndArrives(alias, departs, arrives string) error {
	d, err := w.svc.GetDeparture(w.ctx(), w.depID(alias))
	if err != nil {
		return err
	}
	wantDeparts, err := mustTime(departs)
	if err != nil {
		return err
	}
	wantArrives, err := mustTime(arrives)
	if err != nil {
		return err
	}
	if !d.DepartsAt.Equal(wantDeparts) || !d.ArrivesAt.Equal(wantArrives) {
		return fmt.Errorf("departure %s leaves at %s and arrives at %s, want %s and %s", alias,
			d.DepartsAt.Format(time.RFC3339), d.ArrivesAt.Format(time.RFC3339), departs, arrives)
	}
	return nil
}

func (w *world) hasCapacity(alias, capacity string) error {
	want, _ := strconv.Atoi(capacity)
	d, err := w.svc.GetDeparture(w.ctx(), w.depID(alias))
	if err != nil {
		return err
	}
	if d.CapacityPackages != want {
		return fmt.Errorf("departure %s has capacity %d, want %d", alias, d.CapacityPackages, want)
	}
	return nil
}

func registerRulesDepartureSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the capacity of "([^"]*)" is changed to (-\d+)$`, w.changeCapacity)
	sc.Step(`^the departure "([^"]*)" is cancelled with a reason of (\d+) characters$`, w.cancelWithLongReason)
	sc.Step(`^a label with tracking number "([^"]*)" is issued for an assignment whose id has (\d+) characters$`, w.labelForLongID)
	sc.Step(`^the lock of "([^"]*)" on "([^"]*)" is delivered twice with the same event id$`, w.lockTwice)
	sc.Step(`^the assignment "([^"]*)" is locked on the unknown departure "([^"]*)" at node "([^"]*)"$`, w.lockUnknown)
	sc.Step(`^I define the schedule of "([^"]*)" with the slots:$`, w.defineWithTable)
	sc.Step(`^I define the schedule of "([^"]*)" with (\d+) slots on "([^"]*)" ten minutes apart$`, w.defineSpaced)
}

func (w *world) cancelWithLongReason(alias, n string) {
	count, _ := strconv.Atoi(n)
	w.cancelDeparture(alias, strings.Repeat("r", count))
}

func (w *world) labelForLongID(tracking, n string) {
	count, _ := strconv.Atoi(n)
	w.labelIssued(tracking, strings.Repeat("a", count))
}

func (w *world) lockTwice(assignment, alias string) {
	id := w.depID(alias)
	eventID := w.eventID()
	deliver := func() error {
		d, err := w.svc.GetDeparture(w.ctx(), id)
		if err != nil {
			return err
		}
		return w.svc.HandleLocked(w.ctx(), eventID, usecases.LockedInput{
			AssignmentID: assignment,
			Facts:        volume.Facts{OrderID: "or-" + assignment, GroupNo: 1, NodeID: d.OriginNodeID, DepartureID: departure.ID(id)},
		})
	}
	w.run(deliver)
	if w.lastErr != nil {
		return
	}
	w.run(deliver)
}

func (w *world) lockUnknown(assignment, ghost, node string) {
	w.run(func() error {
		return w.svc.HandleLocked(w.ctx(), w.eventID(), usecases.LockedInput{
			AssignmentID: assignment,
			Facts:        volume.Facts{OrderID: "or-" + assignment, GroupNo: 1, NodeID: node, DepartureID: departure.ID(ghost)},
		})
	})
}

func (w *world) defineWithTable(lane string, tbl *godog.Table) {
	var slots []usecases.SlotInput
	for _, row := range tbl.Rows[1:] {
		c := row.Cells
		slots = append(slots, slotInput(c[0].Value, c[1].Value, c[2].Value, c[3].Value, c[4].Value))
	}
	w.run(func() error {
		_, _, err := w.svc.DefineLaneSchedule(w.ctx(), usecases.DefineLaneScheduleCommand{LaneID: lane, Slots: slots})
		return err
	})
}

// defineSpaced defines n slots of one weekday whose cutoff and departure are
// the same minute, ten minutes after the previous slot, so none overlap.
func (w *world) defineSpaced(lane, count, day string) {
	n, _ := strconv.Atoi(count)
	slots := make([]usecases.SlotInput, 0, n)
	for i := 0; i < n; i++ {
		minute := i * 10
		clock := fmt.Sprintf("%02d:%02d", minute/60, minute%60)
		slots = append(slots, usecases.SlotInput{DayOfWeek: day, CutoffLocalTime: clock, DepartsLocalTime: clock, ArrivesLocalTime: "23:59", CapacityPackages: 5})
	}
	w.run(func() error {
		_, _, err := w.svc.DefineLaneSchedule(w.ctx(), usecases.DefineLaneScheduleCommand{LaneID: lane, Slots: slots})
		return err
	})
}

func registerRulesEventSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the last command published (\d+) "([A-Za-z]+)" events?$`, w.publishedCount)
	sc.Step(`^the last command published BookingMoved of "([^"]*)" from "([^"]*)" to "([^"]*)"$`, w.bookingMoved)
	sc.Step(`^the last command published BookingFailed of "([^"]*)" on "([^"]*)" because "([^"]*)"$`, w.bookingFailed)
	sc.Step(`^the last DepartureCapacityChanged of "([^"]*)" reports capacity (\d+), booked (\d+) and remaining (\d+)$`, w.capacityChanged)
	sc.Step(`^no DepartureCapacityChanged was published for "([^"]*)"$`, w.noCapacityChanged)
	sc.Step(`^the last command published DepartureCancelled of "([^"]*)" with reason "([^"]*)" at "([^"]*)"$`, w.departureCancelled)
	sc.Step(`^the last command scheduled a pickup on lane "([^"]*)" from "([^"]*)" to "([^"]*)" cutting off at "([^"]*)", leaving at "([^"]*)", arriving at "([^"]*)" with capacity (\d+)$`, w.pickupScheduled)
}

func (w *world) publishedCount(count, name string) error {
	want, _ := strconv.Atoi(count)
	got := 0
	for _, n := range w.publishedNames() {
		if n == name {
			got++
		}
	}
	if got != want {
		return fmt.Errorf("the last command published %d %s, want %d (all: %v)", got, name, want, w.publishedNames())
	}
	return nil
}

func (w *world) bookingMoved(assignment, from, to string) error {
	for _, e := range w.published() {
		if m, ok := e.(volume.BookingMoved); ok && m.AssignmentID == assignment {
			if string(m.FromDepartureID) != w.depID(from) || string(m.ToDepartureID) != w.depID(to) {
				return fmt.Errorf("BookingMoved of %s goes %s -> %s, want %s -> %s", assignment, m.FromDepartureID, m.ToDepartureID, w.depID(from), w.depID(to))
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published %v, no BookingMoved of %s", w.publishedNames(), assignment)
}

func (w *world) bookingFailed(assignment, on, reason string) error {
	for _, e := range w.published() {
		if f, ok := e.(volume.BookingFailed); ok && f.AssignmentID == assignment {
			if string(f.DepartureID) != w.depID(on) || string(f.Reason) != reason {
				return fmt.Errorf("BookingFailed of %s is on %s because %s, want %s because %s", assignment, f.DepartureID, f.Reason, w.depID(on), reason)
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published %v, no BookingFailed of %s", w.publishedNames(), assignment)
}

func (w *world) capacityChanged(alias string, capacity, booked, remaining int) error {
	id := w.depID(alias)
	evs := w.published()
	for i := len(evs) - 1; i >= 0; i-- {
		if e, ok := evs[i].(departure.DepartureCapacityChanged); ok && string(e.DepartureID) == id {
			if e.CapacityPackages != capacity || e.BookedPackages != booked || e.RemainingPackages != remaining {
				return fmt.Errorf("DepartureCapacityChanged of %s reports capacity %d booked %d remaining %d, want %d %d %d",
					alias, e.CapacityPackages, e.BookedPackages, e.RemainingPackages, capacity, booked, remaining)
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published %v, no DepartureCapacityChanged of %s", w.publishedNames(), alias)
}

func (w *world) noCapacityChanged(alias string) error {
	id := w.depID(alias)
	for _, e := range w.published() {
		if c, ok := e.(departure.DepartureCapacityChanged); ok && string(c.DepartureID) == id {
			return fmt.Errorf("the last command published DepartureCapacityChanged for %s, want none", alias)
		}
	}
	return nil
}

func (w *world) departureCancelled(alias, reason, instant string) error {
	at, err := mustTime(instant)
	if err != nil {
		return err
	}
	id := w.depID(alias)
	for _, e := range w.published() {
		if c, ok := e.(departure.DepartureCancelled); ok && string(c.DepartureID) == id {
			if c.Reason != reason || !c.CancelledAt.Equal(at) {
				return fmt.Errorf("DepartureCancelled of %s has reason %q at %s, want %q at %s", alias, c.Reason, c.CancelledAt.Format(time.RFC3339), reason, instant)
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published %v, no DepartureCancelled of %s", w.publishedNames(), alias)
}

func (w *world) pickupScheduled(laneID, origin, dest, cutoff, departs, arrives string, capacity int) error {
	cut, err := mustTime(cutoff)
	if err != nil {
		return err
	}
	for _, e := range w.published() {
		s, ok := e.(departure.DepartureScheduled)
		if !ok || !s.CutoffAt.Equal(cut) {
			continue
		}
		if s.Kind != departure.KindPickup || s.LaneID != laneID || s.OriginNodeID != origin || s.DestinationNodeID != dest ||
			s.DepartsAt.Format(time.RFC3339) != departs || s.ArrivesAt.Format(time.RFC3339) != arrives || s.CapacityPackages != capacity {
			return fmt.Errorf("DepartureScheduled is %s on %s %s->%s cutoff %s departs %s arrives %s capacity %d, want a PICKUP on %s %s->%s leaving %s arriving %s capacity %d",
				s.Kind, s.LaneID, s.OriginNodeID, s.DestinationNodeID, s.CutoffAt.Format(time.RFC3339), s.DepartsAt.Format(time.RFC3339),
				s.ArrivesAt.Format(time.RFC3339), s.CapacityPackages, laneID, origin, dest, departs, arrives, capacity)
		}
		return nil
	}
	return fmt.Errorf("the last command published no DepartureScheduled cutting off at %s", cutoff)
}

func registerRulesLoadSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the load "([^"]*)" holds (\d+) packages?$`, w.loadHolds)
	sc.Step(`^the load "([^"]*)" was opened with capacity (\d+)$`, w.loadCapacity)
	sc.Step(`^a tracking number of (\d+) characters is added to the load "([^"]*)"$`, w.addLongTracking)
	sc.Step(`^I (add a package to|close|dispatch|receive) the unknown load "([^"]*)"$`, w.commandOnUnknownLoad)
	sc.Step(`^the last LoadClosed of "([^"]*)" counts (\d+) packages?$`, w.loadClosedCounts)
	sc.Step(`^the last LoadDeparted of "([^"]*)" lists "([^"]*)" from node "([^"]*)"$`, w.departedLists)
	sc.Step(`^the last LoadArrived of "([^"]*)" lists "([^"]*)" at node "([^"]*)"$`, w.arrivedLists)
}

func (w *world) loadOf(alias string) (*load.Load, error) {
	return w.store.Loads().Get(w.ctx(), load.ID(w.loadID(alias)))
}

func (w *world) loadHolds(alias, count string) error {
	want, _ := strconv.Atoi(count)
	l, err := w.loadOf(alias)
	if err != nil {
		return err
	}
	if l.PackageCount() != want {
		return fmt.Errorf("load %s holds %d packages, want %d", alias, l.PackageCount(), want)
	}
	return nil
}

func (w *world) loadCapacity(alias, capacity string) error {
	want, _ := strconv.Atoi(capacity)
	l, err := w.loadOf(alias)
	if err != nil {
		return err
	}
	if l.Capacity() != want {
		return fmt.Errorf("load %s was opened with capacity %d, want %d", alias, l.Capacity(), want)
	}
	return nil
}

func (w *world) addLongTracking(n, alias string) {
	count, _ := strconv.Atoi(n)
	w.addPackage(strings.Repeat("T", count), alias)
}

func (w *world) commandOnUnknownLoad(command, id string) {
	w.run(func() error {
		var err error
		switch command {
		case "add a package to":
			_, err = w.svc.AddPackage(w.ctx(), id, "TRK-ghost")
		case "close":
			_, err = w.svc.CloseLoad(w.ctx(), id)
		case "dispatch":
			_, err = w.svc.DispatchLoad(w.ctx(), id)
		default:
			_, err = w.svc.ReceiveLoad(w.ctx(), id)
		}
		return err
	})
}

func (w *world) loadClosedCounts(alias, count string) error {
	want, _ := strconv.Atoi(count)
	id := w.loadID(alias)
	for _, e := range w.published() {
		if c, ok := e.(load.LoadClosed); ok && string(c.LoadID) == id {
			if c.PackageCount != want {
				return fmt.Errorf("LoadClosed counts %d packages, want %d", c.PackageCount, want)
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published %v, no LoadClosed of %s", w.publishedNames(), alias)
}

func (w *world) departedLists(alias, trackings, node string) error {
	id := w.loadID(alias)
	for _, e := range w.published() {
		if d, ok := e.(load.LoadDeparted); ok && string(d.LoadID) == id {
			if strings.Join(d.TrackingNumbers, ",") != trackings || d.OriginNodeID != node {
				return fmt.Errorf("LoadDeparted lists %v from %s, want %s from %s", d.TrackingNumbers, d.OriginNodeID, trackings, node)
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published %v, no LoadDeparted of %s", w.publishedNames(), alias)
}

func (w *world) arrivedLists(alias, trackings, node string) error {
	id := w.loadID(alias)
	for _, e := range w.published() {
		if a, ok := e.(load.LoadArrived); ok && string(a.LoadID) == id {
			if strings.Join(a.TrackingNumbers, ",") != trackings || a.DestinationNodeID != node {
				return fmt.Errorf("LoadArrived lists %v at %s, want %s at %s", a.TrackingNumbers, a.DestinationNodeID, trackings, node)
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published %v, no LoadArrived of %s", w.publishedNames(), alias)
}

func registerRulesReadSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I list the departures with origin "([^"]*)"$`, w.listOrigin)
	sc.Step(`^I list the departures with destination "([^"]*)"$`, w.listDestination)
	sc.Step(`^I list the departures with a cutoff from "([^"]*)" until "([^"]*)"$`, w.listWindow)
	sc.Step(`^I list the departures with a limit of (-?\d+)$`, w.listLimit)
	sc.Step(`^I page the departures with a limit of (-?\d+)$`, w.pageLimit)
	sc.Step(`^I page through the departures of origin "([^"]*)" (\d+) at a time$`, w.pageThrough)
	sc.Step(`^I look up the departure "([^"]*)"$`, w.lookUp)
	sc.Step(`^the list holds (\d+) departures?$`, w.listHolds)
	sc.Step(`^the list is ordered by cutoff$`, w.listOrdered)
	sc.Step(`^the pages hold "([^"]*)" departures without repeats$`, w.pagesHold)
}

func (w *world) list(q usecases.ListDeparturesQuery) {
	w.run(func() error {
		l, err := w.svc.ListDepartures(w.ctx(), q)
		w.rules.list = l.Departures
		return err
	})
}

func (w *world) listOrigin(origin string) {
	w.list(usecases.ListDeparturesQuery{Origin: origin, Limit: 1000})
}
func (w *world) listDestination(d string) {
	w.list(usecases.ListDeparturesQuery{Destination: d, Limit: 1000})
}
func (w *world) listLimit(limit string) {
	n, _ := strconv.Atoi(limit)
	w.list(usecases.ListDeparturesQuery{Limit: n})
}
func (w *world) lookUp(alias string) {
	w.run(func() error {
		_, err := w.svc.GetDeparture(w.ctx(), w.depID(alias))
		return err
	})
}

func (w *world) listWindow(from, to string) error {
	f, err := mustTime(from)
	if err != nil {
		return err
	}
	t, err := mustTime(to)
	if err != nil {
		return err
	}
	w.list(usecases.ListDeparturesQuery{From: &f, To: &t, Limit: 1000})
	return nil
}

func (w *world) pageLimit(limit string) {
	n, _ := strconv.Atoi(limit)
	w.run(func() error {
		l, _, err := w.svc.ListDeparturesPage(w.ctx(), usecases.ListDeparturesQuery{Limit: n})
		w.rules.list = l.Departures
		return err
	})
}

func (w *world) pageThrough(origin, size string) {
	n, _ := strconv.Atoi(size)
	w.rules.pages, w.rules.seen = nil, nil
	w.run(func() error {
		var cursor *repository.DepartureCursor
		for i := 0; i < 100; i++ {
			l, next, err := w.svc.ListDeparturesPage(w.ctx(), usecases.ListDeparturesQuery{Origin: origin, Limit: n, After: cursor})
			if err != nil {
				return err
			}
			w.rules.pages = append(w.rules.pages, len(l.Departures))
			for _, d := range l.Departures {
				w.rules.seen = append(w.rules.seen, d.DepartureID)
			}
			if next == nil {
				return nil
			}
			cursor = next
		}
		return fmt.Errorf("the walk never reached the last page")
	})
}

func (w *world) listHolds(count string) error {
	want, _ := strconv.Atoi(count)
	if len(w.rules.list) != want {
		return fmt.Errorf("the list holds %d departures, want %d", len(w.rules.list), want)
	}
	return nil
}

func (w *world) listOrdered() error {
	l := w.rules.list
	for i := 1; i < len(l); i++ {
		if l[i].CutoffAt.Before(l[i-1].CutoffAt) {
			return fmt.Errorf("departure %s (%s) is listed after %s (%s)", l[i].DepartureID, l[i].CutoffAt, l[i-1].DepartureID, l[i-1].CutoffAt)
		}
	}
	return nil
}

func (w *world) pagesHold(sizes string) error {
	var got []string
	for _, n := range w.rules.pages {
		got = append(got, strconv.Itoa(n))
	}
	if strings.Join(got, ",") != sizes {
		return fmt.Errorf("the pages hold %s departures, want %s", strings.Join(got, ","), sizes)
	}
	sorted := slices.Clone(w.rules.seen)
	slices.Sort(sorted)
	if len(slices.Compact(sorted)) != len(w.rules.seen) {
		return fmt.Errorf("a departure appears on two pages: %v", w.rules.seen)
	}
	return nil
}
