package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http"
)

// worker is one background loop (the outbox relay, the horizon roll, a
// consumer). It runs until its context is cancelled; returning on its own is a
// failure that ends the process.
type worker struct {
	name string
	run  func(ctx context.Context) error
}

// workerExit reports a worker that returned.
type workerExit struct {
	name string
	err  error
}

// startWorkers runs every worker on a context that stopWorkers cancels, and
// returns the channel their exits arrive on.
func startWorkers(parent context.Context, ws []worker, logger *slog.Logger) (<-chan workerExit, func()) {
	ctx, cancel := context.WithCancel(parent)
	exits := make(chan workerExit, len(ws))
	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := w.run(ctx)
			if ctx.Err() == nil {
				logger.Error("worker stopped on its own", "worker", w.name, "error", err)
			}
			exits <- workerExit{name: w.name, err: err}
		}()
	}
	return exits, func() {
		cancel()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(workerDrainTimeout):
			logger.Warn("workers did not stop within the drain timeout", "timeout", workerDrainTimeout)
		}
	}
}

// serveUntilSignal serves HTTP until SIGINT/SIGTERM, ctx cancellation, a
// listener failure or a worker exit, then shuts down in order: /readyz flips
// to not-ready, the drain delay passes, the listener drains, the workers stop.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, srv *http.Server, readiness *inboundhttp.Readiness,
	drainDelay time.Duration, workers <-chan workerExit, stopWorkers func()) error {
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	logger.Info("listening", "addr", srv.Addr)

	var result error
	select {
	case <-sigCtx.Done():
		logger.Info("shutting down")
	case err := <-serveErr:
		result = fmt.Errorf("http server: %w", err)
	case w := <-workers:
		result = fmt.Errorf("worker %s stopped: %w", w.name, errOrStopped(w.err))
	}

	readiness.SetNotReady()
	if drainDelay > 0 && result == nil {
		time.Sleep(drainDelay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		result = errors.Join(result, fmt.Errorf("http shutdown: %w", err))
	}
	stopWorkers()
	return result
}

func errOrStopped(err error) error {
	if err == nil {
		return errors.New("returned without an error")
	}
	return err
}
