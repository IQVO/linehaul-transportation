package repocontract

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

var departureCases = []named{
	{"a saved departure round-trips and a miss is ErrNotFound", depRoundTrip},
	{"a create needs a free id and an update needs the previous version", depVersions},
	{"List filters by origin, destination, kind, state and window", depFilters},
	{"List orders by cutoff then id, limits and defaults the limit", depOrderAndLimit},
	{"List HasRoom and CutoffAfter", depRoomAndAfter},
	{"every state and its instants survive the database", depStates},
}

func ids(ds []*departure.Departure) string {
	var out []string
	for _, d := range ds {
		out = append(out, string(d.ID()))
	}
	return strings.Join(out, " ")
}

func depRoundTrip(t *testing.T, f Fixture) {
	ctx := context.Background()
	d := NewDeparture(t, "dep-1", "n-fc", departure.KindPickup, time.Hour, 5)
	noErr(t, f.Departures.Save(ctx, d))
	got, err := f.Departures.Get(ctx, "dep-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), d.Snapshot()) {
		t.Fatalf("round trip: %+v != %+v", got.Snapshot(), d.Snapshot())
	}
	_, err = f.Departures.Get(ctx, "dep-9")
	wantErr(t, err, ports.ErrNotFound)
}

func depVersions(t *testing.T, f Fixture) {
	ctx := context.Background()
	d := NewDeparture(t, "dep-1", "n-fc", departure.KindPickup, time.Hour, 5)
	noErr(t, f.Departures.Save(ctx, d))
	wantErr(t, f.Departures.Save(ctx, NewDeparture(t, "dep-1", "n-fc", departure.KindPickup, time.Hour, 5)), ports.ErrConcurrentUpdate)
	_, err := d.Book(at)
	noErr(t, err)
	noErr(t, f.Departures.Save(ctx, d))
	stale, _ := f.Departures.Get(ctx, "dep-1")
	other, _ := f.Departures.Get(ctx, "dep-1")
	_, _ = stale.Book(at)
	noErr(t, f.Departures.Save(ctx, stale))
	_, _ = other.Book(at)
	wantErr(t, f.Departures.Save(ctx, other), ports.ErrConcurrentUpdate)
	ghost := NewDeparture(t, "dep-ghost", "n-fc", departure.KindPickup, time.Hour, 5)
	_, _ = ghost.Book(at)
	wantErr(t, f.Departures.Save(ctx, ghost), ports.ErrConcurrentUpdate)
	final, _ := f.Departures.Get(ctx, "dep-1")
	if final.Booked() != 2 || final.Version() != 3 {
		t.Fatalf("final = booked %d version %d", final.Booked(), final.Version())
	}
}

func seedDepartures(t *testing.T, f Fixture) {
	ctx := context.Background()
	for _, d := range []*departure.Departure{
		NewDeparture(t, "dep-b", "n-1", departure.KindPickup, 3*time.Hour, 5),
		NewDeparture(t, "dep-a", "n-1", departure.KindPickup, 3*time.Hour, 5),
		NewDeparture(t, "dep-c", "n-2", departure.KindLinehaul, 5*time.Hour, 5),
		NewDeparture(t, "dep-d", "n-1", departure.KindAir, 8*time.Hour, 1),
	} {
		noErr(t, f.Departures.Save(ctx, d))
	}
}

func list(t *testing.T, f Fixture, filter repository.DepartureFilter) string {
	t.Helper()
	got, err := f.Departures.List(context.Background(), filter)
	noErr(t, err)
	return ids(got)
}

