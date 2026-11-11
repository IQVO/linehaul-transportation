package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

// A follower of a merged package carries the carrier's id in ConsolidatedInto
// and the carrier's node and departure (ADR 0014).
func (e *env) lockFollower(follower, carrier, node, dep string) {
	e.t.Helper()
	in := lockIn(follower, node, dep)
	in.ConsolidatedInto = carrier
	e.must(e.svc.HandleLocked(e.ctx, nextID(), in))
}

func (e *env) hasNoVolume(asg string) {
	e.t.Helper()
	if _, err := e.store.Volumes().Get(e.ctx, asg); !errors.Is(err, ports.ErrNotFound) {
		e.t.Fatalf("%s must have no ExpectedVolume row, got err = %v", asg, err)
	}
}

func TestCarrierLockBooksOnePackage(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.lock("asg-carrier", "n-fc", "dep-1")
	if d := e.departure("dep-1"); d.Booked() != 1 {
		t.Fatalf("booked = %d, want 1", d.Booked())
	}
	sameNames(t, e.eventNames(), []string{"DepartureCapacityChanged"})
}

func TestFollowerLockBooksNothing(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.lockFollower("asg-follower", "asg-carrier", "n-fc", "dep-1")
	if d := e.departure("dep-1"); d.Booked() != 0 {
		t.Fatalf("a follower must not book: booked = %d", d.Booked())
	}
	e.hasNoVolume("asg-follower")
	if got := len(e.outbox.Events()); got != 0 {
		t.Fatalf("a follower lock publishes nothing, got %d events", got)
	}
	if c := e.store.Counts(); c.Volumes != 0 || c.Processed != 1 || c.OutboxEvents != 0 {
		t.Fatalf("counts = %+v, want only the inbox claim", c)
	}
}

func TestFollowerLockIsClaimedAndRedeliveryIsIdempotent(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	in := lockIn("asg-follower", "n-fc", "dep-1")
	in.ConsolidatedInto = "asg-carrier"
	e.must(e.svc.HandleLocked(e.ctx, "evt-follower", in))
	before := e.store.Counts()
	e.must(e.svc.HandleLocked(e.ctx, "evt-follower", in))
	e.unchanged(before)
}

func TestMergedPackageBooksOnceInEitherOrder(t *testing.T) {
	for _, carrierFirst := range []bool{true, false} {
		e := newEnv(t)
		e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
		if carrierFirst {
			e.lock("asg-carrier", "n-fc", "dep-1")
		}
		e.lockFollower("asg-f1", "asg-carrier", "n-fc", "dep-1")
		e.lockFollower("asg-f2", "asg-carrier", "n-fc", "dep-1")
		if !carrierFirst {
			e.lock("asg-carrier", "n-fc", "dep-1")
		}
		if d := e.departure("dep-1"); d.Booked() != 1 {
			t.Fatalf("carrierFirst=%v: booked = %d, want 1 package for the merged group", carrierFirst, d.Booked())
		}
		if v := e.volume("asg-carrier"); v.BookedOn() != "dep-1" {
			t.Fatalf("carrierFirst=%v: carrier booked on %q", carrierFirst, v.BookedOn())
		}
		e.hasNoVolume("asg-f1")
		e.hasNoVolume("asg-f2")
		sameNames(t, e.eventNames(), []string{"DepartureCapacityChanged"})
	}
}

// The skip must not turn into a free pass for a lock that names no merge: a
// dissolved follower locks later on its own, without consolidatedInto.
func TestAFormerFollowerLockingAloneIsBooked(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.lockFollower("asg-f1", "asg-carrier", "n-fc", "dep-1")
	e.lock("asg-f1", "n-fc", "dep-1")
	if d := e.departure("dep-1"); d.Booked() != 1 {
		t.Fatalf("booked = %d, want 1", d.Booked())
	}
}
