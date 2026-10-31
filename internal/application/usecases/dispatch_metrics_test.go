package usecases_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

// recordingMetrics counts the ports.LoadMetrics calls of a use case.
type recordingMetrics struct {
	mu         sync.Mutex
	dispatched int
	rejected   []string
}

var _ ports.LoadMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) LoadDispatched(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dispatched++
}

func (m *recordingMetrics) LoadDispatchRejected(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, reason)
}

func TestDispatchLoadCountsEachAttemptOnceByOutcome(t *testing.T) {
	m := &recordingMetrics{}
	e := newEnv(t, func(d *usecases.Deps) { d.Metrics = m })
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	id := e.openLoad("dep-1")

	// An OPEN load cannot be dispatched: a rejection with its problem slug.
	if _, err := e.svc.DispatchLoad(e.ctx, id); err == nil {
		t.Fatal("dispatching an open load must be refused")
	}
	if m.dispatched != 0 || len(m.rejected) != 1 || m.rejected[0] != "illegal-transition" {
		t.Fatalf("after a refusal: dispatched=%d rejected=%v, want 0 / [illegal-transition]", m.dispatched, m.rejected)
	}

	_, err := e.svc.CloseLoad(e.ctx, id)
	e.must(err)
	e.clock.Advance(2 * time.Hour)
	_, err = e.svc.DispatchLoad(e.ctx, id)
	e.must(err)
	if m.dispatched != 1 || len(m.rejected) != 1 {
		t.Fatalf("after a dispatch: dispatched=%d rejected=%v, want 1 / unchanged", m.dispatched, m.rejected)
	}

	// The same dispatch again finds the load DEPARTED: a replay, not an attempt.
	_, err = e.svc.DispatchLoad(e.ctx, id)
	e.must(err)
	if m.dispatched != 1 || len(m.rejected) != 1 {
		t.Fatalf("after a replay: dispatched=%d rejected=%v, want unchanged", m.dispatched, m.rejected)
	}

	if _, err := e.svc.DispatchLoad(e.ctx, "missing"); err == nil {
		t.Fatal("dispatching an unknown load must be refused")
	}
	if len(m.rejected) != 2 || m.rejected[1] != "not-found" {
		t.Fatalf("rejected = %v, want not-found second", m.rejected)
	}
}

// A Service built without Metrics records nothing and never panics.
func TestDispatchLoadWithoutMetricsIsANoOp(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	id := e.openLoad("dep-1")
	_, _ = e.svc.CloseLoad(e.ctx, id)
	e.clock.Advance(2 * time.Hour)
	if _, err := e.svc.DispatchLoad(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DispatchLoad(e.ctx, "missing"); err == nil {
		t.Fatal("unknown load must be refused")
	}
}
