//go:build integration

package postgres_test

import (
	"os"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

// The tcpostgres reference keeps the testcontainers module reachable from this
// package (TestPostgresIntegrationTestsUseTestcontainers): the database comes from
// internal/testing/pgtest, never from DATABASE_URL.
var _ = tcpostgres.Run

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}
