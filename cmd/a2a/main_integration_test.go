//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

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

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then a delegating agent's
// whole flow runs over the real wiring on Postgres: the Agent Card is fetched
// from the well-known path, an SDK client is built from it, and both curated
// skills answer from state the write side persisted, with the documented
// rejections answered in-band as problems (ADR 0011). The agent is read-only,
// so the outbox must not grow: there is nothing for a relay to publish from
// this binary.
func TestA2AAgainstPostgres_AnswersFromRealStateAndNeverWrites(t *testing.T) {
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
	seedDepartures(t, ctx, pool, now)
	before := countOutbox()
	if before != 0 {
		t.Fatalf("outbox rows after seeding = %d, want 0 (seeding goes through the repository, like the write path's persistence)", before)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	srv := httptest.NewServer(newAgent(deps, "http://placeholder.invalid/", "it", quietLogger(), testServiceName))
	t.Cleanup(srv.Close)
	client := clientFromServedCard(t, srv)

	assertSeededReads(t, client)
	assertRejections(t, client)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the A2A binary wrote an event", before, after)
	}
}

// clientFromServedCard runs the discovery flow a delegating agent runs: fetch
// the card the binary serves at the well-known path, point its interface at
// the test server the way the gateway's rewritten route would, build the SDK
// client from it.
func clientFromServedCard(t *testing.T, srv *httptest.Server) *a2aclient.Client {
	t.Helper()
	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// seedDepartures persists two departures the way the write side's persistence
// does: domain aggregate in, repository out, on the same real Postgres cmd/api
// owns. (The REST write commands would additionally append outbox events; the
// read-only claim under test is about the A2A binary, so seeding targets the
// repository directly.)
func seedDepartures(t *testing.T, ctx context.Context, pool *pgtest.Pool, now time.Time) {
	t.Helper()
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

// envelopeOf sends one skill call and returns the decoded envelope (a
// test-local shape), the reply's one-line text summary and everything the
// agent put on the wire.
func envelopeOf(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) (env map[string]any, summary, raw string) {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("%s: reply = %T", skill, resp)
	}
	data, ok := reply.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("%s: first part is %T, want the Data part of the envelope", skill, reply.Parts[0].Content)
	}
	text, ok := reply.Parts[1].Content.(a2a.Text)
	if !ok {
		t.Fatalf("%s: second part is %T, want the one-line Text summary", skill, reply.Parts[1].Content)
	}
	rawJSON, _ := json.Marshal(data.Value)
	var e map[string]any
	if err := json.Unmarshal(rawJSON, &e); err != nil {
		t.Fatal(err)
	}
	return e, string(text), string(rawJSON)
}

// assertSeededReads reads back, through both curated skills, what
// seedDepartures persisted (ADR 0011: the goal-shaped twins of the MCP tools
// over the same ListDepartures/GetDeparture use cases).
func assertSeededReads(t *testing.T, client *a2aclient.Client) {
	t.Helper()

	env, summary, raw := envelopeOf(t, client, "get_departure", map[string]any{"departureId": "dep-it-1"})
	dep, _ := env["data"].(map[string]any)["departure"].(map[string]any)
	if env["problem"] != nil || dep["departureId"] != "dep-it-1" || dep["laneId"] != "lane-1" ||
		dep["kind"] != "LINEHAUL" || dep["originNodeId"] != "n-a" || dep["destinationNodeId"] != "n-b" ||
		dep["capacityPackages"] != 120.0 || dep["bookedPackages"] != 0.0 || dep["remainingPackages"] != 120.0 ||
		dep["state"] != "SCHEDULED" || dep["cutoffAt"] != "2026-10-24T13:00:00Z" ||
		dep["departsAt"] != "2026-10-24T13:00:00Z" || dep["arrivesAt"] != "2026-10-24T14:00:00Z" {
		t.Fatalf("get_departure = %s", raw)
	}
	if !strings.Contains(summary, "dep-it-1") || !strings.Contains(summary, "0 of 120 packages booked") {
		t.Fatalf("get_departure summary = %q, want the one-line form naming the departure", summary)
	}

	env, _, raw = envelopeOf(t, client, "list_departures", map[string]any{"origin": "n-a", "destination": "n-b"})
	list, _ := departuresArrayOf(env)
	if env["problem"] != nil || len(list) != 2 || list[0].(map[string]any)["departureId"] != "dep-it-1" {
		t.Fatalf("list_departures origin/destination = %s", raw)
	}

	env, _, raw = envelopeOf(t, client, "list_departures", map[string]any{"from": "2026-10-24T12:30:00Z", "to": "2026-10-24T13:30:00Z", "limit": 10})
	window, _ := departuresArrayOf(env)
	if env["problem"] != nil || len(window) != 1 || window[0].(map[string]any)["departureId"] != "dep-it-1" {
		t.Fatalf("list_departures [from,to) window = %s", raw)
	}
}

// departuresArrayOf digs the departure array out of a list_departures
// envelope: the DTO nests one level (data.departures.departures).
func departuresArrayOf(env map[string]any) ([]any, bool) {
	dto, _ := env["data"].(map[string]any)["departures"].(map[string]any)
	arr, ok := dto["departures"].([]any)
	return arr, ok
}

// assertRejections proves ADR 0011's in-band problem contract over the real
// router: domain rejections and request-shape failures are ANSWERED in the
// message envelope as RFC 7807-shaped problems under the REST adapter's type
// namespace and classification, never as JSON-RPC errors, and a write-shaped
// goal is refused because no write skill exists.
func assertRejections(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	const base = "https://retailnetwork.example/problems/"
	problemOf := func(env map[string]any) map[string]any {
		p, _ := env["problem"].(map[string]any)
		return p
	}

	env, _, raw := envelopeOf(t, client, "get_departure", map[string]any{"departureId": "dep-it-ghost"})
	if p := problemOf(env); p["status"] != 404.0 || p["type"] != base+"not-found" {
		t.Fatalf("an unknown departure must be a 404 not-found problem, got %s", raw)
	}

	env, _, raw = envelopeOf(t, client, "list_departures", map[string]any{"from": "2026-10-24T13:00:00Z", "to": "2026-10-24T12:00:00Z"})
	if p := problemOf(env); p["status"] != 422.0 || p["type"] != base+"validation-failed" {
		t.Fatalf("from after to must be a 422 validation-failed problem, got %s", raw)
	}

	env, _, raw = envelopeOf(t, client, "get_departure", map[string]any{})
	if p := problemOf(env); p["status"] != 400.0 || p["type"] != base+"invalid-argument" {
		t.Fatalf("a missing departureId must be a 400 invalid-argument problem, got %s", raw)
	}

	env, _, raw = envelopeOf(t, client, "list_departures", map[string]any{"limitt": 5})
	if p := problemOf(env); p["status"] != 400.0 || p["type"] != base+"invalid-argument" {
		t.Fatalf("an unknown argument must be refused as 400 invalid-argument (strict input), got %s", raw)
	}

	env, _, raw = envelopeOf(t, client, "cancel_departure", map[string]any{"departureId": "dep-it-1"})
	if p := problemOf(env); p["status"] != 400.0 || p["type"] != base+"unknown-skill" {
		t.Fatalf("cancellations stay a REST operation: the write-shaped goal must be a 400 unknown-skill problem, got %s", raw)
	}
}
