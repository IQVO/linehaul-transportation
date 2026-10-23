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

	inbound "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

var (
	t0    = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	t0Ctx = context.Background()
)

type fixture struct {
	t     *testing.T
	store *memory.Store
	srv   *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Departures: store.Departures(), Schedules: store.Schedules(), Loads: store.Loads(),
		Nodes: store.Nodes(), Lanes: store.Lanes(), Volumes: store.Volumes(), Inbox: store.Inbox(), Outbox: store.Outbox(),
		Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{},
	})
	srv := httptest.NewServer(inbound.NewRouter(&inbound.Server{Service: svc}))
	t.Cleanup(srv.Close)
	return &fixture{t: t, store: store, srv: srv}
}

func (f *fixture) lane(id string, open bool) {
	f.t.Helper()
	if err := f.store.Lanes().Save(t0Ctx, network.LaneView{ID: id, Origin: "n-a", Destination: "n-b", Mode: network.ModeGround, TransitMinutes: 60, Open: open}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) departure(id string, in time.Duration, capacity int) {
	f.t.Helper()
	d, _, err := departure.Schedule(departure.Spec{
		ID: departure.ID(id), LaneID: "lane-1", Kind: departure.KindLinehaul, OriginNodeID: "n-a", DestinationNodeID: "n-b",
		OriginType: network.SortationCenter, DestinationType: network.SortationCenter,
		CutoffAt: t0.Add(in), DepartsAt: t0.Add(in), ArrivesAt: t0.Add(in + time.Hour), CapacityPackages: capacity,
	}, true, t0)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.Departures().Save(t0Ctx, d); err != nil {
		f.t.Fatal(err)
	}
}

// call sends one request and returns the status, the content type and the decoded JSON body.
func (f *fixture) call(method, path, body string) (int, string, map[string]any) {
	f.t.Helper()
	req, err := nethttp.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		f.t.Fatalf("%s %s: body %q is not JSON: %v", method, path, raw, err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), out
}

func slug(body map[string]any) string {
	typ, _ := body["type"].(string)
	return typ[strings.LastIndex(typ, "/")+1:]
}

const slotJSON = `{"dayOfWeek":"FRIDAY","cutoffLocalTime":"08:00","departsLocalTime":"09:00","arrivesLocalTime":"12:00","capacityPackages":50}`

// TestEveryOperationAnswersItsContractStatus drives each operation of
// apis/openapi.yaml through the router: the success status and body shape, and
// every documented error status with its problem slug.
func TestEveryOperationAnswersItsContractStatus(t *testing.T) {
	for _, c := range operationCases() {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

type operationCase struct {
	name       string
	setup      func(f *fixture)
	method     string
	path, body string
	status     int
	slug       string // "" = success
	field      string // a key the success body must carry
}

func (c operationCase) run(t *testing.T) {
	f := newFixture(t)
	if c.setup != nil {
		c.setup(f)
	}
	status, ctype, body := f.call(c.method, c.path, c.body)
	if status != c.status {
		t.Fatalf("status = %d, want %d: %v", status, c.status, body)
	}
	if c.slug == "" {
		if ctype != "application/json" {
			t.Errorf("content type = %q", ctype)
		}
		if _, ok := body[c.field]; !ok {
			t.Errorf("body lacks %q: %v", c.field, body)
		}
		return
	}
	if ctype != "application/problem+json" || slug(body) != c.slug || int(body["status"].(float64)) != c.status {
		t.Errorf("problem = %q %v, want slug %s", ctype, body, c.slug)
	}
}

func operationCases() []operationCase {
	return []operationCase{
		{"define a schedule", func(f *fixture) { f.lane("lane-1", true) }, "POST", "/lane-schedules", `{"laneId":"lane-1","slots":[` + slotJSON + `]}`, 201, "", "laneId"},
		{"redefine a schedule", func(f *fixture) {
			f.lane("lane-1", true)
			f.call("POST", "/lane-schedules", `{"laneId":"lane-1","slots":[`+slotJSON+`]}`)
		}, "POST", "/lane-schedules", `{"laneId":"lane-1","slots":[` + slotJSON + `]}`, 200, "", "slots"},
		{"schedule of an unknown lane", nil, "POST", "/lane-schedules", `{"laneId":"lane-9","slots":[` + slotJSON + `]}`, 404, "not-found", ""},
		{"overlapping slots", func(f *fixture) { f.lane("lane-1", true) }, "POST", "/lane-schedules", `{"laneId":"lane-1","slots":[` + slotJSON + `,` + slotJSON + `]}`, 422, "overlapping-slots", ""},
		{"a bad weekday", func(f *fixture) { f.lane("lane-1", true) }, "POST", "/lane-schedules", `{"laneId":"lane-1","slots":[{"dayOfWeek":"FUNDAY","cutoffLocalTime":"08:00","departsLocalTime":"09:00","arrivesLocalTime":"12:00","capacityPackages":5}]}`, 422, "validation-failed", ""},
		{"malformed JSON", nil, "POST", "/lane-schedules", `{"laneId":`, 400, "malformed-request", ""},
		{"an unknown field", nil, "POST", "/lane-schedules", `{"laneId":"lane-1","slots":[],"extra":1}`, 400, "malformed-request", ""},
		{"trailing data", nil, "POST", "/loads", `{"departureId":"d"} {}`, 400, "malformed-request", ""},
		{"retire a schedule", func(f *fixture) {
			f.lane("lane-1", true)
			f.call("POST", "/lane-schedules", `{"laneId":"lane-1","slots":[`+slotJSON+`]}`)
		}, "POST", "/lane-schedules/lane-1:retire", "", 200, "", "state"},
		{"retire an unknown schedule", nil, "POST", "/lane-schedules/lane-9:retire", "", 404, "not-found", ""},
		{"list departures", func(f *fixture) { f.departure("dep-1", time.Hour, 5) }, "GET", "/departures?origin=n-a&destination=n-b&limit=10&from=2026-10-23T00:00:00Z&to=2026-10-24T00:00:00Z", "", 200, "", "departures"},
		{"list: a bad instant", nil, "GET", "/departures?from=yesterday", "", 422, "validation-failed", ""},
		{"list: a bad limit", nil, "GET", "/departures?limit=0", "", 422, "validation-failed", ""},
		{"list: a limit that is not a number", nil, "GET", "/departures?limit=x", "", 422, "validation-failed", ""},
		{"list: from after to", nil, "GET", "/departures?from=2026-10-25T00:00:00Z&to=2026-10-24T00:00:00Z", "", 422, "validation-failed", ""},
		{"get a departure", func(f *fixture) { f.departure("dep-1", time.Hour, 5) }, "GET", "/departures/dep-1", "", 200, "", "remainingPackages"},
		{"get an unknown departure", nil, "GET", "/departures/dep-9", "", 404, "not-found", ""},
		{"cancel", func(f *fixture) { f.departure("dep-1", time.Hour, 5) }, "POST", "/departures/dep-1:cancel", `{"reason":"planner"}`, 200, "", "cancelledAt"},
		{"cancel twice is a no-op", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/departures/dep-1:cancel", `{"reason":"planner"}`)
		}, "POST", "/departures/dep-1:cancel", `{"reason":"planner"}`, 200, "", "cancelledAt"},
		{"cancel a departed departure", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
			f.call("POST", "/loads/load-1:close", "")
			f.call("POST", "/loads/load-1:dispatch", "")
		}, "POST", "/departures/dep-1:cancel", `{"reason":"planner"}`, 409, "illegal-transition", ""},
		{"cancel with no reason", func(f *fixture) { f.departure("dep-1", time.Hour, 5) }, "POST", "/departures/dep-1:cancel", `{}`, 422, "validation-failed", ""},
		{"cancel unknown", nil, "POST", "/departures/dep-9:cancel", `{"reason":"x"}`, 404, "not-found", ""},
		{"change capacity", func(f *fixture) { f.departure("dep-1", time.Hour, 5) }, "POST", "/departures/dep-1:change-capacity", `{"capacityPackages":9}`, 200, "", "capacityPackages"},
		{"capacity below booked", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
			f.call("POST", "/loads/load-1/packages", `{"trackingNumber":"TN1"}`)
		}, "POST", "/departures/dep-1:change-capacity", `{"capacityPackages":0}`, 409, "departure-full", ""},
		{"capacity out of range", func(f *fixture) { f.departure("dep-1", time.Hour, 5) }, "POST", "/departures/dep-1:change-capacity", `{"capacityPackages":-1}`, 422, "validation-failed", ""},
		{"open a load", func(f *fixture) { f.departure("dep-1", time.Hour, 5) }, "POST", "/loads", `{"departureId":"dep-1"}`, 201, "", "loadId"},
		{"open a second load", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
		}, "POST", "/loads", `{"departureId":"dep-1"}`, 409, "load-already-exists", ""},
		{"open a load on an unknown departure", nil, "POST", "/loads", `{"departureId":"dep-9"}`, 404, "not-found", ""},
		{"add a package", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
		}, "POST", "/loads/load-1/packages", `{"trackingNumber":"TN1"}`, 200, "", "trackingNumbers"},
		{"add the same package twice is a no-op", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
			f.call("POST", "/loads/load-1/packages", `{"trackingNumber":"TN1"}`)
		}, "POST", "/loads/load-1/packages", `{"trackingNumber":"TN1"}`, 200, "", "trackingNumbers"},
		{"a package already on another load", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.departure("dep-2", 2*time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
			f.call("POST", "/loads", `{"departureId":"dep-2"}`)
			f.call("POST", "/loads/load-1/packages", `{"trackingNumber":"TN1"}`)
		}, "POST", "/loads/load-2/packages", `{"trackingNumber":"TN1"}`, 409, "package-already-loaded", ""},
		{"add a package to an unknown load", nil, "POST", "/loads/load-9/packages", `{"trackingNumber":"TN1"}`, 404, "not-found", ""},
		{"add a blank package", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
		}, "POST", "/loads/load-1/packages", `{"trackingNumber":""}`, 422, "validation-failed", ""},
		{"close a load", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
		}, "POST", "/loads/load-1:close", "", 200, "", "state"},
		{"close an unknown load", nil, "POST", "/loads/load-9:close", "", 404, "not-found", ""},
		{"dispatch an open load", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
		}, "POST", "/loads/load-1:dispatch", "", 409, "illegal-transition", ""},
		{"dispatch an unknown load", nil, "POST", "/loads/load-9:dispatch", "", 404, "not-found", ""},
		{"receive an unknown load", nil, "POST", "/loads/load-9:receive", "", 404, "not-found", ""},
		{"receive a load that did not leave", func(f *fixture) {
			f.departure("dep-1", time.Hour, 5)
			f.call("POST", "/loads", `{"departureId":"dep-1"}`)
		}, "POST", "/loads/load-1:receive", "", 409, "illegal-transition", ""},
	}
}

