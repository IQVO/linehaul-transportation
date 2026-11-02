//go:build integration

package http_test

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	inbound "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http"
	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/wire"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/clock"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/ids"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

// These tests run the REAL Idempotency-Key middleware and the REAL use cases
// over a real Postgres (testcontainers) behind the production router.

type dbAPI struct {
	t    *testing.T
	pool *pgtest.Pool
	srv  *httptest.Server
}

type reply struct {
	status int
	header nethttp.Header
	raw    string
}

func (r reply) slug() string {
	var p struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(r.raw), &p)
	return p.Type[strings.LastIndex(p.Type, "/")+1:]
}

func newDBAPI(t *testing.T) dbAPI {
	t.Helper()
	pool := pgtest.NewPool(t)
	svc := usecases.New(usecases.Deps{
		UoW: postgres.NewUnitOfWork(pool), Departures: postgres.NewDepartureRepo(pool), Schedules: postgres.NewScheduleRepo(pool),
		Loads: postgres.NewLoadRepo(pool), Nodes: postgres.NewNodeViewRepo(pool), Lanes: postgres.NewLaneViewRepo(pool),
		Volumes: postgres.NewVolumeRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), wire.Encoder{NewID: uuid.NewString}),
		Clock:  clock.System{}, IDs: ids.UUID{},
	})
	srv := httptest.NewServer(inbound.NewRouter(&inbound.Server{Service: svc, Idempotency: inbound.RequireIdempotencyKey(pool, pgtx.With)}, nil, ""))
	t.Cleanup(srv.Close)
	return dbAPI{t: t, pool: pool, srv: srv}
}

