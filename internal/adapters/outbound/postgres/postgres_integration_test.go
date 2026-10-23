//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/wire"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/repocontract"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

// newFixture wires every Postgres adapter over one fresh database.
func newFixture(pool *pgxpool.Pool) repocontract.Fixture {
	return repocontract.Fixture{
		Departures: postgres.NewDepartureRepo(pool), Schedules: postgres.NewScheduleRepo(pool), Loads: postgres.NewLoadRepo(pool),
		Nodes: postgres.NewNodeViewRepo(pool), Lanes: postgres.NewLaneViewRepo(pool), Volumes: postgres.NewVolumeRepo(pool),
		Inbox:  postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), wire.Encoder{NewID: uuid.NewString}),
		UoW:    postgres.NewUnitOfWork(pool),
		OutboxCount: func() (int, error) {
			var n int
			err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events`).Scan(&n)
			return n, err
		},
	}
}

// TestRepositoryContract runs the SAME contract the in-memory doubles pass.
func TestRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Fixture { return newFixture(pgtest.NewPool(t)) })
}

// TestMigrationsGoUpDownUp proves the schema can be rolled back and re-applied.
func TestMigrationsGoUpDownUp(t *testing.T) {
	url := pgtest.URL(t)
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up (no-op on a migrated database): %v", err)
	}
	if err := postgres.RevertMigrations(url); err != nil {
		t.Fatalf("down: %v", err)
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'schema_migrations'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if tables != 0 {
		t.Fatalf("%d tables left after down", tables)
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
