package kafka

import (
	"context"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// Consumed CloudEvents types, byte-identical to the producers' AsyncAPI
// documents (restated here, never imported).
const (
	TypeNodeRegistered  = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	TypeProfileDeclared = "com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared"
	TypeNodeActivated   = "com.retailnetwork.logistics.node-topology.node.NodeActivated"
	TypeNodeSuspended   = "com.retailnetwork.logistics.node-topology.node.NodeSuspended"
	TypeNodeRetired     = "com.retailnetwork.logistics.node-topology.node.NodeRetired"
	TypeLaneOpened      = "com.retailnetwork.logistics.node-topology.lane.LaneOpened"
	TypeLaneClosed      = "com.retailnetwork.logistics.node-topology.lane.LaneClosed"
	TypeReleased        = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode"
	TypeReassigned      = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReassigned"
	TypeLocked          = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked"
	TypeCancelled       = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelled"
	TypeLabelIssued     = "com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued"
	TypeLabelVoided     = "com.retailnetwork.logistics.shipping-and-tracking.label.LabelVoided"
)

// Tolerant payload mirrors: only the fields this service relies on.
type (
	nodeRegisteredData struct {
		NodeID   string `json:"nodeId"`
		NodeType string `json:"nodeType"`
		Operator string `json:"operator"`
		Timezone string `json:"timezone"`
	}
	profileDeclaredData struct {
		NodeID          string               `json:"nodeId"`
		ProfileRevision int                  `json:"profileRevision"`
		Timezone        string               `json:"timezone"`
		OperatingHours  map[string]hoursData `json:"operatingHoursByDay"`
		Holidays        []string             `json:"holidays"`
		Cutoffs         []cutoffData         `json:"cutoffs"`
		ThroughputCap   capData              `json:"throughputCap"`
	}
	hoursData struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	cutoffData struct {
		LocalTime string   `json:"localTime"`
		Days      []string `json:"days"`
		MaxOrders int      `json:"maxOrders"`
	}
	capData struct {
		Value    int    `json:"value"`
		TimeUnit string `json:"timeUnit"`
	}
	nodeData struct {
		NodeID string `json:"nodeId"`
	}
	laneOpenedData struct {
		LaneID         string `json:"laneId"`
		Origin         string `json:"originNodeId"`
		Destination    string `json:"destinationNodeId"`
		Mode           string `json:"mode"`
		TransitMinutes int    `json:"nominalTransitMinutes"`
	}
	laneData struct {
		LaneID string `json:"laneId"`
	}
	releasedData struct {
		AssignmentID string `json:"assignmentId"`
		ReleaseNo    int    `json:"releaseNo"`
		OrderID      string `json:"orderId"`
		GroupNo      int    `json:"groupNo"`
		NodeID       string `json:"nodeId"`
		DepartureID  string `json:"pickupDepartureId"`
	}
	reassignedData struct {
		AssignmentID string `json:"assignmentId"`
		ToNodeID     string `json:"toNodeId"`
	}
	lockedData struct {
		AssignmentID string `json:"assignmentId"`
		OrderID      string `json:"orderId"`
		GroupNo      int    `json:"groupNo"`
		NodeID       string `json:"nodeId"`
		DepartureID  string `json:"pickupDepartureId"`
	}
	assignmentData struct {
		AssignmentID string `json:"assignmentId"`
	}
	labelIssuedData struct {
		AssignmentID   string `json:"assignmentId"`
		TrackingNumber string `json:"trackingNumber"`
	}
	labelVoidedData struct {
		TrackingNumber string `json:"trackingNumber"`
	}
)

// handle decodes e's data into a T and applies it with fn, under the CloudEvents id.
func handle[T any](fn func(ctx context.Context, eventID string, d T) error) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[T](e)
		if err != nil {
			return err
		}
		return fn(ctx, e.ID(), d)
	}
}

// NodeTopologyHandlers maps node-topology's consumed types to their use cases.
func NodeTopologyHandlers(s *usecases.Service) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeNodeRegistered: handle(func(ctx context.Context, id string, d nodeRegisteredData) error {
			return s.HandleNodeRegistered(ctx, id, usecases.NodeRegisteredInput{NodeID: d.NodeID, NodeType: d.NodeType, Operator: d.Operator, Timezone: d.Timezone})
		}),
		TypeProfileDeclared: handle(func(ctx context.Context, id string, d profileDeclaredData) error {
			return s.HandleProfileDeclared(ctx, id, usecases.ProfileDeclaredInput{NodeID: d.NodeID, Profile: d.profile()})
		}),
		TypeNodeActivated: handle(func(ctx context.Context, id string, d nodeData) error {
			return s.HandleNodeActivated(ctx, id, usecases.NodeInput{NodeID: d.NodeID})
		}),
		TypeNodeSuspended: handle(func(ctx context.Context, id string, d nodeData) error {
			return s.HandleNodeSuspended(ctx, id, usecases.NodeInput{NodeID: d.NodeID})
		}),
		TypeNodeRetired: handle(func(ctx context.Context, id string, d nodeData) error {
			return s.HandleNodeRetired(ctx, id, usecases.NodeInput{NodeID: d.NodeID})
		}),
		TypeLaneOpened: handle(func(ctx context.Context, id string, d laneOpenedData) error {
			return s.HandleLaneOpened(ctx, id, usecases.LaneOpenedInput{LaneID: d.LaneID, OriginNodeID: d.Origin, DestNodeID: d.Destination, Mode: d.Mode, TransitMinutes: d.TransitMinutes})
		}),
		TypeLaneClosed: handle(func(ctx context.Context, id string, d laneData) error {
			return s.HandleLaneClosed(ctx, id, usecases.LaneInput{LaneID: d.LaneID})
		}),
	}
}

