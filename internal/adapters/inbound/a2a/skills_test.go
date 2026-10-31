package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// seededStore holds dep-1 (SCHEDULED, n-a -> n-b), dep-2 (SCHEDULED, n-a -> n-c)
// and dep-3 (CANCELLED), all scheduled through the real aggregate.
func seededStore(t *testing.T) *memory.Store {
	t.Helper()
	store := memory.NewStore()
	seed(t, store, "dep-1", "n-b", time.Hour, 120)
	seed(t, store, "dep-2", "n-c", 2*time.Hour, 10)
	d := seed(t, store, "dep-3", "n-b", 3*time.Hour, 5)
	if _, err := d.Cancel("road closed", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Departures().Save(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return store
}

func seed(t *testing.T, store *memory.Store, id, destination string, in time.Duration, capacity int) *departure.Departure {
	t.Helper()
	d, _, err := departure.Schedule(departure.Spec{
		ID: departure.ID(id), LaneID: "lane-1", Kind: departure.KindLinehaul, OriginNodeID: "n-a", DestinationNodeID: destination,
		OriginType: network.SortationCenter, DestinationType: network.SortationCenter,
		CutoffAt: now.Add(in), DepartsAt: now.Add(in), ArrivesAt: now.Add(in + time.Hour), CapacityPackages: capacity,
	}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Departures().Save(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

func depsOver(store *memory.Store) Deps {
	return Deps{Departures: usecases.New(usecases.Deps{Departures: store.Departures()})}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		Departure  *wireDeparture `json:"departure"`
		Departures *struct {
			Departures []wireDeparture `json:"departures"`
		} `json:"departures"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wireDeparture struct {
	DepartureID       string `json:"departureId"`
	LaneID            string `json:"laneId"`
	Kind              string `json:"kind"`
	OriginNodeID      string `json:"originNodeId"`
	DestinationNodeID string `json:"destinationNodeId"`
	CutoffAt          string `json:"cutoffAt"`
	DepartsAt         string `json:"departsAt"`
	ArrivesAt         string `json:"arrivesAt"`
	CapacityPackages  int    `json:"capacityPackages"`
	BookedPackages    int    `json:"bookedPackages"`
	RemainingPackages int    `json:"remainingPackages"`
	State             string `json:"state"`
	CancelReason      string `json:"cancelReason"`
	CancelledAt       string `json:"cancelledAt"`
	DepartedAt        string `json:"departedAt"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// call sends one message part and returns the decoded envelope and the
// one-line text summary of the agent's reply.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != problemTypeBase+slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func listedIDs(env wireEnvelope) []string {
	if env.Data.Departures == nil {
		return nil
	}
	ids := []string{}
	for _, d := range env.Data.Departures.Departures {
		ids = append(ids, d.DepartureID)
	}
	return ids
}

func TestWireGetDepartureReturnsCapacityAndState(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	env, text := w.skill(t, skillGetDeparture, map[string]any{"departureId": "dep-1"})
	d := env.Data.Departure
	if env.Problem != nil || d == nil {
		t.Fatalf("envelope = %+v", env)
	}
	want := wireDeparture{
		DepartureID: "dep-1", LaneID: "lane-1", Kind: "LINEHAUL", OriginNodeID: "n-a", DestinationNodeID: "n-b",
		CutoffAt: "2026-10-24T13:00:00Z", DepartsAt: "2026-10-24T13:00:00Z", ArrivesAt: "2026-10-24T14:00:00Z",
		CapacityPackages: 120, BookedPackages: 0, RemainingPackages: 120, State: "SCHEDULED",
	}
	if *d != want {
		t.Fatalf("departure = %+v, want %+v", *d, want)
	}
	if text != "dep-1: SCHEDULED LINEHAUL n-a -> n-b, cutoff 2026-10-24T13:00:00Z, 0 of 120 packages booked (120 remaining)" {
		t.Fatalf("summary = %q", text)
	}
}

func TestWireGetDepartureCarriesTheCancelTrail(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	env, _ := w.skill(t, skillGetDeparture, map[string]any{"departureId": "dep-3"})
	d := env.Data.Departure
	if env.Problem != nil || d == nil || d.State != "CANCELLED" || d.CancelReason != "road closed" || d.CancelledAt != "2026-10-24T12:01:00Z" {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestWireListDeparturesFiltersAndWindows(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))
	cases := map[string]struct {
		args map[string]any
		want []string
		text string
	}{
		"all, by cutoff":      {map[string]any{}, []string{"dep-1", "dep-2", "dep-3"}, "3 departures: dep-1, dep-2, dep-3"},
		"destination":         {map[string]any{"destination": "n-b"}, []string{"dep-1", "dep-3"}, ""},
		"origin and dest":     {map[string]any{"origin": "n-a", "destination": "n-c"}, []string{"dep-2"}, ""},
		"origin matches none": {map[string]any{"origin": "n-z"}, []string{}, "0 departures"},
		"window [from, to)":   {map[string]any{"from": "2026-10-24T13:00:00Z", "to": "2026-10-24T14:00:00Z"}, []string{"dep-1"}, ""},
		"limit":               {map[string]any{"limit": 2}, []string{"dep-1", "dep-2"}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, skillListDepartures, tc.args)
			if env.Problem != nil || env.Data.Departures == nil || strings.Join(listedIDs(env), ",") != strings.Join(tc.want, ",") {
				t.Fatalf("envelope = %+v, want ids %v", env, tc.want)
			}
			if tc.text != "" && text != tc.text {
				t.Fatalf("summary = %q, want %q", text, tc.text)
			}
		})
	}
}

func TestSummarizeDeparturesCapsTheListedIDs(t *testing.T) {
	list := departuresDTO{}
	for i := range maxListedIDs + 3 {
		list.Departures = append(list.Departures, departureDTO{DepartureID: "d" + string(rune('a'+i))})
	}
	got := summarizeDepartures(list)
	if !strings.HasPrefix(got, "13 departures: da, db") || !strings.HasSuffix(got, " and 3 more") || strings.Contains(got, "dk") {
		t.Fatalf("summary = %q", got)
	}
}

func TestWireEmptyHorizonIsAnEmptyListNotAProblem(t *testing.T) {
	w := newWire(t, depsOver(memory.NewStore()))

	env, _ := w.skill(t, skillListDepartures, map[string]any{})
	if env.Problem != nil || env.Data.Departures == nil || env.Data.Departures.Departures == nil || len(env.Data.Departures.Departures) != 0 {
		t.Fatalf("envelope = %+v, want an empty departures array", env)
	}
}

// An unknown departure is a 404 answered in-band with the same slug and
// status the REST adapter gives GET /departures/{departureId}.
func TestWireUnknownDepartureIsAnsweredAs404NotRaised(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	env, text := w.skill(t, skillGetDeparture, map[string]any{"departureId": "dep-ghost"})
	wantProblem(t, env, 404, "not-found")
	if env.Problem.Detail != `no departure exists with departureId "dep-ghost"` {
		t.Fatalf("detail = %q", env.Problem.Detail)
	}
	if !strings.Contains(text, "dep-ghost") {
		t.Fatalf("summary = %q", text)
	}
}

// Rejected queries are 422 validation-failed, like the REST adapter's list.
func TestWireRejectedQueriesAreAnsweredAs422(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))
	cases := map[string]map[string]any{
		"limit above the maximum": {"limit": 1001},
		"negative limit":          {"limit": -1},
		"from is not an instant":  {"from": "soon"},
		"to is not an instant":    {"to": "2026-13-01T00:00:00Z"},
		"from after to":           {"from": "2026-10-24T14:00:00Z", "to": "2026-10-24T13:00:00Z"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, skillListDepartures, args)
			wantProblem(t, env, 422, "validation-failed")
			for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
				if strings.HasPrefix(s, "validation failed: ") {
					t.Fatalf("the shared error prefix leaks into %q", s)
				}
			}
		})
	}
}

