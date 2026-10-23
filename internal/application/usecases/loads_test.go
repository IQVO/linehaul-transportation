package usecases_test

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func (e *env) openLoad(depID string) string {
	e.t.Helper()
	v, err := e.svc.OpenLoad(e.ctx, depID)
	e.must(err)
	return v.LoadID
}

func TestOpenLoad(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindLinehaul, time.Hour, 3)
	v, err := e.svc.OpenLoad(e.ctx, "dep-1")
	e.must(err)
	if v.LoadID != "load-1" || v.DepartureID != "dep-1" || v.DepartureKind != "LINEHAUL" || v.CapacityPackages != 3 || v.State != "OPEN" ||
		v.OriginNodeID != "n-fc" || v.DestinationNodeID != "n-sc" || v.TrackingNumbers == nil {
		t.Fatalf("view = %+v", v)
	}
	if len(e.outbox.Events()) != 0 {
		t.Fatal("OpenLoad raises no event")
	}
}

func TestOpenLoadRefusals(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	e.openLoad("dep-1")
	before := e.store.Counts()
	_, err := e.svc.OpenLoad(e.ctx, "dep-1")
	wantErr(t, "second load", err, load.ErrLoadAlreadyExists)
	_, err = e.svc.OpenLoad(e.ctx, "dep-9")
	wantErr(t, "unknown", err, ports.ErrNotFound)
	e.unchanged(before)
	e.departureAt("dep-2", departure.KindPickup, time.Hour, 3)
	_, _ = e.svc.CancelDeparture(e.ctx, "dep-2", "planner")
	before = e.store.Counts()
	_, err = e.svc.OpenLoad(e.ctx, "dep-2")
	wantErr(t, "cancelled departure", err, shared.ErrIllegalTransition)
	e.unchanged(before)
}

func TestAddPackageToAPickupLoadDoesNotBook(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	id := e.openLoad("dep-1")
	v, err := e.svc.AddPackage(e.ctx, id, "TN1")
	e.must(err)
	if v.PackageCount != 1 || strings.Join(v.TrackingNumbers, ",") != "TN1" {
		t.Fatalf("view = %+v", v)
	}
	if e.departure("dep-1").Booked() != 0 || len(e.outbox.Events()) != 0 {
		t.Fatal("a pickup package was booked at the lock: booked does not change and nothing is published")
	}
}

func TestAddPackageToALinehaulLoadBooksTheDeparture(t *testing.T) {
	for _, kind := range []departure.Kind{departure.KindLinehaul, departure.KindAir} {
		e := newEnv(t)
		e.departureAt("dep-1", kind, time.Hour, 2)
		id := e.openLoad("dep-1")
		_, err := e.svc.AddPackage(e.ctx, id, "TN1")
		e.must(err)
		if d := e.departure("dep-1"); d.Booked() != 1 || d.Remaining() != 1 {
			t.Fatalf("%s: booked = %d", kind, d.Booked())
		}
		sameNames(t, e.eventNames(), []string{"DepartureCapacityChanged"})
		if ev := eventsOf[departure.DepartureCapacityChanged](e)[0]; ev.BookedPackages != 1 || ev.RemainingPackages != 1 {
			t.Fatalf("event = %+v", ev)
		}
	}
}

func TestAddPackageRepeatChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindLinehaul, time.Hour, 2)
	id := e.openLoad("dep-1")
	_, err := e.svc.AddPackage(e.ctx, id, "TN1")
	e.must(err)
	before := e.store.Counts()
	v, err := e.svc.AddPackage(e.ctx, id, "TN1")
	e.must(err)
	if v.PackageCount != 1 {
		t.Fatalf("view = %+v", v)
	}
	e.unchanged(before)
}

func TestAddPackageRefusals(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindLinehaul, time.Hour, 1)
	e.departureAt("dep-2", departure.KindLinehaul, time.Hour, 5)
	one, two := e.openLoad("dep-1"), e.openLoad("dep-2")
	_, err := e.svc.AddPackage(e.ctx, one, "TN1")
	e.must(err)
	before := e.store.Counts()
	_, err = e.svc.AddPackage(e.ctx, two, "TN1")
	wantErr(t, "on another load", err, load.ErrPackageAlreadyLoaded)
	_, err = e.svc.AddPackage(e.ctx, one, "TN2")
	wantErr(t, "over capacity", err, load.ErrLoadOverCapacity)
	_, err = e.svc.AddPackage(e.ctx, "load-9", "TN3")
	wantErr(t, "unknown load", err, ports.ErrNotFound)
	_, err = e.svc.AddPackage(e.ctx, two, " bad")
	wantErr(t, "invalid tracking number", err, shared.ErrValidation)
	e.unchanged(before)
}

func TestAddPackageToALinehaulDepartureRefusesWhenTheDepartureIsFullOrClosed(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindLinehaul, time.Hour, 2)
	id := e.openLoad("dep-1")
	e.book("dep-1", 2)
	before := e.store.Counts()
	_, err := e.svc.AddPackage(e.ctx, id, "TN1")
	wantErr(t, "departure full", err, departure.ErrDepartureFull)
	e.unchanged(before)
	e.departureFrom("dep-2", "n-x", departure.KindLinehaul, 3*time.Hour, 5)
	e.clock.Set(t0.Add(4 * time.Hour))
	id2 := e.openLoad("dep-2")
	before = e.store.Counts()
	_, err = e.svc.AddPackage(e.ctx, id2, "TN9")
	wantErr(t, "after the cutoff", err, departure.ErrDepartureClosed)
	e.unchanged(before)
}