// TestTheWholeLoadLifecycleOverHTTP walks open, add, close, dispatch, receive.
func TestTheWholeLoadLifecycleOverHTTP(t *testing.T) {
	f := newFixture(t)
	f.departure("dep-1", -time.Minute, 2) // already past cutoff: only a dispatch is left to do
	f.departure("dep-2", time.Hour, 2)
	f.call("POST", "/loads", `{"departureId":"dep-2"}`)
	f.call("POST", "/loads/load-1/packages", `{"trackingNumber":"TN1"}`)
	if st, _, b := f.call("POST", "/loads/load-1:close", ""); st != 200 || b["state"] != "CLOSED" || b["packageCount"].(float64) != 1 {
		t.Fatalf("close: %d %v", st, b)
	}
	if st, _, b := f.call("POST", "/loads/load-1:dispatch", ""); st != 200 || b["state"] != "DEPARTED" || b["departedAt"] == nil {
		t.Fatalf("dispatch: %d %v", st, b)
	}
	if st, _, b := f.call("POST", "/loads/load-1:receive", ""); st != 200 || b["state"] != "ARRIVED" {
		t.Fatalf("receive: %d %v", st, b)
	}
	if st, _, b := f.call("GET", "/departures/dep-2", ""); st != 200 || b["state"] != "ARRIVED" {
		t.Fatalf("departure: %d %v", st, b)
	}
}

