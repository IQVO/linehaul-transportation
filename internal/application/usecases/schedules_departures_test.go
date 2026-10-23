package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func slotIn(day string, capacity int) usecases.SlotInput {
	return usecases.SlotInput{DayOfWeek: day, CutoffLocalTime: "18:00", DepartsLocalTime: "18:30", ArrivesLocalTime: "22:45", CapacityPackages: capacity}
}

func TestDefineLaneScheduleCreatesThenReplaces(t *testing.T) {
	e := newEnv(t)
	e.lane("lane-1", "n-a", "n-b", network.ModeGround, true)
	v, created, err := e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("MONDAY", 10)}})
	e.must(err)
	if !created || v.State != "ACTIVE" || len(v.Slots) != 1 || v.Slots[0].DayOfWeek != "MONDAY" {
		t.Fatalf("created = %v view = %+v", created, v)
	}
	v, created, err = e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("friday", 20), slotIn("saturday", 5)}})
	e.must(err)
	if created || len(v.Slots) != 2 || v.Slots[0].DayOfWeek != "FRIDAY" {
		t.Fatalf("replace: created = %v view = %+v", created, v)
	}
	stored, _ := e.store.Schedules().Get(e.ctx, "lane-1")
	if stored.Version() != 2 || len(stored.Slots()) != 2 {
		t.Fatalf("stored = %+v", stored.Snapshot())
	}
	if len(e.outbox.Events()) != 0 {
		t.Fatal("a lane schedule raises no event")
	}
}

func TestDefineLaneScheduleRefusals(t *testing.T) {
	e := newEnv(t)
	e.lane("lane-1", "n-a", "n-b", network.ModeGround, false)
	ok := []usecases.SlotInput{slotIn("MONDAY", 10)}
	cases := []struct {
		name   string
		cmd    usecases.DefineLaneScheduleCommand
		target error
	}{
		{"unknown lane", usecases.DefineLaneScheduleCommand{LaneID: "lane-9", Slots: ok}, ports.ErrNotFound},
		{"bad weekday", usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("FUNDAY", 1)}}, shared.ErrValidation},
		{"no slots", usecases.DefineLaneScheduleCommand{LaneID: "lane-1"}, shared.ErrValidation},
		{"overlap", usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("MONDAY", 1), slotIn("MONDAY", 2)}}, schedule.ErrOverlappingSlots},
		{"bad times", usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{{DayOfWeek: "MONDAY", CutoffLocalTime: "19:00", DepartsLocalTime: "18:00", ArrivesLocalTime: "20:00", CapacityPackages: 1}}}, departure.ErrInvalidDepartureTimes},
	}
	before := e.store.Counts()
	for _, c := range cases {
		_, created, err := e.svc.DefineLaneSchedule(e.ctx, c.cmd)
		wantErr(t, c.name, err, c.target)
		if created {
			t.Errorf("%s: nothing was created", c.name)
		}
	}
	e.unchanged(before)
	// A closed lane may still be given a schedule (ADR 0002 A4).
	if _, _, err := e.svc.DefineLaneSchedule(e.ctx, usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: ok}); err != nil {
		t.Fatalf("closed lane: %v", err)
	}
}

func TestRetiredScheduleCannotBeRedefined(t *testing.T) {
	e := newEnv(t)
	e.lane("lane-1", "n-a", "n-b", network.ModeGround, true)
	cmd := usecases.DefineLaneScheduleCommand{LaneID: "lane-1", Slots: []usecases.SlotInput{slotIn("MONDAY", 10)}}
	_, _, err := e.svc.DefineLaneSchedule(e.ctx, cmd)
	e.must(err)
	v, err := e.svc.RetireLaneSchedule(e.ctx, "lane-1")
	e.must(err)
	if v.State != "RETIRED" {
		t.Fatalf("view = %+v", v)
	}
	_, _, err = e.svc.DefineLaneSchedule(e.ctx, cmd)
	wantErr(t, "redefine retired", err, shared.ErrIllegalTransition)
	before := e.store.Counts()
	v, err = e.svc.RetireLaneSchedule(e.ctx, "lane-1")
	e.must(err)
	if v.State != "RETIRED" {
		t.Fatalf("retiring twice returns it unchanged: %+v", v)
	}
	e.unchanged(before)
	_, err = e.svc.RetireLaneSchedule(e.ctx, "lane-9")
	wantErr(t, "unknown", err, ports.ErrNotFound)
}

