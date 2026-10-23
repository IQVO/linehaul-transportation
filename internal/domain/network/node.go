// Package network holds the two read models this context keeps of node-topology:
// NodeView (type, operator, zone, status, declared profile) and LaneView
// (origin, destination, mode, transit, open or closed). They are fed by events,
// never changed by a command of this context, and enforce no rule on their
// source (ADR 0003). The package also picks the default outbound lane of a node.
package network

import (
	"cmp"
	"slices"

	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// NodeType is the type of a node (model node_types).
type NodeType string

// The node types of the model.
const (
	FulfillmentCenter NodeType = "FULFILLMENT_CENTER"
	InboundCrossDock  NodeType = "INBOUND_CROSS_DOCK"
	ReturnsCenter     NodeType = "RETURNS_CENTER"
	SortationCenter   NodeType = "SORTATION_CENTER"
	DeliveryStation   NodeType = "DELIVERY_STATION"
	AirHub            NodeType = "AIR_HUB"
)

// Valid reports whether t is one of the six node types.
func (t NodeType) Valid() bool {
	switch t {
	case FulfillmentCenter, InboundCrossDock, ReturnsCenter, SortationCenter, DeliveryStation, AirHub:
		return true
	}
	return false
}

// Operator is who operates a node.
type Operator string

// The two operators.
const (
	OperatorNetwork Operator = "NETWORK"
	OperatorVendor  Operator = "VENDOR"
)

// Status is the lifecycle state of a node as announced by node-topology.
type Status string

// The node states.
const (
	StatusProposed  Status = "PROPOSED"
	StatusActive    Status = "ACTIVE"
	StatusSuspended Status = "SUSPENDED"
	StatusRetired   Status = "RETIRED"
)

// NodeView is the local copy of a node. Profile is nil until a profile is
// declared. Generation counts the activations (ADR 0002 A10).
type NodeView struct {
	ID         string
	Type       NodeType
	Operator   Operator
	Timezone   string
	Status     Status
	Generation int
	Profile    *Profile
}

// NewNodeView returns an empty PROPOSED view; the first NodeRegistered fills it.
func NewNodeView(id string) NodeView { return NodeView{ID: id, Status: StatusProposed} }

// ApplyRegistered stores what NodeRegistered announces.
func (n *NodeView) ApplyRegistered(t NodeType, op Operator, tz string) error {
	if !t.Valid() || (op != OperatorNetwork && op != OperatorVendor) {
		return shared.Invalid("node %s: type %q or operator %q is unknown", n.ID, t, op)
	}
	n.Type, n.Operator, n.Timezone = t, op, tz
	return nil
}

// ApplyProfile stores p when its revision is higher than the stored one and
// reports whether it did. An invalid profile is an error.
func (n *NodeView) ApplyProfile(p Profile) (bool, error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	if n.Profile != nil && p.Revision <= n.Profile.Revision {
		return false, nil
	}
	n.Profile = &p
	return true, nil
}

// ApplyActivated marks the node ACTIVE. A node that was not ACTIVE starts a
// new generation. A RETIRED node stays retired. It reports whether the status
// changed.
func (n *NodeView) ApplyActivated() bool {
	if n.Status == StatusActive || n.Status == StatusRetired {
		return false
	}
	n.Status = StatusActive
	n.Generation++
	return true
}

// ApplySuspended marks an ACTIVE or PROPOSED node SUSPENDED and reports whether
// the status changed (a RETIRED node stays retired).
func (n *NodeView) ApplySuspended() bool {
	if n.Status == StatusSuspended || n.Status == StatusRetired {
		return false
	}
	n.Status = StatusSuspended
	return true
}

// ApplyRetired marks the node RETIRED and reports whether the status changed.
func (n *NodeView) ApplyRetired() bool {
	if n.Status == StatusRetired {
		return false
	}
	n.Status = StatusRetired
	return true
}

// PickupEligible reports whether the node gets pickup departures: a
// vendor-operated, ACTIVE fulfillment center with a declared profile (ADR 0002 A7).
func (n NodeView) PickupEligible() bool {
	return n.Type == FulfillmentCenter && n.Operator == OperatorVendor && n.Status == StatusActive && n.Profile != nil
}

// DefaultOutboundLane picks the default outbound lane of a node (ADR 0002 A8)
// among lanes: the OPEN GROUND lanes that leave the node, ranked by destination
// type (SORTATION_CENTER first, then any known type, then unknown), lowest
// transit, lowest id. typeOf returns "" for an unknown node.
func DefaultOutboundLane(nodeID string, lanes []LaneView, typeOf func(string) NodeType) (LaneView, bool) {
	var cands []LaneView
	for _, l := range lanes {
		if l.Origin == nodeID && l.Open && l.Mode == ModeGround {
			cands = append(cands, l)
		}
	}
	if len(cands) == 0 {
		return LaneView{}, false
	}
	rank := func(l LaneView) int {
		switch typeOf(l.Destination) {
		case SortationCenter:
			return 0
		case "":
			return 2
		}
		return 1
	}
	slices.SortFunc(cands, func(a, b LaneView) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a.TransitMinutes, b.TransitMinutes), cmp.Compare(a.ID, b.ID))
	})
	return cands[0], true
}