func (d profileDeclaredData) profile() network.Profile {
	p := network.Profile{
		Revision: d.ProfileRevision, Timezone: d.Timezone, Hours: map[string]network.Hours{}, Holidays: d.Holidays,
		Cap: network.ThroughputCap{Value: d.ThroughputCap.Value, TimeUnit: d.ThroughputCap.TimeUnit},
	}
	for day, h := range d.OperatingHours {
		p.Hours[day] = network.Hours{Start: h.Start, End: h.End}
	}
	for _, c := range d.Cutoffs {
		p.Cutoffs = append(p.Cutoffs, network.Cutoff{LocalTime: c.LocalTime, Days: c.Days, MaxOrders: c.MaxOrders})
	}
	return p
}

// OrderSourcingHandlers maps order-sourcing's consumed types to their use cases.
func OrderSourcingHandlers(s *usecases.Service) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeReleased: handle(func(ctx context.Context, id string, d releasedData) error {
			return s.HandleReleased(ctx, id, usecases.ReleasedInput{AssignmentID: d.AssignmentID, ReleaseNo: d.ReleaseNo,
				Facts: volume.Facts{OrderID: d.OrderID, GroupNo: d.GroupNo, NodeID: d.NodeID, DepartureID: departure.ID(d.DepartureID)}})
		}),
		TypeReassigned: handle(func(ctx context.Context, id string, d reassignedData) error {
			return s.HandleReassigned(ctx, id, usecases.ReassignedInput{AssignmentID: d.AssignmentID, ToNodeID: d.ToNodeID})
		}),
		TypeLocked: handle(func(ctx context.Context, id string, d lockedData) error {
			return s.HandleLocked(ctx, id, usecases.LockedInput{AssignmentID: d.AssignmentID,
				Facts: volume.Facts{OrderID: d.OrderID, GroupNo: d.GroupNo, NodeID: d.NodeID, DepartureID: departure.ID(d.DepartureID)}})
		}),
		TypeCancelled: handle(func(ctx context.Context, id string, d assignmentData) error {
			return s.HandleCancelled(ctx, id, usecases.AssignmentInput{AssignmentID: d.AssignmentID})
		}),
	}
}

// ShippingHandlers maps shipping-and-tracking's consumed types to their use cases.
func ShippingHandlers(s *usecases.Service) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeLabelIssued: handle(func(ctx context.Context, id string, d labelIssuedData) error {
			return s.HandleLabelIssued(ctx, id, usecases.LabelIssuedInput{AssignmentID: d.AssignmentID, TrackingNumber: d.TrackingNumber})
		}),
		TypeLabelVoided: handle(func(ctx context.Context, id string, d labelVoidedData) error {
			return s.HandleLabelVoided(ctx, id, usecases.LabelVoidedInput{TrackingNumber: d.TrackingNumber})
		}),
	}
}

// NewConsumers builds the three consumers, each under its own consumer group
// (from NODE_TOPOLOGY_CONSUMER_GROUP, ORDER_SOURCING_CONSUMER_GROUP and
// SHIPPING_AND_TRACKING_CONSUMER_GROUP at the composition root, never a
// literal). Constructing them does not dial.
func NewConsumers(brokers []string, groups Groups, s *usecases.Service, logger *slog.Logger) []*Consumer {
	logger = defaultLogger(logger)
	mk := func(name, topic, group string, h map[string]HandlerFunc) *Consumer {
		c := newConsumer(name, brokers, topic, group, h, logger)
		c.DeadLetterInvalid = true
		return c
	}
	return []*Consumer{
		mk("node-topology consumer", NodeTopologyTopic, groups.NodeTopology, NodeTopologyHandlers(s)),
		mk("order-sourcing consumer", OrderSourcingTopic, groups.OrderSourcing, OrderSourcingHandlers(s)),
		mk("shipping-and-tracking consumer", ShippingTopic, groups.Shipping, ShippingHandlers(s)),
	}
}

// Groups are the consumer group ids of the three consumers.
type Groups struct {
	NodeTopology, OrderSourcing, Shipping string
}
