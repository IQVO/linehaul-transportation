//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/mcp"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases
// are already migrated, a second run must be a no-op), then reads through the
// MCP tools exactly what the write side persisted, and its reads add no
// outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheApiWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool := pgtest.OpenPool(t, url)
	countOutbox := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return n
	}

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	seedThroughWriteSide(t, ctx, url, now)
	before := countOutbox()
	if before != 0 {
		t.Fatalf("outbox rows after seeding = %d, want 0 (seeding goes through the repository, like the write path's persistence)", before)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertSeededReads(t, ctx, session, now)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
	}
}

// seedThroughWriteSide persists two departures the way the write side's
// persistence does: domain aggregate in, repository out, on the same real
// Postgres cmd/api owns. (The REST write commands would additionally append
// outbox events; the read-only claim under test is about the MCP binary, so
// seeding targets the repository directly.)
func seedThroughWriteSide(t *testing.T, ctx context.Context, url string, now time.Time) {
	t.Helper()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := postgres.NewDepartureRepo(pool)
	for _, spec := range []struct {
		id string
		in time.Duration
	}{
		{"dep-it-1", time.Hour},
		{"dep-it-2", 2 * time.Hour},
	} {
		d, _, err := departure.Schedule(departure.Spec{
			ID: departure.ID(spec.id), LaneID: "lane-1", Kind: departure.KindLinehaul, OriginNodeID: "n-a", DestinationNodeID: "n-b",
			OriginType: network.SortationCenter, DestinationType: network.SortationCenter,
			CutoffAt: now.Add(spec.in), DepartsAt: now.Add(spec.in), ArrivesAt: now.Add(spec.in + time.Hour), CapacityPackages: 120,
		}, true, now)
		if err != nil {
			t.Fatalf("schedule %s: %v", spec.id, err)
		}
		if err := repo.Save(ctx, d); err != nil {
			t.Fatalf("save %s: %v", spec.id, err)
		}
	}
}

// assertSeededReads reads back, through every MCP tool, what
// seedThroughWriteSide persisted.
func assertSeededReads(t *testing.T, ctx context.Context, session *sdk.ClientSession, now time.Time) {
	t.Helper()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v res=%+v", name, err, res)
		}
		return res.StructuredContent.(map[string]any)
	}

	dep := call("get_departure", map[string]any{"departureId": "dep-it-1"})
	if dep["departureId"] != "dep-it-1" || dep["kind"] != "LINEHAUL" || dep["originNodeId"] != "n-a" ||
		dep["capacityPackages"] != 120.0 || dep["state"] != "SCHEDULED" || dep["cutoffAt"] != "2026-10-24T13:00:00Z" {
		t.Fatalf("get_departure = %v", dep)
	}

	page := call("list_departures", map[string]any{"origin": "n-a", "destination": "n-b"})
	items := page["departures"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["departureId"] != "dep-it-1" {
		t.Fatalf("list_departures = %v", page)
	}

	window := call("list_departures", map[string]any{"from": "2026-10-24T12:30:00Z", "to": "2026-10-24T13:30:00Z", "limit": 10})
	if got := len(window["departures"].([]any)); got != 1 {
		t.Fatalf("from/to window = %d departures, want 1: %v", got, window)
	}
}
