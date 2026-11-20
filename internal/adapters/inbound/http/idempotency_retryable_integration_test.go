//go:build integration

package http_test

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	inbound "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http"
	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

// A refusal that depends on state another request can change (ADR 0015) is
// RETRYABLE: nothing is stored under the key, the key is released, and the same
// request under the same key runs again. These tests run the production router,
// the real use cases and the real middleware over Postgres.

// keyRows counts the rows idempotency_keys holds for key.
func (d dbAPI) keyRows(key string) int {
	d.t.Helper()
	var n int
	if err := d.pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n); err != nil {
		d.t.Fatalf("count key %s: %v", key, err)
	}
	return n
}

func wasReplayed(r reply) bool { return r.header.Get(inbound.IdempotencyReplayedHeader) == "true" }

// field reads one top-level string of a JSON reply.
func (r reply) field(name string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(r.raw), &m)
	s, _ := m[name].(string)
	return s
}

// losesTheLock wraps the departure repository: its next failSaves Save calls
// lose the optimistic lock, as when another request moved the same departure first.
type losesTheLock struct {
	ports.DepartureRepository
	failSaves atomic.Int32
}

func (l *losesTheLock) Save(ctx context.Context, d *departure.Departure) error {
	if l.failSaves.Add(-1) >= 0 {
		return ports.ErrConcurrentUpdate
	}
	return l.DepartureRepository.Save(ctx, d)
}

// TestAConcurrentUpdateLeavesNoKeyRowSoTheRetryExecutes: the loser of an
// optimistic lock answers 409 concurrent-update and is NOT stored; the same
// request under the same key then executes, succeeds and is stored and replayed.
func TestAConcurrentUpdateLeavesNoKeyRowSoTheRetryExecutes(t *testing.T) {
	lock := &losesTheLock{}
	d := newDBAPIWith(t, func(deps *usecases.Deps) {
		lock.DepartureRepository = deps.Departures
		deps.Departures = lock
	})
	d.seed("dep-1")
	outboxBefore := d.count(`SELECT count(*) FROM outbox_events`)

	lock.failSaves.Store(1)
	loser := d.do("POST", "/departures/dep-1/cancellations", `{"reason":"weather"}`, "key-cu")
	if loser.status != nethttp.StatusConflict || loser.slug() != "concurrent-update" {
		t.Fatalf("loser = %d %s, want 409 concurrent-update", loser.status, loser.raw)
	}
	if n := d.keyRows("key-cu"); n != 0 {
		t.Fatalf("key rows after the lost lock = %d, want 0: a retryable refusal must release the key", n)
	}
	if wasReplayed(loser) {
		t.Fatal("the first answer carries Idempotency-Replayed")
	}

	retry := d.do("POST", "/departures/dep-1/cancellations", `{"reason":"weather"}`, "key-cu")
	if retry.status != nethttp.StatusOK || wasReplayed(retry) || retry.field("state") != "CANCELLED" {
		t.Fatalf("retry = %d %s replayed=%v, want a fresh 200 CANCELLED: the key is free, the request runs again", retry.status, retry.raw, wasReplayed(retry))
	}
	if n := d.keyRows("key-cu"); n != 1 {
		t.Fatalf("key rows after the success = %d, want 1: a 2xx is stored", n)
	}
	again := d.do("POST", "/departures/dep-1/cancellations", `{"reason":"weather"}`, "key-cu")
	if again.status != nethttp.StatusOK || !wasReplayed(again) || again.raw != retry.raw {
		t.Fatalf("third = %d %s replayed=%v, want the stored 200 replayed", again.status, again.raw, wasReplayed(again))
	}
	if got := d.count(`SELECT count(*) FROM outbox_events`); got != outboxBefore+1 {
		t.Fatalf("outbox rows = %d, want %d: the cancellation is published once", got, outboxBefore+1)
	}
}

// TestANotFoundIsReleasedAndTheSameKeySucceedsOnceTheDepartureExists: a
// departure the request names may be created by another process (the horizon
// roll, a consumed event) after the refusal, so not-found is not stored.
func TestANotFoundIsReleasedAndTheSameKeySucceedsOnceTheDepartureExists(t *testing.T) {
	d := newDBAPI(t)
	refused := d.do("POST", "/loads", loadBody("dep-late"), "key-nf")
	if refused.status != nethttp.StatusNotFound || refused.slug() != "not-found" {
		t.Fatalf("first = %d %s, want 404 not-found", refused.status, refused.raw)
	}
	if n := d.keyRows("key-nf"); n != 0 {
		t.Fatalf("key rows after not-found = %d, want 0: the key must be released", n)
	}

	d.seed("dep-late")
	ok := d.do("POST", "/loads", loadBody("dep-late"), "key-nf")
	if ok.status != nethttp.StatusCreated || wasReplayed(ok) {
		t.Fatalf("same key after the departure exists = %d %s replayed=%v, want a fresh 201", ok.status, ok.raw, wasReplayed(ok))
	}
	again := d.do("POST", "/loads", loadBody("dep-late"), "key-nf")
	if again.status != nethttp.StatusCreated || !wasReplayed(again) || again.raw != ok.raw {
		t.Fatalf("third = %d %s replayed=%v, want the stored 201 replayed", again.status, again.raw, wasReplayed(again))
	}
	if n := d.count(`SELECT count(*) FROM loads`); n != 1 {
		t.Fatalf("loads = %d, want 1: the load is opened once", n)
	}
}

