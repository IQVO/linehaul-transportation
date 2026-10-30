package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback.
func TestBuildDeps_MissingDatabaseFailsBoot(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want an error naming DATABASE_URL", err)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes the boot retry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

// The boot migrations run against MIGRATIONS_DATABASE_URL (a direct DSN
// behind PgBouncer) and never against DATABASE_URL. The retry error wraps only
// ctx.Err(), so the cause is read from the logged "retrying ... error=" line:
// a schemeless migrations DSN is rejected by the migration driver at once,
// while DATABASE_URL (reached only after the migrations) points at a closed
// port and would show a dial error if the two were ever conflated.
func TestBuildDeps_RunsMigrationsAgainstMigrationsDatabaseURL(t *testing.T) {
	const (
		bogusMigrationsURL = "not-a-valid-connection-string"
		unreachableAppURL  = "postgres://u:pw@127.0.0.1:1/linehaul?sslmode=disable&connect_timeout=1"
	)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if _, _, err := buildDeps(ctx, logger, unreachableAppURL, bogusMigrationsURL); err == nil {
		t.Fatal("a malformed MIGRATIONS_DATABASE_URL must fail boot")
	}
	out := logs.String()
	if !strings.Contains(out, "run migrations failed; retrying") {
		t.Fatalf("the migration step was never attempted: %s", out)
	}
	if strings.Contains(out, "refused") || strings.Contains(out, "127.0.0.1") || strings.Contains(out, "ping postgres") {
		t.Fatalf("the migrations ran against DATABASE_URL instead of MIGRATIONS_DATABASE_URL: %s", out)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("LH_A2A_TEST_SET", "v")
	t.Setenv("LH_A2A_TEST_EMPTY", "")
	if got := getenv("LH_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("LH_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestMigrationsDatabaseURLFallsBackToDatabaseURL(t *testing.T) {
	const databaseURL = "postgres://u:pw@pgbouncer.example:6432/linehaul"
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got != databaseURL {
		t.Fatalf("fallback = %q, want DATABASE_URL", got)
	}
	t.Setenv("MIGRATIONS_DATABASE_URL", "postgres://u:pw@direct.example:5432/linehaul")
	if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got == databaseURL {
		t.Fatal("MIGRATIONS_DATABASE_URL must win over DATABASE_URL when set")
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
