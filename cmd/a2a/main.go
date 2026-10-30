// Command a2a is the composition root for the linehaul-transportation A2A
// (Agent2Agent) agent (docs/adr/0011-a2a-inbound-adapter.md): it wires env
// config to the Postgres departure repository, the repository to the SAME
// application-layer read methods the REST and MCP adapters serve
// (ListDepartures, GetDeparture), and those to the inbound A2A adapter, then
// serves the Agent Card at /.well-known/agent-card.json and the JSON-RPC
// endpoint at both / and /a2a (and nothing else). It is a third, independent
// deployable beside cmd/api and cmd/mcp.
//
// The surface is READ-ONLY, exactly like cmd/mcp: no write use case is
// reachable (the adapter holds a read interface, and the usecases.Deps literal
// below sets only the departure repository -- never the unit of work, the
// outbox or another repository), so this binary can never insert into the
// outbox. It never starts the outbox relay, never runs the horizon roll or the
// local-copy consumers and never dials Kafka (no KAFKA_BROKERS, no consumer
// groups, no EVENT_PUBLISHER): those run in cmd/api only. It makes no REST,
// MCP or A2A call to any sibling context.
//
// There is NO auth of any kind (fleet-wide decision): no keys, no bearer
// checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	A2A_ADDR                 listen address (default :8093)
//	A2A_AGENT_URL            REQUIRED. Absolute http(s) URL the Agent Card advertises for the JSON-RPC endpoint
//	DATABASE_URL             REQUIRED. Postgres DSN (the service database cmd/api owns)
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	LOG_LEVEL                debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME        service.name of spans and metrics (default linehaul-transportation-a2a)
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
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inbounda2a "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/a2a"
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
		slog.Error("a2a server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// The configuration is validated before telemetry, any connection and the
	// listener: a bad environment fails fast, naming every offending variable.
	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}
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

	databaseURL := cfg.databaseURL
	deps, closeAdapters, err := buildDeps(context.Background(), logger, databaseURL, getenv("MIGRATIONS_DATABASE_URL", databaseURL))
	if err != nil {
		return err
	}
	defer closeAdapters()

	srv := &http.Server{
		Addr:              getenv("A2A_ADDR", ":8093"),
		Handler:           newAgent(deps, cfg.agentURL, tel.serviceVersion, logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveA2A(logger, srv)
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
// (internal/architecture's TestA2AAdapterIsReadOnly pins this).
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL string) (inbounda2a.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inbounda2a.Deps{}, noop, errNoDatabase
	}
	if err := bootRetry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return inbounda2a.Deps{}, noop, fmt.Errorf("DATABASE_URL: %w", err)
	}
	if err := bootRetry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inbounda2a.Deps{}, noop, err
	}
	logger.Info("postgres repositories configured")
	service := usecases.New(usecases.Deps{Departures: postgres.NewDepartureRepo(pool)})
	return inbounda2a.Deps{Departures: service}, pool.Close, nil
}

// serveA2A runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveA2A(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("a2a server listening (JSON-RPC over HTTP)", "addr", srv.Addr)
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

// bootConfig is the validated part of the environment that must be right
// before anything else starts.
type bootConfig struct {
	databaseURL string
	agentURL    string
}

// loadBootConfig reads and validates the mandatory environment, reporting
// every problem at once so an operator fixes the environment in one pass.
// Every message names its variable (the chart's mandatory-env scan reads the
// "<VAR> is required" phrasing).
func loadBootConfig() (bootConfig, error) {
	cfg := bootConfig{databaseURL: os.Getenv("DATABASE_URL"), agentURL: strings.TrimSpace(os.Getenv("A2A_AGENT_URL"))}
	var problems []error
	if cfg.databaseURL == "" {
		problems = append(problems, errNoDatabase)
	}
	if err := checkAgentURL(cfg.agentURL); err != nil {
		problems = append(problems, err)
	}
	return cfg, errors.Join(problems...)
}

// checkAgentURL requires the Agent Card URL to be an absolute http(s) URL with
// a host: a card that advertises something clients cannot dial is worse than
// no card, so the process refuses to boot instead.
func checkAgentURL(raw string) error {
	if raw == "" {
		return errors.New("A2A_AGENT_URL is required: the Agent Card must advertise the absolute URL clients dial " +
			"(e.g. http://<release>-a2a.<namespace>.svc.cluster.local:8093/)")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("A2A_AGENT_URL=%q is not an absolute http(s) URL", raw)
	}
	return nil
}
