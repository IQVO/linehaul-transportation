package mcp_test

import (
	"testing"
	"time"
)

func TestGetDeparture(t *testing.T) {
	h := newHarness(t)
	h.seedDeparture(t, "dep-1", time.Hour, 120)
	got := h.ok(t, "get_departure", map[string]any{"departureId": "dep-1"})
	for k, v := range map[string]any{
		"departureId": "dep-1", "laneId": "lane-1", "kind": "LINEHAUL",
		"originNodeId": "n-a", "destinationNodeId": "n-b",
		"cutoffAt": "2026-10-24T13:00:00Z", "departsAt": "2026-10-24T13:00:00Z", "arrivesAt": "2026-10-24T14:00:00Z",
		"capacityPackages": 120.0, "bookedPackages": 0.0, "remainingPackages": 120.0, "state": "SCHEDULED",
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	for _, absent := range []string{"cancelReason", "cancelledAt", "departedAt", "arrivedAt"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s must be absent on a SCHEDULED departure: %v", absent, got)
		}
	}
}

func TestGetDepartureErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_departure", map[string]any{"departureId": "dep-nope"}, "not-found")
}

func TestListDeparturesFiltersAndWindows(t *testing.T) {
	h := newHarness(t)
	h.seedDeparture(t, "dep-1", time.Hour, 120)
	h.seedDeparture(t, "dep-2", 2*time.Hour, 10)

	all := h.ok(t, "list_departures", map[string]any{})
	items := all["departures"].([]any)
	if len(items) != 2 {
		t.Fatalf("list_departures = %d items, want 2: %v", len(items), all)
	}
	if items[0].(map[string]any)["departureId"] != "dep-1" {
		t.Fatalf("departures are not ordered by cutoffAt: %v", all)
	}

	// a cutoff window [from, to): dep-1 only
	window := h.ok(t, "list_departures", map[string]any{
		"from": "2026-10-24T12:30:00Z", "to": "2026-10-24T13:30:00Z",
	})
	if got := len(window["departures"].([]any)); got != 1 || window["departures"].([]any)[0].(map[string]any)["departureId"] != "dep-1" {
		t.Fatalf("from/to window = %v, want only dep-1", window)
	}

	// limit truncates
	if got := h.ok(t, "list_departures", map[string]any{"limit": 1}); len(got["departures"].([]any)) != 1 {
		t.Fatalf("limit = %v, want 1 row", got)
	}

	// an origin that matches nothing
	if got := h.ok(t, "list_departures", map[string]any{"origin": "n-z"}); len(got["departures"].([]any)) != 0 {
		t.Fatalf("origin filter = %v, want none", got)
	}
	if got := h.ok(t, "list_departures", map[string]any{"destination": "n-b", "origin": "n-a"}); len(got["departures"].([]any)) != 2 {
		t.Fatalf("origin+destination filter = %v, want both", got)
	}
}

func TestListDeparturesErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "list_departures", map[string]any{"limit": 1001}, "validation-failed")
	failWith(t, h.session, "list_departures", map[string]any{"limit": -1}, "validation-failed")
	failWith(t, h.session, "list_departures", map[string]any{"from": "soon"}, "validation-failed")
	failWith(t, h.session, "list_departures", map[string]any{"to": "2026-13-01T00:00:00Z"}, "validation-failed")
	failWith(t, h.session, "list_departures",
		map[string]any{"from": "2026-10-24T14:00:00Z", "to": "2026-10-24T13:00:00Z"}, "validation-failed")
}

// Reads never reach a write seam: the outbox log of the seeded store stays
// empty however the tools are called.
func TestReadsEmitNothing(t *testing.T) {
	h := newHarness(t)
	h.seedDeparture(t, "dep-1", time.Hour, 120)
	before := h.store.Counts()
	h.ok(t, "list_departures", map[string]any{})
	h.ok(t, "get_departure", map[string]any{"departureId": "dep-1"})
	if after := h.store.Counts(); after != before {
		t.Fatalf("store counts %v -> %v: a read tool changed something", before, after)
	}
}