func TestListDepartures(t *testing.T) {
	e := newEnv(t)
	e.departureFrom("dep-b", "n-1", departure.KindPickup, 3*time.Hour, 5)
	e.departureFrom("dep-a", "n-1", departure.KindPickup, 3*time.Hour, 5)
	e.departureFrom("dep-c", "n-2", departure.KindLinehaul, 5*time.Hour, 5)
	e.departureFrom("dep-d", "n-1", departure.KindPickup, 8*time.Hour, 5)
	ids := func(q usecases.ListDeparturesQuery) []string {
		out, err := e.svc.ListDepartures(e.ctx, q)
		e.must(err)
		var r []string
		for _, d := range out.Departures {
			r = append(r, d.DepartureID)
		}
		return r
	}
	from, to := t0.Add(3*time.Hour), t0.Add(8*time.Hour)
	cases := []struct {
		name string
		q    usecases.ListDeparturesQuery
		want string
	}{
		{"all, by cutoff then id", usecases.ListDeparturesQuery{}, "dep-a dep-b dep-c dep-d"},
		{"origin", usecases.ListDeparturesQuery{Origin: "n-1"}, "dep-a dep-b dep-d"},
		{"destination", usecases.ListDeparturesQuery{Destination: "n-sc"}, "dep-a dep-b dep-c dep-d"},
		{"from is inclusive", usecases.ListDeparturesQuery{From: &from}, "dep-a dep-b dep-c dep-d"},
		{"to is exclusive", usecases.ListDeparturesQuery{From: &from, To: &to}, "dep-a dep-b dep-c"},
		{"limit", usecases.ListDeparturesQuery{Limit: 2}, "dep-a dep-b"},
		{"no match is empty", usecases.ListDeparturesQuery{Origin: "nowhere"}, ""},
	}
	for _, c := range cases {
		got := ""
		for i, id := range ids(c.q) {
			if i > 0 {
				got += " "
			}
			got += id
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestListDeparturesValidatesItsQuery(t *testing.T) {
	e := newEnv(t)
	early, late := t0, t0.Add(time.Hour)
	for name, q := range map[string]usecases.ListDeparturesQuery{
		"negative limit": {Limit: -1}, "limit over the maximum": {Limit: 1001}, "from after to": {From: &late, To: &early},
	} {
		_, err := e.svc.ListDepartures(e.ctx, q)
		wantErr(t, name, err, shared.ErrValidation)
	}
	if _, err := e.svc.ListDepartures(e.ctx, usecases.ListDeparturesQuery{Limit: 1000, From: &early, To: &early}); err != nil {
		t.Fatalf("the maximum limit and from == to are fine: %v", err)
	}
	out, err := e.svc.ListDepartures(e.ctx, usecases.ListDeparturesQuery{})
	e.must(err)
	if out.Departures == nil {
		t.Fatal("the list is never nil")
	}
}

func TestGetDeparture(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	v, err := e.svc.GetDeparture(e.ctx, "dep-1")
	e.must(err)
	if v.DepartureID != "dep-1" || v.RemainingPackages != 5 || v.State != "SCHEDULED" {
		t.Fatalf("view = %+v", v)
	}
	_, err = e.svc.GetDeparture(e.ctx, "dep-9")
	wantErr(t, "unknown", err, ports.ErrNotFound)
}

func TestChangeDepartureCapacity(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.book("dep-1", 1)
	v, err := e.svc.ChangeDepartureCapacity(e.ctx, "dep-1", 9)
	e.must(err)
	if v.CapacityPackages != 9 || v.BookedPackages != 1 || v.RemainingPackages != 8 {
		t.Fatalf("view = %+v", v)
	}
	sameNames(t, e.eventNames(), []string{"DepartureCapacityChanged"})
	if ev := eventsOf[departure.DepartureCapacityChanged](e)[0]; ev.CapacityPackages != 9 || ev.RemainingPackages != 8 {
		t.Fatalf("event = %+v", ev)
	}
	if _, err := e.svc.ChangeDepartureCapacity(e.ctx, "dep-1", 9); err != nil || len(e.outbox.Events()) != 1 {
		t.Fatalf("the same capacity changes and raises nothing: %v", err)
	}
	before := e.store.Counts()
	_, err = e.svc.ChangeDepartureCapacity(e.ctx, "dep-1", 0)
	wantErr(t, "below booked", err, departure.ErrDepartureFull)
	_, err = e.svc.ChangeDepartureCapacity(e.ctx, "dep-9", 1)
	wantErr(t, "unknown", err, ports.ErrNotFound)
	e.unchanged(before)
}

func TestCancelDepartureWithoutBookings(t *testing.T) {
	e := newEnv(t)
	e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	v, err := e.svc.CancelDeparture(e.ctx, "dep-1", "planner decision")
	e.must(err)
	if v.State != "CANCELLED" || v.CancelReason != "planner decision" || v.CancelledAt == nil {
		t.Fatalf("view = %+v", v)
	}
	sameNames(t, e.eventNames(), []string{"DepartureCancelled"})
	before := e.store.Counts()
	again, err := e.svc.CancelDeparture(e.ctx, "dep-1", "another reason")
	e.must(err)
	if again.CancelReason != "planner decision" {
		t.Fatalf("a cancelled departure is returned unchanged: %+v", again)
	}
	e.unchanged(before)
}

func TestCancelDepartureRefusals(t *testing.T) {
	e := newEnv(t)
	d := e.departureAt("dep-1", departure.KindPickup, time.Hour, 5)
	e.must(d.Close())
	e.must(e.store.Departures().Save(e.ctx, d))
	before := e.store.Counts()
	_, err := e.svc.CancelDeparture(e.ctx, "dep-1", "late")
	wantErr(t, "closed departure", err, shared.ErrIllegalTransition)
	_, err = e.svc.CancelDeparture(e.ctx, "dep-9", "x")
	wantErr(t, "unknown", err, ports.ErrNotFound)
	e.unchanged(before)
	e.departureAt("dep-2", departure.KindPickup, time.Hour, 5)
	before = e.store.Counts()
	_, err = e.svc.CancelDeparture(e.ctx, "dep-2", " ")
	wantErr(t, "blank reason", err, shared.ErrValidation)
	e.unchanged(before)
}
