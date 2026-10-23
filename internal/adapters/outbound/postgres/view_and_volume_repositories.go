package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// NodeViewRepo is the Postgres ports.NodeViewRepository.
type NodeViewRepo struct{ pool *pgxpool.Pool }

// NewNodeViewRepo constructs a NodeViewRepo over pool.
func NewNodeViewRepo(pool *pgxpool.Pool) *NodeViewRepo { return &NodeViewRepo{pool: pool} }

const nodeColumns = `id, type, operator, timezone, status, generation, profile`

func scanNode(row pgx.Row) (network.NodeView, error) {
	var (
		n                     network.NodeView
		typ, operator, status string
		raw                   []byte
	)
	if err := row.Scan(&n.ID, &typ, &operator, &n.Timezone, &status, &n.Generation, &raw); err != nil {
		return n, err
	}
	n.Type, n.Operator, n.Status = network.NodeType(typ), network.Operator(operator), network.Status(status)
	if raw != nil {
		var p network.Profile
		if err := json.Unmarshal(raw, &p); err != nil {
			return n, fmt.Errorf("decode profile of node %s: %w", n.ID, err)
		}
		n.Profile = &p
	}
	return n, nil
}

// Get implements ports.NodeViewRepository.
func (r *NodeViewRepo) Get(ctx context.Context, id string) (network.NodeView, error) {
	n, err := scanNode(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+nodeColumns+` FROM node_views WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return network.NodeView{}, fmt.Errorf("node %s: %w", id, ports.ErrNotFound)
	}
	return n, err
}

// ListPickupEligible implements ports.NodeViewRepository.
func (r *NodeViewRepo) ListPickupEligible(ctx context.Context) ([]network.NodeView, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+nodeColumns+` FROM node_views
		WHERE type = $1 AND operator = $2 AND status = $3 AND profile IS NOT NULL ORDER BY id`,
		string(network.FulfillmentCenter), string(network.OperatorVendor), string(network.StatusActive))
	if err != nil {
		return nil, fmt.Errorf("list pickup-eligible nodes: %w", err)
	}
	defer rows.Close()
	out := []network.NodeView{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("read node: %w", err)
		}
		if n.PickupEligible() {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// Save implements ports.NodeViewRepository.
func (r *NodeViewRepo) Save(ctx context.Context, n network.NodeView) error {
	var raw []byte
	if n.Profile != nil {
		var err error
		if raw, err = json.Marshal(n.Profile); err != nil {
			return fmt.Errorf("encode profile of node %s: %w", n.ID, err)
		}
	}
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO node_views (`+nodeColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET type = EXCLUDED.type, operator = EXCLUDED.operator, timezone = EXCLUDED.timezone,
		status = EXCLUDED.status, generation = EXCLUDED.generation, profile = EXCLUDED.profile`,
		n.ID, string(n.Type), string(n.Operator), n.Timezone, string(n.Status), n.Generation, raw)
	if err != nil {
		return fmt.Errorf("save node %s: %w", n.ID, err)
	}
	return nil
}

// LaneViewRepo is the Postgres ports.LaneViewRepository.
type LaneViewRepo struct{ pool *pgxpool.Pool }

// NewLaneViewRepo constructs a LaneViewRepo over pool.
func NewLaneViewRepo(pool *pgxpool.Pool) *LaneViewRepo { return &LaneViewRepo{pool: pool} }

const laneColumns = `id, origin, destination, mode, transit_minutes, open`

func scanLane(row pgx.Row) (network.LaneView, error) {
	var (
		l    network.LaneView
		mode string
	)
	err := row.Scan(&l.ID, &l.Origin, &l.Destination, &mode, &l.TransitMinutes, &l.Open)
	l.Mode = network.Mode(mode)
	return l, err
}

// Get implements ports.LaneViewRepository.
func (r *LaneViewRepo) Get(ctx context.Context, id string) (network.LaneView, error) {
	l, err := scanLane(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+laneColumns+` FROM lane_views WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return network.LaneView{}, fmt.Errorf("lane %s: %w", id, ports.ErrNotFound)
	}
	return l, err
}

