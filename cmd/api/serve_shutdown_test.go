package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http"
)

// The shutdown contract of startWorkers + serveUntilSignal, proven without
// Postgres or Kafka: a cancellation of the CALLER's ctx (how the integration
// tests stop run) is a clean shutdown in the fleet order — the workers (the
// outbox relay among them) keep running through the readiness flip and the
// drain and stop only at stopWorkers — never "worker X stopped"; a worker that
// returns on its own while the service is serving is still a death that ends
// run with an error.

// fakeWorker stands in for a background loop. Like the real ones it returns
// nil once its context is cancelled; stopOnItsOwn makes it return at once
// while its context is still live (a worker that died).
type fakeWorker struct {
	stopOnItsOwn bool

	started  chan struct{}
	ctxDone  atomic.Bool
	returned atomic.Bool
}

func newFakeWorker() *fakeWorker { return &fakeWorker{started: make(chan struct{})} }

func (f *fakeWorker) run(ctx context.Context) error {
	close(f.started)
	defer f.returned.Store(true)
	if f.stopOnItsOwn {
		return nil
	}
	<-ctx.Done()
	f.ctxDone.Store(true)
	return nil
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// serve wires one fake worker exactly as run does (startWorkers on the
// caller's ctx) and runs serveUntilSignal in the background on a loopback
// ephemeral port.
func serve(ctx context.Context, readiness *inboundhttp.Readiness, drain time.Duration, w *fakeWorker) <-chan error {
	logger := quietLogger()
	exits, stopWorkers := startWorkers(ctx, []worker{{name: "outbox relay", run: w.run}}, logger)
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- serveUntilSignal(ctx, logger, srv, readiness, drain, exits, stopWorkers) }()
	return done
}

func waitResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("serveUntilSignal did not return")
		return nil
	}
}

// A caller-ctx cancellation must not reach the workers before the shutdown
// sequence does: they keep running through the readiness flip and the drain
// delay, stop at stopWorkers, and the shutdown returns nil. Workers started on
// a context derived from the caller's ctx are cancelled at the same instant as
// sigCtx — their clean return lands on exits, races sigCtx.Done() in the
// serving select, and run reports "worker ... stopped" for a clean stop.
func TestServeUntilSignal_ACallerCancelIsACleanShutdownThatStopsTheWorkersAfterTheDrain(t *testing.T) {
	readiness := inboundhttp.NewReadiness(0, 0)
	w := newFakeWorker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := serve(ctx, readiness, 300*time.Millisecond, w)
	<-w.started

	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for readiness.Ready() {
		select {
		case err := <-done:
			if err != nil && strings.Contains(err.Error(), "stopped") {
				t.Fatalf("serveUntilSignal returned %v: the caller's cancellation was taken for a worker that "+
					"stopped on its own", err)
			}
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("readiness never flipped to not-ready after the caller's cancellation")
		}
		time.Sleep(time.Millisecond)
	}
	// Inside the 300 ms drain window: the workers must still be running.
	if w.ctxDone.Load() || w.returned.Load() {
		t.Error("the caller's cancellation stopped the workers before the drain: their context must outlive " +
			"the caller's ctx and end only at stopWorkers")
	}

	if err := waitResult(t, done); err != nil {
		t.Fatalf("a caller-ctx cancellation must be a clean shutdown, serveUntilSignal returned %v", err)
	}
	if !w.returned.Load() {
		t.Error("the shutdown returned without stopping and awaiting the workers")
	}
}

// The exact symptom, repeated: with no drain delay the window between the
// caller's cancellation and the serving select is as small as it gets, and
// not a single stop may come back as an error.
func TestServeUntilSignal_ACallerCancelNeverReportsAWorkerStopped(t *testing.T) {
	const rounds = 200
	failures := 0
	var first error
	for range rounds {
		w := newFakeWorker()
		ctx, cancel := context.WithCancel(context.Background())
		done := serve(ctx, inboundhttp.NewReadiness(0, 0), 0, w)
		<-w.started
		cancel()
		if err := waitResult(t, done); err != nil {
			failures++
			if first == nil {
				first = err
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d caller-ctx cancellations returned an error (first: %v), want 0", failures, rounds, first)
	}
}

// A worker that returns on its own while the service is serving is a death:
// serveUntilSignal stops serving and returns an error naming it (the pod
// restarts).
func TestServeUntilSignal_AWorkerThatStopsOnItsOwnIsAnError(t *testing.T) {
	w := newFakeWorker()
	w.stopOnItsOwn = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := waitResult(t, serve(ctx, inboundhttp.NewReadiness(0, 0), 0, w))
	if err == nil || !strings.Contains(err.Error(), "worker outbox relay stopped") {
		t.Fatalf("a worker that stopped on its own must end serving with an error naming it, got %v", err)
	}
}
