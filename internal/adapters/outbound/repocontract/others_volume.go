package repocontract

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

var volumeCases = []named{
	{"a saved row round-trips and a miss is ErrNotFound", volRoundTrip},
	{"versions: one save per unit of work, from the previous version", volVersions},
	{"FindByTracking and ListBookedOn", volQueries},
}

func volRoundTrip(t *testing.T, f Fixture) {
	ctx := context.Background()
	v := NewVolume(t, "asg-1", "n-fc", "dep-1")
	_, _ = v.AddTracking("TN1")
	_, _ = v.AddTracking("TN2")
	noErr(t, v.Book("dep-1"))
	noErr(t, f.Volumes.Save(ctx, v))
	got, err := f.Volumes.Get(ctx, "asg-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), v.Snapshot()) {
		t.Fatalf("round trip: %+v != %+v", got.Snapshot(), v.Snapshot())
	}
	_, err = f.Volumes.Get(ctx, "asg-9")
	wantErr(t, err, ports.ErrNotFound)
}

func volVersions(t *testing.T, f Fixture) {
	ctx := context.Background()
	v := NewVolume(t, "asg-1", "n-fc", "dep-1")
	noErr(t, f.Volumes.Save(ctx, v))
	wantErr(t, f.Volumes.Save(ctx, NewVolume(t, "asg-1", "n-fc", "dep-1")), ports.ErrConcurrentUpdate)
	a, _ := f.Volumes.Get(ctx, "asg-1")
	b, _ := f.Volumes.Get(ctx, "asg-1")
	_, _ = a.AddTracking("TN1")
	noErr(t, f.Volumes.Save(ctx, a))
	_, _ = b.AddTracking("TN2")
	wantErr(t, f.Volumes.Save(ctx, b), ports.ErrConcurrentUpdate)
	got, _ := f.Volumes.Get(ctx, "asg-1")
	if got.Version() != 2 || strings.Join(got.TrackingNumbers(), ",") != "TN1" {
		t.Fatalf("stored = %+v", got.Snapshot())
	}
}

func volQueries(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, c := range []struct{ asg, dep, tn string }{{"asg-b", "dep-1", "TN-b"}, {"asg-a", "dep-1", "TN-a"}, {"asg-c", "dep-2", "TN-c"}} {
		v := NewVolume(t, c.asg, "n-fc", c.dep)
		_, _ = v.AddTracking(c.tn)
		noErr(t, v.Book(departure.ID(c.dep)))
		noErr(t, f.Volumes.Save(ctx, v))
	}
	cancelled, _ := f.Volumes.Get(ctx, "asg-b")
	cancelled.Cancel()
	noErr(t, f.Volumes.Save(ctx, cancelled))
	got, err := f.Volumes.FindByTracking(ctx, "TN-c")
	noErr(t, err)
	if got.AssignmentID() != "asg-c" {
		t.Fatalf("found %s", got.AssignmentID())
	}
	_, err = f.Volumes.FindByTracking(ctx, "TN-none")
	wantErr(t, err, ports.ErrNotFound)
	booked, err := f.Volumes.ListBookedOn(ctx, "dep-1")
	noErr(t, err)
	if len(booked) != 1 || booked[0].AssignmentID() != "asg-a" {
		t.Fatalf("a cancelled row is not BOOKED: %d rows", len(booked))
	}
	none, err := f.Volumes.ListBookedOn(ctx, "dep-9")
	noErr(t, err)
	if none == nil || len(none) != 0 {
		t.Fatalf("no rows is an empty list: %v", none)
	}
}