func TestHealthzAndReadyzWithoutAGate(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/healthz", "/readyz"} {
		if st, _, b := f.call("GET", p, ""); st != 200 || b["status"] == nil {
			t.Errorf("%s: %d %v", p, st, b)
		}
	}
}

func TestAnIdempotencyMiddlewareWrapsEveryPOSTAndNoGET(t *testing.T) {
	store := memory.NewStore()
	svc := usecases.New(usecases.Deps{UoW: store.UnitOfWork(), Departures: store.Departures(), Schedules: store.Schedules(), Loads: store.Loads(),
		Nodes: store.Nodes(), Lanes: store.Lanes(), Volumes: store.Volumes(), Inbox: store.Inbox(), Outbox: store.Outbox(),
		Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{}})
	var seen []string
	mw := func(next nethttp.Handler) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			seen = append(seen, r.Method+" "+r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
	srv := httptest.NewServer(inbound.NewRouter(&inbound.Server{Service: svc, Idempotency: mw}))
	defer srv.Close()
	for _, c := range [][2]string{{"POST", "/lane-schedules"}, {"POST", "/lane-schedules/l:retire"}, {"POST", "/departures/d:cancel"},
		{"POST", "/departures/d:change-capacity"}, {"POST", "/loads"}, {"POST", "/loads/l/packages"}, {"POST", "/loads/l:close"},
		{"POST", "/loads/l:dispatch"}, {"POST", "/loads/l:receive"}, {"GET", "/departures"}, {"GET", "/departures/d"}, {"GET", "/healthz"}} {
		req, _ := nethttp.NewRequest(c[0], srv.URL+c[1], strings.NewReader("{}"))
		resp, err := nethttp.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if len(seen) != 9 {
		t.Fatalf("the middleware saw %d requests, want the 9 POSTs: %v", len(seen), seen)
	}
}