func (r *LaneViewRepo) list(ctx context.Context, column, nodeID string) ([]network.LaneView, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+laneColumns+` FROM lane_views WHERE `+column+` = $1 ORDER BY id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list lanes of node %s: %w", nodeID, err)
	}
	defer rows.Close()
	out := []network.LaneView{}
	for rows.Next() {
		l, err := scanLane(rows)
		if err != nil {
			return nil, fmt.Errorf("read lane: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListByOrigin implements ports.LaneViewRepository.
func (r *LaneViewRepo) ListByOrigin(ctx context.Context, id string) ([]network.LaneView, error) {
	return r.list(ctx, "origin", id)
}

// ListByDestination implements ports.LaneViewRepository.
func (r *LaneViewRepo) ListByDestination(ctx context.Context, id string) ([]network.LaneView, error) {
	return r.list(ctx, "destination", id)
}

// Save implements ports.LaneViewRepository.
func (r *LaneViewRepo) Save(ctx context.Context, l network.LaneView) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO lane_views (`+laneColumns+`) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET origin = EXCLUDED.origin, destination = EXCLUDED.destination, mode = EXCLUDED.mode,
		transit_minutes = EXCLUDED.transit_minutes, open = EXCLUDED.open`,
		l.ID, l.Origin, l.Destination, string(l.Mode), l.TransitMinutes, l.Open)
	if err != nil {
		return fmt.Errorf("save lane %s: %w", l.ID, err)
	}
	return nil
}

// VolumeRepo is the Postgres ports.VolumeRepository.
type VolumeRepo struct{ pool *pgxpool.Pool }

// NewVolumeRepo constructs a VolumeRepo over pool.
func NewVolumeRepo(pool *pgxpool.Pool) *VolumeRepo { return &VolumeRepo{pool: pool} }

const volumeColumns = `assignment_id, order_id, group_no, node_id, departure_id, release_no, booked_on, state, tracking_numbers, version`

func scanVolume(row pgx.Row) (*volume.ExpectedVolume, error) {
	var (
		s            volume.Snapshot
		depID, state string
		bookedOn     *string
	)
	if err := row.Scan(&s.AssignmentID, &s.OrderID, &s.GroupNo, &s.NodeID, &depID, &s.ReleaseNo, &bookedOn, &state,
		&s.TrackingNumbers, &s.Version); err != nil {
		return nil, err
	}
	s.DepartureID, s.BookedOn, s.State = departure.ID(depID), departure.ID(fromNullText(bookedOn)), volume.State(state)
	return volume.Restore(s)
}

func (r *VolumeRepo) one(ctx context.Context, what, where string, arg any) (*volume.ExpectedVolume, error) {
	v, err := scanVolume(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+volumeColumns+` FROM expected_volumes WHERE `+where+` ORDER BY assignment_id LIMIT 1`, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("expected volume of %s: %w", what, ports.ErrNotFound)
	}
	return v, err
}

// Get implements ports.VolumeRepository.
func (r *VolumeRepo) Get(ctx context.Context, assignmentID string) (*volume.ExpectedVolume, error) {
	return r.one(ctx, assignmentID, "assignment_id = $1", assignmentID)
}

// FindByTracking implements ports.VolumeRepository.
func (r *VolumeRepo) FindByTracking(ctx context.Context, trackingNumber string) (*volume.ExpectedVolume, error) {
	return r.one(ctx, "tracking number "+trackingNumber, "tracking_numbers @> ARRAY[$1]::text[]", trackingNumber)
}

// ListBookedOn implements ports.VolumeRepository.
func (r *VolumeRepo) ListBookedOn(ctx context.Context, id departure.ID) ([]*volume.ExpectedVolume, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+volumeColumns+` FROM expected_volumes
		WHERE booked_on = $1 AND state = $2 ORDER BY assignment_id`, string(id), string(volume.StateBooked))
	if err != nil {
		return nil, fmt.Errorf("list volumes booked on %s: %w", id, err)
	}
	defer rows.Close()
	out := []*volume.ExpectedVolume{}
	for rows.Next() {
		v, err := scanVolume(rows)
		if err != nil {
			return nil, fmt.Errorf("read expected volume: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Save implements ports.VolumeRepository.
func (r *VolumeRepo) Save(ctx context.Context, v *volume.ExpectedVolume) error {
	s := v.Snapshot()
	tns := s.TrackingNumbers
	if tns == nil {
		tns = []string{}
	}
	return inTx(ctx, r.pool, func(q querier) error {
		args := []any{s.AssignmentID, s.OrderID, s.GroupNo, s.NodeID, string(s.DepartureID), s.ReleaseNo,
			nullText(string(s.BookedOn)), string(s.State), tns, s.Version}
		if s.Version == 1 {
			tag, err := q.Exec(ctx, `INSERT INTO expected_volumes (`+volumeColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT (assignment_id) DO NOTHING`, args...)
			if err != nil {
				return fmt.Errorf("insert expected volume %s: %w", s.AssignmentID, err)
			}
			return concurrent(tag.RowsAffected())
		}
		tag, err := q.Exec(ctx, `UPDATE expected_volumes SET order_id = $2, group_no = $3, node_id = $4, departure_id = $5,
			release_no = $6, booked_on = $7, state = $8, tracking_numbers = $9, version = $10
			WHERE assignment_id = $1 AND version = $10 - 1`, args...)
		if err != nil {
			return fmt.Errorf("update expected volume %s: %w", s.AssignmentID, err)
		}
		return concurrent(tag.RowsAffected())
	})
}
