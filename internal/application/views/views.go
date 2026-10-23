// Package views holds the read DTOs the use cases return and the HTTP adapter
// serialises: they carry the REST names of apis/openapi.yaml and no behaviour.
// Optional fields are omitted when absent (never null).
package views

import (
	"strings"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
)

// Departure is the REST view of a departure.
type Departure struct {
	DepartureID       string     `json:"departureId"`
	LaneID            string     `json:"laneId"`
	Kind              string     `json:"kind"`
	OriginNodeID      string     `json:"originNodeId"`
	DestinationNodeID string     `json:"destinationNodeId"`
	CutoffAt          time.Time  `json:"cutoffAt"`
	DepartsAt         time.Time  `json:"departsAt"`
	ArrivesAt         time.Time  `json:"arrivesAt"`
	CapacityPackages  int        `json:"capacityPackages"`
	BookedPackages    int        `json:"bookedPackages"`
	RemainingPackages int        `json:"remainingPackages"`
	State             string     `json:"state"`
	CancelReason      string     `json:"cancelReason,omitempty"`
	CancelledAt       *time.Time `json:"cancelledAt,omitempty"`
	DepartedAt        *time.Time `json:"departedAt,omitempty"`
	ArrivedAt         *time.Time `json:"arrivedAt,omitempty"`
}

// DepartureList is the REST view of a list of departures; the array is always present.
type DepartureList struct {
	Departures []Departure `json:"departures"`
}

// Slot is the REST view of a lane-schedule slot; DayOfWeek is MONDAY..SUNDAY.
type Slot struct {
	DayOfWeek        string `json:"dayOfWeek"`
	CutoffLocalTime  string `json:"cutoffLocalTime"`
	DepartsLocalTime string `json:"departsLocalTime"`
	ArrivesLocalTime string `json:"arrivesLocalTime"`
	CapacityPackages int    `json:"capacityPackages"`
}

// LaneSchedule is the REST view of a lane schedule.
type LaneSchedule struct {
	LaneID string `json:"laneId"`
	State  string `json:"state"`
	Slots  []Slot `json:"slots"`
}

// Load is the REST view of a load.
type Load struct {
	LoadID            string     `json:"loadId"`
	DepartureID       string     `json:"departureId"`
	DepartureKind     string     `json:"departureKind"`
	OriginNodeID      string     `json:"originNodeId"`
	DestinationNodeID string     `json:"destinationNodeId"`
	CapacityPackages  int        `json:"capacityPackages"`
	PackageCount      int        `json:"packageCount"`
	TrackingNumbers   []string   `json:"trackingNumbers"`
	State             string     `json:"state"`
	DepartedAt        *time.Time `json:"departedAt,omitempty"`
	ArrivedAt         *time.Time `json:"arrivedAt,omitempty"`
}

func optional(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// FromDeparture builds the view of a departure.
func FromDeparture(d *departure.Departure) Departure {
	s := d.Snapshot()
	return Departure{
		DepartureID: string(s.ID), LaneID: s.LaneID, Kind: string(s.Kind), OriginNodeID: s.OriginNodeID,
		DestinationNodeID: s.DestinationNodeID, CutoffAt: s.CutoffAt.UTC(), DepartsAt: s.DepartsAt.UTC(), ArrivesAt: s.ArrivesAt.UTC(),
		CapacityPackages: s.CapacityPackages, BookedPackages: s.BookedPackages, RemainingPackages: s.CapacityPackages - s.BookedPackages,
		State: string(s.State), CancelReason: s.CancelReason, CancelledAt: optional(s.CancelledAt),
		DepartedAt: optional(s.DepartedAt), ArrivedAt: optional(s.ArrivedAt),
	}
}

// FromDepartures builds the list view; no departure is an empty array.
func FromDepartures(ds []*departure.Departure) DepartureList {
	out := DepartureList{Departures: make([]Departure, 0, len(ds))}
	for _, d := range ds {
		out.Departures = append(out.Departures, FromDeparture(d))
	}
	return out
}

// FromSchedule builds the view of a lane schedule.
func FromSchedule(s *schedule.LaneSchedule) LaneSchedule {
	slots := s.Slots()
	out := LaneSchedule{LaneID: s.LaneID(), State: string(s.State()), Slots: make([]Slot, 0, len(slots))}
	for _, sl := range slots {
		out.Slots = append(out.Slots, Slot{
			DayOfWeek: strings.ToUpper(network.WeekdayName(sl.Day)), CutoffLocalTime: sl.Cutoff,
			DepartsLocalTime: sl.Departs, ArrivesLocalTime: sl.Arrives, CapacityPackages: sl.CapacityPackages,
		})
	}
	return out
}

// FromLoad builds the view of a load.
func FromLoad(l *load.Load) Load {
	s := l.Snapshot()
	return Load{
		LoadID: string(s.ID), DepartureID: string(s.DepartureID), DepartureKind: string(s.Kind), OriginNodeID: s.OriginNodeID,
		DestinationNodeID: s.DestinationNodeID, CapacityPackages: s.CapacityPackages, PackageCount: len(s.TrackingNumbers),
		TrackingNumbers: s.TrackingNumbers, State: string(s.State), DepartedAt: optional(s.DepartedAt), ArrivedAt: optional(s.ArrivedAt),
	}
}