// failingReader makes every read fail, standing in for a database outage.
type failingReader struct{}

var errBoom = errors.New("pgx: connection refused to 10.0.0.7:5432 password=hunter2")

func (failingReader) ListDepartures(context.Context, usecases.ListDeparturesQuery) (views.DepartureList, error) {
	return views.DepartureList{}, errBoom
}

func (failingReader) GetDeparture(context.Context, string) (views.Departure, error) {
	return views.Departure{}, errBoom
}

// An unexpected failure is answered as a 500 problem, never a JSON-RPC error,
// and its raw text (which may carry infrastructure detail) is not echoed.
func TestWireUnexpectedFailureIs500AndDoesNotLeakTheCause(t *testing.T) {
	w := newWire(t, Deps{Departures: failingReader{}})
	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"get":  {skillGetDeparture, map[string]any{"departureId": "dep-1"}},
		"list": {skillListDepartures, map[string]any{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 500, "internal-error")
			for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
				if strings.Contains(s, "hunter2") || strings.Contains(s, "10.0.0.7") {
					t.Fatalf("a 500 answer leaks the underlying error: %q", s)
				}
			}
		})
	}
}

func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))
	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"get: missing departureId":    {skillGetDeparture, map[string]any{}},
		"get: blank departureId":      {skillGetDeparture, map[string]any{"departureId": "   "}},
		"get: departureId wrong type": {skillGetDeparture, map[string]any{"departureId": 7}},
		"get: unknown argument":       {skillGetDeparture, map[string]any{"departureId": "dep-1", "force": true}},
		"get: typo'd argument":        {skillGetDeparture, map[string]any{"departureID": "dep-1", "departureIdd": "dep-1"}},
		"list: unknown argument":      {skillListDepartures, map[string]any{"limitt": 1}},
		"list: limit a string":        {skillListDepartures, map[string]any{"limit": "5"}},
		"list: limit fractional":      {skillListDepartures, map[string]any{"limit": 1.5}},
		"list: from a number":         {skillListDepartures, map[string]any{"from": 20261024}},
		"list: wrong-skill argument":  {skillListDepartures, map[string]any{"departureId": "dep-1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 400, "invalid-argument")
		})
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	env, _ := w.call(t, a2a.NewTextPart("not json at all"))
	wantProblem(t, env, 400, "malformed-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}))
	wantProblem(t, env, 400, "malformed-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": "list_departures", "arguments": map[string]any{}, "extra": 1}))
	wantProblem(t, env, 400, "malformed-request")

	// Trailing data after the JSON document is refused too.
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"list_departures","arguments":{}} {"skill":"get_departure"}`))
	wantProblem(t, env, 400, "malformed-request")

	env, _ = w.skill(t, "launch_rocket", map[string]any{})
	wantProblem(t, env, 400, "unknown-skill")

	// A text part holding the same JSON is accepted (documented contract).
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"get_departure","arguments":{"departureId":"dep-1"}}`))
	if env.Problem != nil || env.Data.Departure == nil || env.Data.Departure.DepartureID != "dep-1" {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", env)
	}
}

// The write operations of the REST surface are not offered here: asking for
// one is an unknown skill, answered in-band, and nothing is written.
func TestWireOffersNoWriteSkillAndReadsChangeNothing(t *testing.T) {
	store := seededStore(t)
	before := store.Counts()
	w := newWire(t, depsOver(store))

	for _, skill := range []string{"cancel_departure", "change_departure_capacity", "define_lane_schedule", "open_load", "add_package", "close_load", "dispatch_load", "receive_load"} {
		env, _ := w.skill(t, skill, map[string]any{"departureId": "dep-1"})
		wantProblem(t, env, 400, "unknown-skill")
	}
	w.skill(t, skillListDepartures, map[string]any{})
	w.skill(t, skillGetDeparture, map[string]any{"departureId": "dep-1"})
	if after := store.Counts(); after != before {
		t.Fatalf("store counts %v -> %v: a skill changed something", before, after)
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(depsOver(seededStore(t)))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	w := &wire{client: client}
	env, _ := w.skill(t, skillGetDeparture, map[string]any{"departureId": "dep-1"})
	if env.Data.Departure == nil {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}
