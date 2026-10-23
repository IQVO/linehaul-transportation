package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// NewDeparture builds a SCHEDULED departure whose cutoff is `in` after the contract instant.
func NewDeparture(t *testing.T, id, origin string, kind departure.Kind, in time.Duration, capacity int) *departure.Departure {
	t.Helper()
	d, _, err := departure.Schedule(departure.Spec{
		ID: departure.ID(id), LaneID: "lane-1", Kind: kind, OriginNodeID: origin, DestinationNodeID: "n-sc",
		OriginType: network.FulfillmentCenter, DestinationType: network.SortationCenter,
		CutoffAt: at.Add(in), DepartsAt: at.Add(in), ArrivesAt: at.Add(in + 2*time.Hour), CapacityPackages: capacity,
	}, true, at)
	noErr(t, err)
	return d
}

// NewSchedule builds an ACTIVE lane schedule with two slots.
func NewSchedule(t *testing.T, laneID string) *schedule.LaneSchedule {
	t.Helper()
	s, err := schedule.Define(laneID, []schedule.Slot{
		{Day: time.Monday, Cutoff: "18:00", Departs: "18:30", Arrives: "22:45", CapacityPackages: 10},
		{Day: time.Friday, Cutoff: "08:00", Departs: "09:00", Arrives: "12:00", CapacityPackages: 20},
	})
	noErr(t, err)
	return s
}

// NewLoad opens a load on d.
func NewLoad(t *testing.T, id string, d *departure.Departure) *load.Load {
	t.Helper()
	l, err := load.Open(load.ID(id), d, false)
	noErr(t, err)
	return l
}

// NewVolume builds a RELEASED expected volume.
func NewVolume(t *testing.T, assignment, node, dep string) *volume.ExpectedVolume {
	t.Helper()
	v, err := volume.Unknown(assignment)
	noErr(t, err)
	_, err = v.Release(volume.Facts{OrderID: "ord-1", GroupNo: 1, NodeID: node, DepartureID: departure.ID(dep)}, 1)
	noErr(t, err)
	return v
}

// NewNodeView builds a vendor fulfillment center that is ACTIVE with a profile.
func NewNodeView(id string) network.NodeView {
	p := network.Profile{
		Revision: 3, Timezone: "America/Sao_Paulo",
		Hours:    map[string]network.Hours{"monday": {Start: "06:00", End: "22:00"}, "saturday": {Start: "08:00", End: "14:00"}},
		Holidays: []string{"2026-12-25"},
		Cutoffs:  []network.Cutoff{{LocalTime: "11:00"}, {LocalTime: "18:00", Days: []string{"monday", "friday"}, MaxOrders: 50}},
		Cap:      network.ThroughputCap{Value: 400, TimeUnit: network.CapHour},
	}
	return network.NodeView{
		ID: id, Type: network.FulfillmentCenter, Operator: network.OperatorVendor, Timezone: "America/Sao_Paulo",
		Status: network.StatusActive, Generation: 2, Profile: &p,
	}
}
