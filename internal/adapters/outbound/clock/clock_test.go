package clock_test

import (
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/clock"
)

func TestSystemNowIsUTCAndCurrent(t *testing.T) {
	var c clock.System
	before := time.Now().Add(-2 * time.Second)
	got := c.Now()
	after := time.Now().Add(2 * time.Second)
	if got.Before(before) || got.After(after) {
		t.Fatalf("Now() = %v, want an instant inside [%v, %v]", got, before, after)
	}
	if _, offset := got.Zone(); offset != 0 {
		t.Fatalf("Now() offset = %ds, want UTC", offset)
	}
}
