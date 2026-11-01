package repocontract

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
)

var loadCases = []named{
	{"a saved load round-trips with its tracking numbers in order", loadRoundTrip},
	{"GetByDeparture and a miss", loadByDeparture},
	{"FindActiveByTracking sees OPEN and CLOSED loads only", loadFindActive},
	{"a tracking number is on one active load: ErrPackageAlreadyLoaded", loadUniquePackage},
	{"a departure has one load: ErrLoadAlreadyExists", loadUniqueDeparture},
	{"versions: create once, update from the previous version", loadVersions},
}

func saveLoadOn(t *testing.T, f Fixture, depID, loadID string, tns ...string) *load.Load {
	t.Helper()
	ctx := context.Background()
	d := NewDeparture(t, depID, "n-fc", departure.KindLinehaul, time.Hour, 10)
	noErr(t, f.Departures.Save(ctx, d))
	l := NewLoad(t, loadID, d)
	noErr(t, f.Loads.Save(ctx, l))
	for _, tn := range tns {
		_, err := l.AddPackage(tn, false, departure.StateScheduled)
		noErr(t, err)
		noErr(t, f.Loads.Save(ctx, l))
	}
	return l
}

func loadRoundTrip(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1", "TN3", "TN1", "TN2")
	got, err := f.Loads.Get(ctx, "load-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), l.Snapshot()) {
		t.Fatalf("round trip: %+v != %+v", got.Snapshot(), l.Snapshot())
	}
	if strings.Join(got.Snapshot().TrackingNumbers, ",") != "TN3,TN1,TN2" {
		t.Fatalf("order lost: %v", got.Snapshot().TrackingNumbers)
	}
	empty := saveLoadOn(t, f, "dep-2", "load-2")
	got, err = f.Loads.Get(ctx, "load-2")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), empty.Snapshot()) || got.Snapshot().TrackingNumbers == nil {
		t.Fatalf("empty load: %+v", got.Snapshot())
	}
	_, err = f.Loads.Get(ctx, "load-9")
	wantErr(t, err, ports.ErrNotFound)
}

func loadByDeparture(t *testing.T, f Fixture) {
	ctx := context.Background()
	saveLoadOn(t, f, "dep-1", "load-1")
	got, err := f.Loads.GetByDeparture(ctx, "dep-1")
	noErr(t, err)
	if got.ID() != "load-1" {
		t.Fatalf("load = %s", got.ID())
	}
	_, err = f.Loads.GetByDeparture(ctx, "dep-9")
	wantErr(t, err, ports.ErrNotFound)
}

func loadFindActive(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1", "TN1")
	id, err := f.Loads.FindActiveByTracking(ctx, "TN1")
	noErr(t, err)
	if id != "load-1" {
		t.Fatalf("id = %s", id)
	}
	_, err = f.Loads.FindActiveByTracking(ctx, "TN-none")
	wantErr(t, err, ports.ErrNotFound)
	d, _ := f.Departures.Get(ctx, "dep-1")
	_, err = l.Close(departure.StateScheduled, at)
	noErr(t, err)
	noErr(t, f.Loads.Save(ctx, l))
	if _, err := f.Loads.FindActiveByTracking(ctx, "TN1"); err != nil {
		t.Fatalf("a CLOSED load is active: %v", err)
	}
	noErr(t, d.Close())
	_, err = l.Dispatch(at)
	noErr(t, err)
	noErr(t, f.Loads.Save(ctx, l))
	_, err = f.Loads.FindActiveByTracking(ctx, "TN1")
	wantErr(t, err, ports.ErrNotFound)
}

func loadUniquePackage(t *testing.T, f Fixture) {
	ctx := context.Background()
	saveLoadOn(t, f, "dep-1", "load-1", "TN1")
	other := saveLoadOn(t, f, "dep-2", "load-2")
	_, err := other.AddPackage("TN1", false, departure.StateScheduled) // the use case would pass true; the index is the backstop
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, other), load.ErrPackageAlreadyLoaded)
	got, _ := f.Loads.Get(ctx, "load-2")
	if got.PackageCount() != 0 {
		t.Fatal("the refused save must leave the stored load unchanged")
	}
}

func loadUniqueDeparture(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1")
	d, _ := f.Departures.Get(ctx, "dep-1")
	second, err := load.Open("load-2", d, false)
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, second), load.ErrLoadAlreadyExists)
	_ = l
}

func loadVersions(t *testing.T, f Fixture) {
	ctx := context.Background()
	l := saveLoadOn(t, f, "dep-1", "load-1")
	d, _ := f.Departures.Get(ctx, "dep-1")
	dup, err := load.Open("load-1", d, false)
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, dup), ports.ErrConcurrentUpdate)
	stale, _ := f.Loads.Get(ctx, "load-1")
	_, err = l.AddPackage("TN1", false, departure.StateScheduled)
	noErr(t, err)
	noErr(t, f.Loads.Save(ctx, l))
	_, err = stale.AddPackage("TN2", false, departure.StateScheduled)
	noErr(t, err)
	wantErr(t, f.Loads.Save(ctx, stale), ports.ErrConcurrentUpdate)
}
