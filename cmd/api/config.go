package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Boot errors for the variables the context cannot run without. Each message
// names the variable.
var (
	errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")
	errMissingBrokers     = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)")
)

// Defaults of the optional variables.
const (
	defaultHTTPAddr         = ":8080"
	defaultRelayInterval    = time.Second
	defaultOutboxAttempts   = 20
	defaultGenerateInterval = time.Hour
	defaultDrainDelay       = 5 * time.Second
)

// config is the validated environment.
type config struct {
	httpAddr          string
	databaseURL       string
	migrationsURL     string
	brokers           []string
	relayInterval     time.Duration
	outboxMaxAttempts int
	generateInterval  time.Duration
	packagesPerOrder  int
	drainDelay        time.Duration
	logLevel          string
}

// loadConfig validates the environment before anything is opened or bound.
func loadConfig(env func(string) string) (config, error) {
	c := config{httpAddr: orDefault(env("HTTP_ADDR"), defaultHTTPAddr), databaseURL: env("DATABASE_URL"), logLevel: orDefault(env("LOG_LEVEL"), "info")}
	if c.databaseURL == "" {
		return config{}, errMissingDatabaseURL
	}
	c.migrationsURL = orDefault(env("MIGRATIONS_DATABASE_URL"), c.databaseURL)
	if c.brokers = splitList(env("KAFKA_BROKERS")); len(c.brokers) == 0 {
		return config{}, errMissingBrokers
	}
	var err error
	if c.relayInterval, err = durationVar(env, "OUTBOX_RELAY_INTERVAL", defaultRelayInterval); err != nil {
		return config{}, err
	}
	if c.generateInterval, err = durationVar(env, "GENERATE_INTERVAL", defaultGenerateInterval); err != nil {
		return config{}, err
	}
	if c.drainDelay, err = durationVar(env, "SHUTDOWN_DRAIN_DELAY", defaultDrainDelay); err != nil {
		return config{}, err
	}
	if c.outboxMaxAttempts, err = intVar(env, "OUTBOX_MAX_ATTEMPTS", defaultOutboxAttempts, 0); err != nil {
		return config{}, err
	}
	if c.packagesPerOrder, err = intVar(env, "PACKAGES_PER_ORDER", 1, 1); err != nil {
		return config{}, err
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// durationVar reads a Go duration ("1s") or a number of seconds ("5"); "0" is
// allowed (SHUTDOWN_DRAIN_DELAY=0 disables the wait) and negative values are not.
func durationVar(env func(string) string, name string, def time.Duration) (time.Duration, error) {
	raw := env(name)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		secs, serr := strconv.ParseFloat(raw, 64)
		if serr != nil {
			return 0, fmt.Errorf("%s=%q is neither a Go duration nor a number of seconds", name, raw)
		}
		d = time.Duration(secs * float64(time.Second))
	}
	if d < 0 {
		return 0, fmt.Errorf("%s=%q must not be negative", name, raw)
	}
	return d, nil
}

func intVar(env func(string) string, name string, def, minimum int) (int, error) {
	raw := env(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minimum {
		return 0, fmt.Errorf("%s=%q must be an integer >= %d", name, raw, minimum)
	}
	return n, nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
