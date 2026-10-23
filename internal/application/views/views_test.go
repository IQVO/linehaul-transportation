package views

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func dep(t *testing.T) *departure.Departure {
	t.Helper()
	d, _, err := departure.Schedule(departure.Spec{
		ID: "dep-1", LaneID: "lane-1", Kind: departure.KindPickup, OriginNodeID: "n-a", DestinationNodeID: "n-b",
		OriginType: network.FulfillmentCenter, DestinationType: network.SortationCenter,
		CutoffAt: t0.Add(time.Hour), DepartsAt: t0.Add(time.Hour), ArrivesAt: t0.Add(3 * time.Hour), CapacityPackages: 5,
	}, true, t0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDepartureViewOmitsAbsentFields(t *testing.T) {
	d := dep(t)
	_, _ = d.Book(t0)
	raw, _ := json.Marshal(FromDeparture(d))
	got := string(raw)
	for _, want := range []string{`"departureId":"dep-1"`, `"kind":"PICKUP"`, `"capacityPackages":5`, `"bookedPackages":1`, `"remainingPackages":4`, `"state":"SCHEDULED"`, `"cutoffAt":"2026-10-23T13:00:00Z"`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing from %s", want, got)
		}
	}
	for _, absent := range []string{"cancelReason", "cancelledAt", "departedAt", "arrivedAt", "null"} {
		if strings.Contains(got, absent) {
			t.Errorf("%s must be omitted: %s", absent, got)
		}
	}
}

func TestDepartureViewCarriesLifecycleInstants(t *testing.T) {
	d := dep(t)
	_, _ = d.Cancel("planner", t0)
	v := FromDeparture(d)
	if v.State != "CANCELLED" || v.CancelReason != "planner" || v.CancelledAt == nil || !v.CancelledAt.Equal(t0) {
		t.Fatalf("view = %+v", v)
	}
	d = dep(t)
	_ = d.Close()
	_, _ = d.RecordDeparture(t0)
	_, _ = d.RecordArrival(t0.Add(time.Hour))
	v = FromDeparture(d)
	if v.DepartedAt == nil || v.ArrivedAt == nil || !v.ArrivedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("view = %+v", v)
	}
}

func TestDepartureListIsNeverNull(t *testing.T) {
	raw, _ := json.Marshal(FromDepartures(nil))
	if string(raw) != `{"departures":[]}` {
		t.Fatalf("empty list = %s", raw)
	}
	if got := FromDepartures([]*departure.Departure{dep(t), dep(t)}); len(got.Departures) != 2 {
		t.Fatalf("list = %+v", got)
	}
}

func TestScheduleViewUsesUpperCaseWeekdays(t *testing.T) {
	s, err := schedule.Define("lane-1", []schedule.Slot{{Day: time.Monday, Cutoff: "18:00", Departs: "18:30", Arrives: "22:45", CapacityPackages: 9}})
	if err != nil {
		t.Fatal(err)
	}
	v := FromSchedule(s)
	if v.LaneID != "lane-1" || v.State != "ACTIVE" || len(v.Slots) != 1 || v.Slots[0] != (Slot{"MONDAY", "18:00", "18:30", "22:45", 9}) {
		t.Fatalf("view = %+v", v)
	}
}

func TestLoadViewKeepsAnEmptyArray(t *testing.T) {
	l, err := load.Open("load-1", dep(t), false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(FromLoad(l))
	got := string(raw)
	if !strings.Contains(got, `"trackingNumbers":[]`) || !strings.Contains(got, `"departureKind":"PICKUP"`) || !strings.Contains(got, `"state":"OPEN"`) ||
		strings.Contains(got, "departedAt") || strings.Contains(got, "arrivedAt") {
		t.Fatalf("view = %s", got)
	}
	_, _ = l.AddPackage("TN1", false, departure.StateScheduled)
	_, _ = l.Close(departure.StateScheduled, t0)
	_, _ = l.Dispatch(t0)
	v := FromLoad(l)
	if v.PackageCount != 1 || v.DepartedAt == nil || v.ArrivedAt != nil || v.CapacityPackages != 5 || v.OriginNodeID != "n-a" || v.DestinationNodeID != "n-b" {
		t.Fatalf("view = %+v", v)
	}
}
