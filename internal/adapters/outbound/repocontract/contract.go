// Package repocontract is the behavioural contract of the repository ports:
// one function, Run, that takes a Factory of a fresh empty fixture and checks
// every promise of internal/application/ports. The in-memory doubles pass it in
// their own tests and the Postgres adapters pass it in their integration tests,
// so the two can never drift. It is test support: cmd/** must never import it.
package repocontract

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
)

// Fixture is one implementation of every port, over ONE empty database.
type Fixture struct {
	Departures ports.DepartureRepository
	Schedules  ports.LaneScheduleRepository
	Loads      ports.LoadRepository
	Nodes      ports.NodeViewRepository
	Lanes      ports.LaneViewRepository
	Volumes    ports.VolumeRepository
	Inbox      ports.ProcessedEvents
	Outbox     ports.EventOutbox
	UoW        ports.UnitOfWork
	// OutboxCount returns how many events are stored in the outbox.
	OutboxCount func() (int, error)
}

// Factory returns a fresh, empty Fixture and registers its own cleanup with t.
type Factory func(t *testing.T) Fixture

// named is one contract case.
type named struct {
	name string
	run  func(*testing.T, Fixture)
}

// Run executes the whole contract; every case gets a fresh Fixture.
func Run(t *testing.T, factory Factory) {
	t.Helper()
	groups := []struct {
		name  string
		cases []named
	}{
		{"Departure", departureCases},
		{"LaneSchedule", scheduleCases},
		{"Load", loadCases},
		{"Views", viewCases},
		{"ExpectedVolume", volumeCases},
		{"Inbox", inboxCases},
		{"UnitOfWork", unitOfWorkCases},
	}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			for _, c := range g.cases {
				t.Run(c.name, func(t *testing.T) { c.run(t, factory(t)) })
			}
		})
	}
}

// at is the fixed instant of the contract: whole seconds, so every database
// keeps it exactly.
var at = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// wantErr fails unless err matches target.
func wantErr(t testing.TB, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

// noErr fails on err.
func noErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
