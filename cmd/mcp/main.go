// Command mcp is the composition root for the linehaul-transportation MCP
// server (docs/adr/0008-mcp-server-adoption.md): it wires env config to the
// Postgres departure repository, the repository to the SAME application-layer
// read methods the REST adapter serves (ListDepartures, GetDeparture), and
// those to the inbound MCP adapter, then serves MCP over Streamable HTTP
// (and nothing else: no stdio, no SSE). It is a second, independent
// deployable alongside cmd/api.
//
// The surface is READ-ONLY: no write use case is reachable (the adapter holds
// a read interface, and the usecases.Deps literal below sets only the
// departure repository -- never the unit of work, the outbox or another
// repository), so this binary can never insert into the outbox. It never
// starts the outbox relay, never runs the horizon roll or the local-copy
// consumers and never dials Kafka (no KAFKA_BROKERS, no consumer groups):
// those run in cmd/api only. It makes no REST or MCP call to any sibling
// context.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): no keys, no
// bearer checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	MCP_ADDR                 listen address (default :8090)
//	DATABASE_URL             REQUIRED. Postgres DSN (the service database cmd/api owns)
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	LOG_LEVEL                debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME        service.name of spans and metrics (default linehaul-transportation-mcp)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION          service.version (default dev)
//	ENVIRONMENT              deployment.environment.name (default local)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inboundmcp "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/mcp"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/telemetry"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// bootAttempts / bootInterval mirror cmd/api's boot retry: the migrations and
// the first pool ping retry for up to ~30s while Postgres comes up, then the
// process refuses to boot.
const (
	bootAttempts = 30
	bootInterval = time.Second
)

// errNoDatabase is the boot error of a missing DATABASE_URL: this binary has
// no in-memory mode.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed last.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	databaseURL := os.Getenv("DATABASE_URL")
	deps, closeAdapters, err := buildDeps(context.Background(), logger, databaseURL, getenv("MIGRATIONS_DATABASE_URL", databaseURL))
	if err != nil {
		return err
	}
	defer closeAdapters()

	srv := &http.Server{
		Addr:              getenv("MCP_ADDR", ":8090"),
		Handler:           newRouter(inboundmcp.Handler(inboundmcp.NewServer(deps)), logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveMCP(logger, srv)
}

// bootRetry runs fn until it succeeds, the attempts run out or ctx is done;
// each failure is logged so a slow Postgres is visible in the pod log.
func bootRetry(ctx context.Context, logger *slog.Logger, what string, fn func() error) error {
	var err error
	for attempt := 0; attempt < bootAttempts; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		logger.Warn(what+" failed; retrying", "attempt", attempt+1, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bootInterval):
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

// buildDeps migrates the database, connects a pgx pool and wires the read
// side of the service over the Postgres departure repository. The returned
// close func releases the pool. An empty databaseURL is a boot error: there
// is no fallback runtime.
//
// Like cmd/api this binary runs the idempotent embedded migrations on start,
// so it can boot against a fresh database before cmd/api has;
// golang-migrate's advisory lock makes concurrent starts safe.
// migrationsDatabaseURL is used ONLY for that step (a DIRECT Postgres DSN
// where DATABASE_URL points at PgBouncer, which cannot honour the
// session-scoped advisory lock); the runtime pool always uses databaseURL.
//
// The usecases.Deps literal sets ONLY the departure repository: no unit of
// work, no outbox, no second repository, so no write path is reachable
// (internal/architecture's TestMCPServerIsReadOnly pins this).
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL string) (inboundmcp.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inboundmcp.Deps{}, noop, errNoDatabase
	}
	if err := bootRetry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return inboundmcp.Deps{}, noop, fmt.Errorf("DATABASE_URL: %w", err)
	}
	if err := bootRetry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inboundmcp.Deps{}, noop, err
	}
	logger.Info("postgres repositories configured")
	service := usecases.New(usecases.Deps{Departures: postgres.NewDepartureRepo(pool)})
	return inboundmcp.Deps{Departures: service}, pool.Close, nil
}

// serveMCP runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveMCP(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newLogger builds the process-wide JSON logger. LOG_LEVEL maps
// debug|info|warn|warning|error (case-insensitive; "warning" is accepted
// like "warn" fleet-wide), defaulting to Info.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
