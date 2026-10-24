package ids_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/ids"
)

func TestNewLoadIDMintsAPrefixedUUID(t *testing.T) {
	var g ids.UUID
	id := string(g.NewLoadID())
	if !strings.HasPrefix(id, "load-") {
		t.Fatalf("NewLoadID() = %q, want the load- prefix", id)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(id, "load-")); err != nil {
		t.Fatalf("the id after the prefix must be a UUID: %v", err)
	}
	if second := string(g.NewLoadID()); second == id {
		t.Fatal("two calls must mint two ids")
	}
}
