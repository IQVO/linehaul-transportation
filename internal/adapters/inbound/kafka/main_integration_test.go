//go:build integration

package kafka_test

import (
	"os"
	"testing"

	"github.com/claudioed/linehaul-transportation/internal/testing/kafkatest"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}
