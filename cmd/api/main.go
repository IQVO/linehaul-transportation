// Command api is linehaul-transportation's composition root: it wires env
// config into adapters, adapters into use cases, and use cases into the HTTP
// router, the Kafka consumers and the outbox relay, all on real Postgres and
// real Kafka: there is no in-memory mode, no log sink and no consumer that is
// off.
//
// Environment:
//
//	HTTP_ADDR                   listen address (default :8080)
//	DATABASE_URL                Postgres DSN (REQUIRED)
//	MIGRATIONS_DATABASE_URL     direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS               comma-separated brokers (REQUIRED: relay, consumers, /readyz)
//	NODE_TOPOLOGY_CONSUMER_GROUP          consumer group of the node-topology consumer (REQUIRED)
//	ORDER_SOURCING_CONSUMER_GROUP         consumer group of the order-sourcing consumer (REQUIRED)
//	SHIPPING_AND_TRACKING_CONSUMER_GROUP  consumer group of the shipping-and-tracking consumer (REQUIRED)
//	OUTBOX_RELAY_INTERVAL       relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS         failures before the relay excludes a row (default 20, 0 = unlimited)
//	GENERATE_INTERVAL           how often the horizon roll runs (Go duration or seconds, default 1h)
//	PACKAGES_PER_ORDER          packages one order adds to a pickup (default 1, ADR 0002 A9)
//	SHUTDOWN_DRAIN_DELAY        wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                   debug, info, warn or error (default info)
//	OTEL_SERVICE_NAME           service.name of spans and metrics (default linehaul-transportation)
//	OTEL_EXPORTER_OTLP_ENDPOINT Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION             service.version (default dev)
//	ENVIRONMENT                 deployment.environment.name (default local)
//
// GET /readyz is 200 only while Postgres answers a ping AND a Kafka broker
// answers a metadata request (verdict cached 3s, bounded 2s); /healthz is
// liveness only. The process exits non-zero when the relay, the horizon roll
// or a consumer returns on its own, so the orchestrator restarts it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/kafka"
	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/wire"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/clock"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/ids"
	outboundkafka "github.com/claudioed/linehaul-transportation/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/linehaul-transportation/internal/adapters/outbound/outbox"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// workerDrainTimeout bounds how long shutdown waits for the workers to return
// after their context is cancelled.
const workerDrainTimeout = 10 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way).
func run(ctx context.Context) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.logLevel)
	slog.SetDefault(logger)

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed LAST: deferred before
	// everything below, so the final spans of the drain still leave the process.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	if err := migrate(ctx, cfg); err != nil {
		return err
	}
	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("DATABASE_URL: %w", err)
	}
	defer pool.Close()

	app := buildApp(pool, cfg, logger)
	readiness := inboundhttp.NewReadiness(0, 0,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: app.brokerCheck.Check},
	)
	httpServer := &http.Server{
		Addr: cfg.httpAddr,
		Handler: inboundhttp.NewRouter(&inboundhttp.Server{
			Service: app.service, Readiness: readiness,
			Idempotency: inboundhttp.RequireIdempotencyKey(pool, pgtx.With),
		}, logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}

	exits, stopWorkers := startWorkers(ctx, app.workers, logger)
	defer app.close()
	return serveUntilSignal(ctx, logger, httpServer, readiness, cfg.drainDelay, exits, stopWorkers)
}

// migrate applies the embedded migrations over the direct DSN.
func migrate(ctx context.Context, cfg config) error {
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		if err = postgres.RunMigrations(cfg.migrationsURL); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("migrations (MIGRATIONS_DATABASE_URL / DATABASE_URL): %w", err)
}

// app is everything built over the pool.
type app struct {
	service     *usecases.Service
	brokerCheck *outboundkafka.BrokerCheck
	workers     []worker
	close       func()
}

// buildApp wires the adapters into the use cases and lists the background workers.
func buildApp(pool *pgxpool.Pool, cfg config, logger *slog.Logger) *app {
	outboxRepo := postgres.NewOutboxRepo(pool)
	service := usecases.New(usecases.Deps{
		UoW: postgres.NewUnitOfWork(pool), Departures: postgres.NewDepartureRepo(pool), Schedules: postgres.NewScheduleRepo(pool),
		Loads: postgres.NewLoadRepo(pool), Nodes: postgres.NewNodeViewRepo(pool), Lanes: postgres.NewLaneViewRepo(pool),
		Volumes: postgres.NewVolumeRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewEventOutbox(outboxRepo, wire.Encoder{NewID: uuid.NewString}),
		Clock:  clock.System{}, IDs: ids.UUID{}, PackagesPerOrder: cfg.packagesPerOrder, Logger: logger,
	})
	sink := outboundkafka.NewRelaySink(cfg.brokers)
	relay := outboxrelay.NewOutboxRelay(outboxRepo, sink, logger,
		outboxrelay.WithInterval(cfg.relayInterval),
		outboxrelay.WithMaxAttempts(cfg.outboxMaxAttempts))
	consumers := inboundkafka.NewConsumers(cfg.brokers, inboundkafka.Groups{
		NodeTopology: cfg.nodeTopologyGroup, OrderSourcing: cfg.orderSourcingGroup, Shipping: cfg.shippingGroup,
	}, service, logger)
	workers := []worker{
		{name: "outbox relay", run: relay.Run},
		{name: "horizon roll", run: horizonRoll(service, cfg.generateInterval, logger)},
	}
	for _, c := range consumers {
		workers = append(workers, worker{name: c.Name, run: c.Run})
	}
	return &app{
		service:     service,
		brokerCheck: outboundkafka.NewBrokerCheck(cfg.brokers),
		workers:     workers,
		close: func() {
			_ = sink.Close()
			for _, c := range consumers {
				_ = c.Close()
			}
		},
	}
}

// horizonRoll runs policy HorizonRoll now and then every interval.
func horizonRoll(service *usecases.Service, interval time.Duration, logger *slog.Logger) func(context.Context) error {
	return func(ctx context.Context) error {
		for {
			report, err := service.GenerateDepartures(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.ErrorContext(ctx, "horizon roll failed", "error", err)
			} else if err == nil {
				logger.InfoContext(ctx, "horizon roll", "scheduled", report.Scheduled, "failed", report.Failed)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
		}
	}
}
