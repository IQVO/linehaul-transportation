package features_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// bddNow is Friday 2026-10-23 12:00 UTC, 09:00 at the nodes (Sao Paulo, UTC-3).
var bddNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

const nodeZone = "America/Sao_Paulo"

// world is the state of one scenario: the in-memory store behind the use
// cases, the aliases the scenario gave to departures and loads, and the
// outcome of the last command.
type world struct {
	store *memory.Store
	clock *memory.Clock
	svc   *usecases.Service

	lanes      map[string]network.LaneView
	departures map[string]string // alias -> departure id
	loads      map[string]string // alias -> load id
	eventSeq   int

	lastErr      error
	errInspected bool
	outboxBefore int
	scheduled    int
}

func newWorld() *world {
	w := &world{}
	w.reset()
	return w
}

func (w *world) reset() {
	s := memory.NewStore()
	w.store, w.clock = s, memory.NewClock(bddNow)
	w.svc = usecases.New(usecases.Deps{
		UoW: s.UnitOfWork(), Departures: s.Departures(), Schedules: s.Schedules(), Loads: s.Loads(), Nodes: s.Nodes(), Lanes: s.Lanes(),
		Volumes: s.Volumes(), Inbox: s.Inbox(), Outbox: s.Outbox(), Clock: w.clock, IDs: &memory.SequentialIDs{},
	})
	w.lanes, w.departures, w.loads = map[string]network.LaneView{}, map[string]string{}, map[string]string{}
	w.eventSeq, w.lastErr, w.errInspected, w.outboxBefore, w.scheduled = 0, nil, true, 0, 0
}

func (w *world) ctx() context.Context { return context.Background() }

// eventID mints the CloudEvents id of a consumed event.
func (w *world) eventID() string {
	w.eventSeq++
	return fmt.Sprintf("ev-%d", w.eventSeq)
}

// hooks resets the world per scenario and refuses to let a command that failed
// unexpectedly slip through: its error must be inspected by an outcome step
// before the next step runs.
func (w *world) hooks(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.reset()
		return ctx, nil
	})
	sc.StepContext().Before(func(ctx context.Context, st *godog.Step) (context.Context, error) {
		if w.lastErr != nil && !w.errInspected && !strings.HasPrefix(st.Text, "the command") {
			err := w.lastErr
			w.lastErr, w.errInspected = nil, true
			return ctx, fmt.Errorf("the previous command failed and no step expected it: %w", err)
		}
		return ctx, nil
	})
}

// run executes one command and remembers its error and where the outbox
// stood, so the Then steps can ask what the command published.
func (w *world) run(command func() error) {
	w.outboxBefore = len(w.store.Outbox().Events())
	w.scheduled = 0
	w.lastErr = command()
	w.errInspected = w.lastErr == nil
}

// published returns the events the last command appended.
func (w *world) published() []shared.Event { return w.store.Outbox().Events()[w.outboxBefore:] }

func (w *world) publishedNames() []string {
	var out []string
	for _, e := range w.published() {
		out = append(out, e.EventName())
	}
	return out
}

func (w *world) depID(alias string) string {
	if id, ok := w.departures[alias]; ok {
		return id
	}
	return alias
}

func (w *world) loadID(alias string) string {
	if id, ok := w.loads[alias]; ok {
		return id
	}
	return alias
}

// departuresOf lists the departures of an origin, earliest cutoff first.
func (w *world) departuresOf(origin string, f func(views.Departure) bool) ([]views.Departure, error) {
	list, err := w.svc.ListDepartures(w.ctx(), usecases.ListDeparturesQuery{Origin: origin, Limit: 100})
	if err != nil {
		return nil, err
	}
	var out []views.Departure
	for _, d := range list.Departures {
		if f(d) {
			out = append(out, d)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CutoffAt.Before(out[j-1].CutoffAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func isKind(k departure.Kind) func(views.Departure) bool {
	return func(d views.Departure) bool { return d.Kind == string(k) }
}

// slugs maps the problem slugs of the REST contract to the typed errors the
// use cases return.
var slugs = map[string]error{
	"not-found":               ports.ErrNotFound,
	"validation-failed":       shared.ErrValidation,
	"illegal-transition":      shared.ErrIllegalTransition,
	"invalid-departure-times": departure.ErrInvalidDepartureTimes,
	"overlapping-slots":       schedule.ErrOverlappingSlots,
	"departure-full":          departure.ErrDepartureFull,
	"departure-closed":        departure.ErrDepartureClosed,
	"lane-closed":             departure.ErrLaneClosed,
	"package-already-loaded":  load.ErrPackageAlreadyLoaded,
	"load-over-capacity":      load.ErrLoadOverCapacity,
	"load-already-exists":     load.ErrLoadAlreadyExists,
}