func depFilters(t *testing.T, f Fixture) {
	seedDepartures(t, f)
	cancel, _ := f.Departures.Get(context.Background(), "dep-c")
	_, _ = cancel.Cancel("planner", at)
	noErr(t, f.Departures.Save(context.Background(), cancel))
	cases := []struct {
		name   string
		filter repository.DepartureFilter
		want   string
	}{
		{"none", repository.DepartureFilter{}, "dep-a dep-b dep-c dep-d"},
		{"origin", repository.DepartureFilter{Origin: "n-1"}, "dep-a dep-b dep-d"},
		{"destination", repository.DepartureFilter{Destination: "n-sc"}, "dep-a dep-b dep-c dep-d"},
		{"destination miss", repository.DepartureFilter{Destination: "n-x"}, ""},
		{"kind", repository.DepartureFilter{Kind: departure.KindPickup}, "dep-a dep-b"},
		{"state", repository.DepartureFilter{State: departure.StateCancelled}, "dep-c"},
		{"from inclusive", repository.DepartureFilter{CutoffFrom: at.Add(5 * time.Hour)}, "dep-c dep-d"},
		{"to exclusive", repository.DepartureFilter{CutoffTo: at.Add(5 * time.Hour)}, "dep-a dep-b"},
		{"window", repository.DepartureFilter{CutoffFrom: at.Add(3 * time.Hour), CutoffTo: at.Add(8 * time.Hour)}, "dep-a dep-b dep-c"},
		{"combined", repository.DepartureFilter{Origin: "n-1", Kind: departure.KindPickup, State: departure.StateScheduled}, "dep-a dep-b"},
	}
	for _, c := range cases {
		if got := list(t, f, c.filter); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func depOrderAndLimit(t *testing.T, f Fixture) {
	seedDepartures(t, f)
	if got := list(t, f, repository.DepartureFilter{Limit: 3}); got != "dep-a dep-b dep-c" {
		t.Fatalf("limit 3: %q", got)
	}
	if got := list(t, f, repository.DepartureFilter{Limit: 1}); got != "dep-a" {
		t.Fatalf("limit 1: %q", got)
	}
	// Keyset: (cutoffAt, id) strictly after the cursor; dep-a and dep-b tie on the cutoff.
	cut := at.Add(3 * time.Hour)
	if got := list(t, f, repository.DepartureFilter{After: &repository.DepartureCursor{CutoffAt: cut, ID: "dep-a"}}); got != "dep-b dep-c dep-d" {
		t.Fatalf("after dep-a: %q", got)
	}
	if got := list(t, f, repository.DepartureFilter{After: &repository.DepartureCursor{CutoffAt: cut, ID: "dep-b"}, Limit: 1}); got != "dep-c" {
		t.Fatalf("after dep-b, limit 1: %q", got)
	}
	for i := 0; i < 3; i++ {
		noErr(t, f.Departures.Save(context.Background(), NewDeparture(t, "dep-x"+string(rune('0'+i)), "n-9", departure.KindPickup, time.Duration(i+1)*time.Minute, 1)))
	}
	if got, _ := f.Departures.List(context.Background(), repository.DepartureFilter{}); len(got) != 7 {
		t.Fatalf("the default limit (%d) must not cut a short list: %d", repository.DefaultListLimit, len(got))
	}
}

func depRoomAndAfter(t *testing.T, f Fixture) {
	seedDepartures(t, f)
	full, _ := f.Departures.Get(context.Background(), "dep-d")
	_, _ = full.Book(at)
	noErr(t, f.Departures.Save(context.Background(), full))
	if got := list(t, f, repository.DepartureFilter{HasRoom: true}); got != "dep-a dep-b dep-c" {
		t.Fatalf("HasRoom: %q", got)
	}
	if got := list(t, f, repository.DepartureFilter{CutoffAfter: at.Add(3 * time.Hour)}); got != "dep-c dep-d" {
		t.Fatalf("CutoffAfter is exclusive: %q", got)
	}
	if got := list(t, f, repository.DepartureFilter{CutoffAfter: at.Add(3*time.Hour - time.Second), Limit: 1}); got != "dep-a" {
		t.Fatalf("CutoffAfter just before: %q", got)
	}
}

func depStates(t *testing.T, f Fixture) {
	ctx := context.Background()
	closeIt := func(d *departure.Departure) error { return d.Close() }
	depart := func(d *departure.Departure) error { _, err := d.RecordDeparture(at.Add(time.Minute)); return err }
	arrive := func(d *departure.Departure) error { _, err := d.RecordArrival(at.Add(2 * time.Minute)); return err }
	cancel := func(d *departure.Departure) error {
		_, err := d.Cancel("NODE_SUSPENDED", at.Add(time.Minute))
		return err
	}
	paths := map[string][]func(*departure.Departure) error{
		"dep-closed": {closeIt}, "dep-departed": {closeIt, depart}, "dep-arrived": {closeIt, depart, arrive}, "dep-cancelled": {cancel},
	}
	for id, steps := range paths {
		d := NewDeparture(t, id, "n-1", departure.KindAir, time.Hour, 3)
		noErr(t, f.Departures.Save(ctx, d))
		for _, step := range steps {
			noErr(t, step(d))
			noErr(t, f.Departures.Save(ctx, d))
			got, err := f.Departures.Get(ctx, d.ID())
			noErr(t, err)
			if !reflect.DeepEqual(got.Snapshot(), d.Snapshot()) {
				t.Fatalf("%s: %+v != %+v", id, got.Snapshot(), d.Snapshot())
			}
		}
	}
}