func TestLoadLifecycleDrivesTheDeparture(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	id := e.openLoad("dep-1")
	_, _ = e.svc.AddPackage(e.ctx, id, "TN1")
	_, _ = e.svc.AddPackage(e.ctx, id, "TN2")

	v, err := e.svc.CloseLoad(e.ctx, id)
	e.must(err)
	if v.State != "CLOSED" || e.departure("dep-1").State() != departure.StateClosed {
		t.Fatalf("close: %+v / %s", v, e.departure("dep-1").State())
	}
	sameNames(t, e.eventNames(), []string{"LoadClosed"})
	if ev := eventsOf[load.LoadClosed](e)[0]; ev.PackageCount != 2 || ev.DepartureID != "dep-1" {
		t.Fatalf("LoadClosed = %+v", ev)
	}

	e.clock.Advance(2 * time.Hour)
	v, err = e.svc.DispatchLoad(e.ctx, id)
	e.must(err)
	assertDispatched(t, e, v)

	e.clock.Advance(3 * time.Hour)
	v, err = e.svc.ReceiveLoad(e.ctx, id)
	e.must(err)
	assertReceived(t, e, v)
}

func assertDispatched(t *testing.T, e *env, v views.Load) {
	t.Helper()
	if v.State != "DEPARTED" || v.DepartedAt == nil || e.departure("dep-1").State() != departure.StateDeparted {
		t.Fatalf("dispatch: %+v", v)
	}
	sameNames(t, e.eventNames(), []string{"LoadClosed", "LoadDeparted", "DepartureDeparted"})
	dep := eventsOf[load.LoadDeparted](e)[0]
	if dep.DepartureKind != departure.KindPickup || dep.NodeType != network.FulfillmentCenter || strings.Join(dep.TrackingNumbers, ",") != "TN1,TN2" ||
		dep.OriginNodeID != "n-fc" || !dep.DepartedAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("LoadDeparted = %+v", dep)
	}
}

func assertReceived(t *testing.T, e *env, v views.Load) {
	t.Helper()
	if v.State != "ARRIVED" || v.ArrivedAt == nil || e.departure("dep-1").State() != departure.StateArrived {
		t.Fatalf("receive: %+v", v)
	}
	sameNames(t, e.eventNames(), []string{"LoadClosed", "LoadDeparted", "DepartureDeparted", "LoadArrived", "DepartureArrived"})
	arr := eventsOf[load.LoadArrived](e)[0]
	if arr.DestinationNodeID != "n-sc" || arr.NodeType != network.SortationCenter || arr.DepartureKind != departure.KindPickup {
		t.Fatalf("LoadArrived = %+v", arr)
	}
}

func TestLoadCommandsAreIdempotentOnTheirTargetState(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	id := e.openLoad("dep-1")
	steps := []func() error{
		func() error { _, err := e.svc.CloseLoad(e.ctx, id); return err },
		func() error { _, err := e.svc.DispatchLoad(e.ctx, id); return err },
		func() error { _, err := e.svc.ReceiveLoad(e.ctx, id); return err },
	}
	for i, step := range steps {
		e.must(step())
		before := e.store.Counts()
		e.must(step())
		e.unchanged(before)
		_ = i
	}
}

func TestLoadCommandsRefuseTheWrongState(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	id := e.openLoad("dep-1")
	before := e.store.Counts()
	_, err := e.svc.DispatchLoad(e.ctx, id)
	wantErr(t, "dispatch an open load", err, shared.ErrIllegalTransition)
	_, err = e.svc.ReceiveLoad(e.ctx, id)
	wantErr(t, "receive an open load", err, shared.ErrIllegalTransition)
	e.unchanged(before)
	_, _ = e.svc.CloseLoad(e.ctx, id)
	before = e.store.Counts()
	_, err = e.svc.AddPackage(e.ctx, id, "TN1")
	wantErr(t, "add to a closed load", err, shared.ErrIllegalTransition)
	_, err = e.svc.ReceiveLoad(e.ctx, id)
	wantErr(t, "receive a closed load", err, shared.ErrIllegalTransition)
	e.unchanged(before)
	_, _ = e.svc.DispatchLoad(e.ctx, id)
	before = e.store.Counts()
	_, err = e.svc.CloseLoad(e.ctx, id)
	wantErr(t, "close a departed load", err, shared.ErrIllegalTransition)
	e.unchanged(before)
	_, _ = e.svc.ReceiveLoad(e.ctx, id)
	before = e.store.Counts()
	_, err = e.svc.DispatchLoad(e.ctx, id)
	wantErr(t, "dispatch an arrived load", err, shared.ErrIllegalTransition)
	e.unchanged(before)
	for name, fn := range map[string]func() error{
		"close":    func() error { _, err := e.svc.CloseLoad(e.ctx, "load-9"); return err },
		"dispatch": func() error { _, err := e.svc.DispatchLoad(e.ctx, "load-9"); return err },
		"receive":  func() error { _, err := e.svc.ReceiveLoad(e.ctx, "load-9"); return err },
	} {
		wantErr(t, name+" unknown", fn(), ports.ErrNotFound)
	}
}

func TestLoadOfACancelledDepartureCannotChange(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 3)
	id := e.openLoad("dep-1")
	_, err := e.svc.CancelDeparture(e.ctx, "dep-1", "planner")
	e.must(err)
	_, err = e.svc.AddPackage(e.ctx, id, "TN1")
	wantErr(t, "add", err, shared.ErrIllegalTransition)
	_, err = e.svc.CloseLoad(e.ctx, id)
	wantErr(t, "close", err, shared.ErrIllegalTransition)
}
