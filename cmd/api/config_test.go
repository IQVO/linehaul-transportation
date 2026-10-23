package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func valid() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "k1:9092, k2:9092"}
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := loadConfig(envOf(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.httpAddr != ":8080" || c.migrationsURL != "postgres://x" || len(c.brokers) != 2 || c.brokers[1] != "k2:9092" ||
		c.relayInterval != time.Second || c.outboxMaxAttempts != 20 || c.generateInterval != time.Hour ||
		c.packagesPerOrder != 1 || c.drainDelay != 5*time.Second || c.logLevel != "info" {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	m := valid()
	m["HTTP_ADDR"], m["MIGRATIONS_DATABASE_URL"], m["OUTBOX_RELAY_INTERVAL"] = ":9", "postgres://direct", "250ms"
	m["GENERATE_INTERVAL"], m["SHUTDOWN_DRAIN_DELAY"], m["OUTBOX_MAX_ATTEMPTS"] = "90", "0", "0"
	m["PACKAGES_PER_ORDER"], m["LOG_LEVEL"] = "3", "debug"
	c, err := loadConfig(envOf(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.httpAddr != ":9" || c.migrationsURL != "postgres://direct" || c.relayInterval != 250*time.Millisecond ||
		c.generateInterval != 90*time.Second || c.drainDelay != 0 || c.outboxMaxAttempts != 0 || c.packagesPerOrder != 3 || c.logLevel != "debug" {
		t.Fatalf("config = %+v", c)
	}
}

// TestBootErrorsNameTheVariable proves a misconfiguration is refused with a
// message that names the variable.
func TestBootErrorsNameTheVariable(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"no database", func(m map[string]string) { delete(m, "DATABASE_URL") }, "DATABASE_URL"},
		{"no brokers", func(m map[string]string) { delete(m, "KAFKA_BROKERS") }, "KAFKA_BROKERS"},
		{"blank brokers", func(m map[string]string) { m["KAFKA_BROKERS"] = " , " }, "KAFKA_BROKERS"},
		{"bad relay interval", func(m map[string]string) { m["OUTBOX_RELAY_INTERVAL"] = "soon" }, "OUTBOX_RELAY_INTERVAL"},
		{"negative interval", func(m map[string]string) { m["GENERATE_INTERVAL"] = "-1s" }, "GENERATE_INTERVAL"},
		{"bad drain delay", func(m map[string]string) { m["SHUTDOWN_DRAIN_DELAY"] = "x" }, "SHUTDOWN_DRAIN_DELAY"},
		{"bad attempts", func(m map[string]string) { m["OUTBOX_MAX_ATTEMPTS"] = "-1" }, "OUTBOX_MAX_ATTEMPTS"},
		{"bad packages", func(m map[string]string) { m["PACKAGES_PER_ORDER"] = "0" }, "PACKAGES_PER_ORDER"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := valid()
			c.mutate(m)
			_, err := loadConfig(envOf(m))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to name %s", err, c.want)
			}
		})
	}
}

func TestUnknownLogLevelFallsBackToInfo(t *testing.T) {
	l := newLogger("loud")
	if !l.Enabled(context.Background(), slog.LevelInfo) || l.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("an unknown level must behave as info")
	}
}