func (d dbAPI) do(method, path, body, key string) reply {
	d.t.Helper()
	req, err := nethttp.NewRequest(method, d.srv.URL+path, strings.NewReader(body))
	if err != nil {
		d.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
}

func (d dbAPI) count(query string) int {
	d.t.Helper()
	var n int
	if err := d.pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		d.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// seed stores a SCHEDULED line-haul departure that cuts off in an hour.
func (d dbAPI) seed(id string) {
	d.t.Helper()
	now := time.Now().UTC()
	dep, _, err := departure.Schedule(departure.Spec{
		ID: departure.ID(id), LaneID: "lane-1", Kind: departure.KindLinehaul, OriginNodeID: "n-a", DestinationNodeID: "n-b",
		OriginType: network.SortationCenter, DestinationType: network.SortationCenter,
		CutoffAt: now.Add(time.Hour), DepartsAt: now.Add(time.Hour), ArrivesAt: now.Add(2 * time.Hour), CapacityPackages: 5,
	}, true, now)
	if err != nil {
		d.t.Fatal(err)
	}
	if err := postgres.NewDepartureRepo(d.pool).Save(context.Background(), dep); err != nil {
		d.t.Fatal(err)
	}
}

func loadBody(departureID string) string { return `{"departureId":"` + departureID + `"}` }

func TestTheSameKeyAndBodyReplaysTheFirstResponse(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	first := d.do("POST", "/loads", loadBody("dep-1"), "key-1")
	second := d.do("POST", "/loads", loadBody("dep-1"), "key-1")

	if first.status != nethttp.StatusCreated || second.status != first.status || second.raw != first.raw {
		t.Fatalf("first %d %s, replay %d %s: want the identical response", first.status, first.raw, second.status, second.raw)
	}
	if first.header.Get(inbound.IdempotencyReplayedHeader) != "" || second.header.Get(inbound.IdempotencyReplayedHeader) != "true" {
		t.Fatalf("replayed header: first %q second %q", first.header.Get(inbound.IdempotencyReplayedHeader), second.header.Get(inbound.IdempotencyReplayedHeader))
	}
	if second.header.Get("Content-Type") != "application/json" {
		t.Fatalf("replay content type = %q", second.header.Get("Content-Type"))
	}
	if n, k := d.count(`SELECT count(*) FROM loads`), d.count(`SELECT count(*) FROM idempotency_keys`); n != 1 || k != 1 {
		t.Fatalf("rows: loads %d keys %d, want 1 each", n, k)
	}
}

func TestTheSameKeyWithAnotherBodyIs409KeyReused(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	d.seed("dep-2")
	d.do("POST", "/loads", loadBody("dep-1"), "key-1")
	r := d.do("POST", "/loads", loadBody("dep-2"), "key-1")
	if r.status != nethttp.StatusConflict || r.slug() != "idempotency-key-reused" {
		t.Fatalf("conflicting body = %d %s, want 409 idempotency-key-reused", r.status, r.raw)
	}
	if n := d.count(`SELECT count(*) FROM loads`); n != 1 {
		t.Fatalf("the conflicting request opened a load: %d loads", n)
	}
}

func TestTheSameKeyOnAnotherResourceIsKeyReusedGlobally(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	d.seed("dep-2")
	if first := d.do("POST", "/departures/dep-1/cancellations", `{"reason":"weather"}`, "same-key"); first.status != nethttp.StatusOK {
		t.Fatalf("cancel = %d %s", first.status, first.raw)
	}
	other := d.do("POST", "/departures/dep-2/cancellations", `{"reason":"weather"}`, "same-key")
	if other.status != nethttp.StatusConflict || other.slug() != "idempotency-key-reused" {
		t.Fatalf("same key, other departure = %d %s, want 409 idempotency-key-reused", other.status, other.raw)
	}
	if n := d.count(`SELECT count(*) FROM departures WHERE state = 'CANCELLED'`); n != 1 {
		t.Fatalf("%d departures cancelled, want only the first", n)
	}
}

func TestAFourXXIsStoredAndItsWritesAreRolledBack(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	d.do("POST", "/departures/dep-1/cancellations", `{"reason":"weather"}`, "cancel-1")
	// A load on a CANCELLED departure is refused (409); the refusal is stored.
	first := d.do("POST", "/loads", loadBody("dep-1"), "key-409")
	second := d.do("POST", "/loads", loadBody("dep-1"), "key-409")
	if first.status != nethttp.StatusConflict || first.slug() != "illegal-transition" || second.status != first.status || second.raw != first.raw ||
		second.header.Get(inbound.IdempotencyReplayedHeader) != "true" {
		t.Fatalf("first %d %s, replay %d %s: want the stored 409", first.status, first.raw, second.status, second.raw)
	}
	if first.header.Get("Content-Type") != "application/problem+json" || second.header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("problem content types: %q %q", first.header.Get("Content-Type"), second.header.Get("Content-Type"))
	}
	if n := d.count(`SELECT count(*) FROM loads`); n != 0 {
		t.Fatalf("a refused request left %d loads", n)
	}
}

func TestAMissingOrInvalidKeyIs400(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	if r := d.do("POST", "/loads", loadBody("dep-1"), ""); r.status != 400 || r.slug() != "idempotency-key-required" {
		t.Fatalf("no key = %d %s", r.status, r.raw)
	}
	if r := d.do("POST", "/loads", loadBody("dep-1"), strings.Repeat("k", 129)); r.status != 400 || r.slug() != "idempotency-key-invalid" {
		t.Fatalf("129-char key = %d %s", r.status, r.raw)
	}
	if n := d.count(`SELECT count(*) FROM idempotency_keys`); n != 0 {
		t.Fatalf("refused keys were recorded: %d rows", n)
	}
	if r := d.do("POST", "/loads", loadBody("dep-1"), strings.Repeat("k", 128)); r.status != 201 {
		t.Fatalf("128-char key = %d %s, want it accepted", r.status, r.raw)
	}
}

func TestABodyAboveTheLimitIs413AndRecordsNothing(t *testing.T) {
	d := newDBAPI(t)
	r := d.do("POST", "/loads", `{"departureId":"`+strings.Repeat("x", 70<<10)+`"}`, "big-1")
	if r.status != nethttp.StatusRequestEntityTooLarge || r.slug() != "request-body-too-large" {
		t.Fatalf("oversized body = %d %.200s, want 413 request-body-too-large", r.status, r.raw)
	}
	if n := d.count(`SELECT count(*) FROM idempotency_keys`); n != 0 {
		t.Fatalf("an oversized request recorded %d keys", n)
	}
}

func TestKeysExpireAfter24HoursAndAreSweptOnTheNextKeyedWrite(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	d.do("POST", "/loads", loadBody("dep-1"), "old-key")
	if _, err := d.pool.Exec(context.Background(), `UPDATE idempotency_keys SET created_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	// The expired key is gone on the next keyed write, so the same key is new:
	// it runs again (and is refused by the domain: the departure has a load).
	r := d.do("POST", "/loads", loadBody("dep-1"), "old-key")
	if r.status != nethttp.StatusConflict || r.slug() != "load-already-exists" {
		t.Fatalf("an expired key = %d %s, want it to run again (409 load-already-exists)", r.status, r.raw)
	}
	if n := d.count(`SELECT count(*) FROM idempotency_keys WHERE created_at < now() - interval '24 hours'`); n != 0 {
		t.Fatalf("%d expired keys survived the sweep", n)
	}
}

func TestConcurrentRequestsWithTheSameKeyHaveOneEffect(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	const n = 8
	statuses := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			req, _ := nethttp.NewRequest("POST", d.srv.URL+"/loads", strings.NewReader(loadBody("dep-1")))
			req.Header.Set("Idempotency-Key", "race-1")
			resp, err := nethttp.DefaultClient.Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			_ = resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	for i := 0; i < n; i++ {
		if st := <-statuses; st != nethttp.StatusCreated {
			t.Fatalf("concurrent same-key request answered %d, want the first answer (201) replayed", st)
		}
	}
	if loads, keys := d.count(`SELECT count(*) FROM loads`), d.count(`SELECT count(*) FROM idempotency_keys`); loads != 1 || keys != 1 {
		t.Fatalf("rows: loads %d keys %d, want 1 each", loads, keys)
	}
}

func TestAPutIsNotKeyedAndIgnoresAKey(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	for i := 0; i < 2; i++ {
		if r := d.do("PUT", "/departures/dep-1/capacity", `{"capacityPackages":9}`, ""); r.status != nethttp.StatusOK || r.header.Get(inbound.IdempotencyReplayedHeader) != "" {
			t.Fatalf("PUT without a key = %d %s", r.status, r.raw)
		}
	}
	if r := d.do("PUT", "/departures/dep-1/capacity", `{"capacityPackages":9}`, "ignored"); r.status != nethttp.StatusOK {
		t.Fatalf("PUT with a key = %d %s", r.status, r.raw)
	}
	if n := d.count(`SELECT count(*) FROM idempotency_keys`); n != 0 {
		t.Fatalf("a PUT stored %d key rows", n)
	}
}

func TestListDeparturesPagesOverPostgresWithTiesOnTheCutoff(t *testing.T) {
	d := newDBAPI(t)
	cutoff := time.Now().UTC().Add(time.Hour)
	for i := 0; i < 7; i++ {
		id := "dep-" + string(rune('a'+i))
		at := cutoff.Add(time.Duration(i/3) * time.Minute) // 3 + 3 + 1 rows share a cutoff
		dep, _, err := departure.Schedule(departure.Spec{
			ID: departure.ID(id), LaneID: "lane-1", Kind: departure.KindLinehaul, OriginNodeID: "n-a", DestinationNodeID: "n-b",
			OriginType: network.SortationCenter, DestinationType: network.SortationCenter,
			CutoffAt: at, DepartsAt: at, ArrivesAt: at.Add(time.Hour), CapacityPackages: 5,
		}, true, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := postgres.NewDepartureRepo(d.pool).Save(context.Background(), dep); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	var order []string
	path := "/departures?limit=3"
	for pages := 0; path != ""; pages++ {
		if pages > 4 {
			t.Fatal("paging does not terminate")
		}
		r := d.do("GET", path, "", "")
		var body struct {
			Items []struct {
				DepartureID string `json:"departureId"`
			} `json:"items"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal([]byte(r.raw), &body); err != nil || r.status != 200 {
			t.Fatalf("page = %d %s", r.status, r.raw)
		}
		for _, it := range body.Items {
			if seen[it.DepartureID] {
				t.Fatalf("%s returned twice", it.DepartureID)
			}
			seen[it.DepartureID] = true
			order = append(order, it.DepartureID)
		}
		path = ""
		if body.NextCursor != "" {
			path = "/departures?limit=3&cursor=" + body.NextCursor
		}
	}
	if strings.Join(order, " ") != "dep-a dep-b dep-c dep-d dep-e dep-f dep-g" {
		t.Fatalf("walked %v", order)
	}
}