// TestPackageAlreadyLoadedIsReleasedWhenTheOtherLoadDeparts: the unique index
// only covers OPEN and CLOSED loads (load_packages.active), so the refusal
// ends when the other load is dispatched; the same key then adds the package.
func TestPackageAlreadyLoadedIsReleasedWhenTheOtherLoadDeparts(t *testing.T) {
	d := newDBAPI(t)
	d.seed("dep-1")
	d.seed("dep-2")
	first := d.do("POST", "/loads", loadBody("dep-1"), "open-1")
	second := d.do("POST", "/loads", loadBody("dep-2"), "open-2")
	if first.status != nethttp.StatusCreated || second.status != nethttp.StatusCreated {
		t.Fatalf("open loads = %d %s / %d %s", first.status, first.raw, second.status, second.raw)
	}
	one, two := first.field("loadId"), second.field("loadId")
	if r := d.do("POST", "/loads/"+one+"/packages", `{"trackingNumber":"TN-1"}`, "add-1"); r.status != nethttp.StatusOK {
		t.Fatalf("add to the first load = %d %s", r.status, r.raw)
	}

	refused := d.do("POST", "/loads/"+two+"/packages", `{"trackingNumber":"TN-1"}`, "key-pal")
	if refused.status != nethttp.StatusConflict || refused.slug() != "package-already-loaded" {
		t.Fatalf("first = %d %s, want 409 package-already-loaded", refused.status, refused.raw)
	}
	if n := d.keyRows("key-pal"); n != 0 {
		t.Fatalf("key rows after package-already-loaded = %d, want 0: the key must be released", n)
	}

	for _, step := range []string{"closures", "dispatches"} {
		if r := d.do("POST", "/loads/"+one+"/"+step, "", "key-"+step); r.status != nethttp.StatusOK {
			t.Fatalf("%s of the first load = %d %s", step, r.status, r.raw)
		}
	}
	ok := d.do("POST", "/loads/"+two+"/packages", `{"trackingNumber":"TN-1"}`, "key-pal")
	if ok.status != nethttp.StatusOK || wasReplayed(ok) {
		t.Fatalf("same key after the other load departed = %d %s replayed=%v, want a fresh 200", ok.status, ok.raw, wasReplayed(ok))
	}
	again := d.do("POST", "/loads/"+two+"/packages", `{"trackingNumber":"TN-1"}`, "key-pal")
	if again.status != nethttp.StatusOK || !wasReplayed(again) || again.raw != ok.raw {
		t.Fatalf("third = %d %s replayed=%v, want the stored 200 replayed", again.status, again.raw, wasReplayed(again))
	}
}

// gatedSave is a departure repository whose FIRST Save waits for release and
// then loses the optimistic lock; every later Save succeeds. It records how many
// Saves ran and the most that ran at once.
type gatedSave struct {
	ports.DepartureRepository
	calls, running, peak atomic.Int32
	entered, release     chan struct{}
}

func (g *gatedSave) Save(ctx context.Context, d *departure.Departure) error {
	n := g.calls.Add(1)
	cur := g.running.Add(1)
	defer g.running.Add(-1)
	for p := g.peak.Load(); cur > p && !g.peak.CompareAndSwap(p, cur); p = g.peak.Load() {
	}
	if n == 1 {
		close(g.entered)
		<-g.release
		return ports.ErrConcurrentUpdate
	}
	return g.DepartureRepository.Save(ctx, d)
}

// TestACopyWaitingOnARetryableFirstRunsTheUseCaseItself: copies of a request that
// arrive while the first one is in flight wait on the key's unique index. The
// first ends RETRYABLE and rolls back, so the key row is gone: the next copy is a
// fresh request and runs the use case itself (never replays the released refusal,
// never runs alongside the first); the copies behind it replay ITS answer.
func TestACopyWaitingOnARetryableFirstRunsTheUseCaseItself(t *testing.T) {
	g := &gatedSave{entered: make(chan struct{}), release: make(chan struct{})}
	d := newDBAPIWith(t, func(deps *usecases.Deps) {
		g.DepartureRepository = deps.Departures
		deps.Departures = g
	})
	d.seed("dep-1")

	const copies = 6
	replies := make([]reply, copies)
	var wg sync.WaitGroup
	start := func(i int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replies[i] = d.do("POST", "/departures/dep-1/cancellations", `{"reason":"weather"}`, "key-wait")
		}()
	}
	start(0)
	<-g.entered
	for i := 1; i < copies; i++ {
		start(i)
	}
	time.Sleep(300 * time.Millisecond) // let the copies reach the key's unique index
	close(g.release)
	wg.Wait()

	var refused, fresh, replays int
	for _, r := range replies {
		switch {
		case r.status == nethttp.StatusConflict && r.slug() == "concurrent-update" && !wasReplayed(r):
			refused++
		case r.status == nethttp.StatusOK && !wasReplayed(r):
			fresh++
		case r.status == nethttp.StatusOK && wasReplayed(r):
			replays++
		default:
			t.Fatalf("unexpected reply %d %s replayed=%v", r.status, r.raw, wasReplayed(r))
		}
	}
	if refused != 1 || fresh != 1 || replays != copies-2 {
		t.Fatalf("refused %d fresh %d replayed %d, want 1, 1 and %d", refused, fresh, replays, copies-2)
	}
	if g.calls.Load() != 2 || g.peak.Load() != 1 {
		t.Fatalf("the use case saved %d times, at most %d at once, want 2 and 1", g.calls.Load(), g.peak.Load())
	}
	if n := d.keyRows("key-wait"); n != 1 {
		t.Fatalf("key rows = %d, want 1 (the stored 200)", n)
	}
}
