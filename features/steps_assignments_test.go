package features_test

import (
	"fmt"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// registerAssignmentSteps adds the FulfillmentReleased / Reassigned /
// Cancelled and LabelIssued / LabelVoided projections (ADR 0002 A15-A18):
// view updates only, none of them publishes an event.
func registerAssignmentSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the assignment "([^"]*)" is released to node "([^"]*)" on the pickup departure "([^"]*)" as release (\d+)$`, w.release)
	sc.Step(`^the assignment "([^"]*)" is reassigned to node "([^"]*)"$`, w.reassign)
	sc.Step(`^the assignment "([^"]*)" is cancelled by order-sourcing$`, w.sourcingCancel)
	sc.Step(`^a label with tracking number "([^"]*)" is issued for the assignment "([^"]*)"$`, w.labelIssued)
	sc.Step(`^the label with tracking number "([^"]*)" is voided$`, w.labelVoided)
	sc.Step(`^the assignment "([^"]*)" is planned on the pickup departure "([^"]*)"$`, w.plannedOn)
	sc.Step(`^the assignment "([^"]*)" has no planned departure$`, w.noPlannedDeparture)
	sc.Step(`^the assignment "([^"]*)" is at node "([^"]*)"$`, w.atNode)
	sc.Step(`^the assignment "([^"]*)" tracks the label "([^"]*)"$`, w.tracksLabel)
	sc.Step(`^the assignment "([^"]*)" tracks no labels$`, w.tracksNoLabels)
}

func (w *world) release(assignment, node, departureAlias, releaseNo string) {
	id := w.depID(departureAlias)
	n, _ := strconv.Atoi(releaseNo)
	w.run(func() error {
		return w.svc.HandleReleased(w.ctx(), w.eventID(), usecases.ReleasedInput{
			AssignmentID: assignment,
			ReleaseNo:    n,
			Facts:        volume.Facts{OrderID: "or-" + assignment, GroupNo: 1, NodeID: node, DepartureID: departure.ID(id)},
		})
	})
}

func (w *world) reassign(assignment, node string) {
	w.run(func() error {
		return w.svc.HandleReassigned(w.ctx(), w.eventID(), usecases.ReassignedInput{AssignmentID: assignment, ToNodeID: node})
	})
}

func (w *world) sourcingCancel(assignment string) {
	w.run(func() error {
		return w.svc.HandleCancelled(w.ctx(), w.eventID(), usecases.AssignmentInput{AssignmentID: assignment})
	})
}

func (w *world) labelIssued(tracking, assignment string) {
	w.run(func() error {
		return w.svc.HandleLabelIssued(w.ctx(), w.eventID(), usecases.LabelIssuedInput{AssignmentID: assignment, TrackingNumber: tracking})
	})
}

func (w *world) labelVoided(tracking string) {
	w.run(func() error {
		return w.svc.HandleLabelVoided(w.ctx(), w.eventID(), usecases.LabelVoidedInput{TrackingNumber: tracking})
	})
}

func (w *world) plannedOn(assignment, departureAlias string) error {
	s, err := w.volumeOf(assignment)
	if err != nil {
		return err
	}
	if string(s.DepartureID) != w.depID(departureAlias) {
		return fmt.Errorf("assignment %s is planned on %q, want %s (%s)", assignment, s.DepartureID, departureAlias, w.depID(departureAlias))
	}
	return nil
}

func (w *world) noPlannedDeparture(assignment string) error {
	s, err := w.volumeOf(assignment)
	if err != nil {
		return err
	}
	if s.DepartureID != "" {
		return fmt.Errorf("assignment %s is still planned on %q, want no planned departure", assignment, s.DepartureID)
	}
	return nil
}

func (w *world) atNode(assignment, node string) error {
	s, err := w.volumeOf(assignment)
	if err != nil {
		return err
	}
	if s.NodeID != node {
		return fmt.Errorf("assignment %s is at node %q, want %q", assignment, s.NodeID, node)
	}
	return nil
}

func (w *world) tracksLabel(assignment, tracking string) error {
	s, err := w.volumeOf(assignment)
	if err != nil {
		return err
	}
	for _, tn := range s.TrackingNumbers {
		if tn == tracking {
			return nil
		}
	}
	return fmt.Errorf("assignment %s tracks %v, want %q among them", assignment, s.TrackingNumbers, tracking)
}

func (w *world) tracksNoLabels(assignment string) error {
	s, err := w.volumeOf(assignment)
	if err != nil {
		return err
	}
	if len(s.TrackingNumbers) != 0 {
		return fmt.Errorf("assignment %s still tracks %v, want none", assignment, s.TrackingNumbers)
	}
	return nil
}
