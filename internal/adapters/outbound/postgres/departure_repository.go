package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

// DepartureRepo is the Postgres ports.DepartureRepository.
type DepartureRepo struct{ pool *pgxpool.Pool }

// NewDepartureRepo constructs a DepartureRepo over pool.
func NewDepartureRepo(pool *pgxpool.Pool) *DepartureRepo { return &DepartureRepo{pool: pool} }

const departureColumns = `id, lane_id, kind, origin_node_id, destination_node_id, origin_type, destination_type,
	cutoff_at, departs_at, arrives_at, capacity_packages, booked_packages, state, cancel_reason,
	cancelled_at, departed_at, arrived_at, version`

func scanDeparture(row pgx.Row) (*departure.Departure, error) {
	var (
		s                                     departure.Snapshot
		id, kind, originType, destType, state string
		cancelReason                          *string
		cancelledAt, departedAt, arrivedAt    *time.Time
		cutoff, departs, arrives              time.Time
	)
	if err := row.Scan(&id, &s.LaneID, &kind, &s.OriginNodeID, &s.DestinationNodeID, &originType, &destType,
		&cutoff, &departs, &arrives, &s.CapacityPackages, &s.BookedPackages, &state, &cancelReason,
		&cancelledAt, &departedAt, &arrivedAt, &s.Version); err != nil {
		return nil, err
	}
	s.ID, s.Kind, s.State = departure.ID(id), departure.Kind(kind), departure.State(state)
	s.OriginType, s.DestinationType = network.NodeType(originType), network.NodeType(destType)
	s.CutoffAt, s.DepartsAt, s.ArrivesAt = cutoff.UTC(), departs.UTC(), arrives.UTC()
	s.CancelReason, s.CancelledAt = fromNullText(cancelReason), fromNull(cancelledAt)
	s.DepartedAt, s.ArrivedAt = fromNull(departedAt), fromNull(arrivedAt)
	return departure.Restore(s)
}

// Get implements ports.DepartureRepository.
func (r *DepartureRepo) Get(ctx context.Context, id departure.ID) (*departure.Departure, error) {
	d, err := scanDeparture(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+departureColumns+` FROM departures WHERE id = $1`, string(id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("departure %s: %w", id, ports.ErrNotFound)
	}
	return d, err
}

// List implements ports.DepartureRepository.
func (r *DepartureRepo) List(ctx context.Context, f repository.DepartureFilter) ([]*departure.Departure, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.Origin != "" {
		add("origin_node_id = ?", f.Origin)
	}
	if f.Destination != "" {
		add("destination_node_id = ?", f.Destination)
	}
	if f.Kind != "" {
		add("kind = ?", string(f.Kind))
	}
	if f.State != "" {
		add("state = ?", string(f.State))
	}
	if !f.CutoffFrom.IsZero() {
		add("cutoff_at >= ?", f.CutoffFrom.UTC())
	}
	if !f.CutoffTo.IsZero() {
		add("cutoff_at < ?", f.CutoffTo.UTC())
	}
	if !f.CutoffAfter.IsZero() {
		add("cutoff_at > ?", f.CutoffAfter.UTC())
	}
	if f.HasRoom {
		where = append(where, "booked_packages < capacity_packages")
	}
	limit := f.Limit
	if limit == 0 {
		limit = repository.DefaultListLimit
	}
	sql := `SELECT ` + departureColumns + ` FROM departures`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += ` ORDER BY cutoff_at, id LIMIT ` + strconv.Itoa(limit)
	rows, err := queryFor(ctx, r.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list departures: %w", err)
	}
	defer rows.Close()
	out := []*departure.Departure{}
	for rows.Next() {
		d, err := scanDeparture(rows)
		if err != nil {
			return nil, fmt.Errorf("read departure: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Save implements ports.DepartureRepository.
func (r *DepartureRepo) Save(ctx context.Context, d *departure.Departure) error {
	s := d.Snapshot()
	return inTx(ctx, r.pool, func(q querier) error {
		args := []any{string(s.ID), s.LaneID, string(s.Kind), s.OriginNodeID, s.DestinationNodeID,
			string(s.OriginType), string(s.DestinationType), s.CutoffAt.UTC(), s.DepartsAt.UTC(), s.ArrivesAt.UTC(),
			s.CapacityPackages, s.BookedPackages, string(s.State), nullText(s.CancelReason),
			nullTime(s.CancelledAt), nullTime(s.DepartedAt), nullTime(s.ArrivedAt), s.Version}
		if s.Version == 1 {
			tag, err := q.Exec(ctx, `INSERT INTO departures (`+departureColumns+`)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
				ON CONFLICT (id) DO NOTHING`, args...)
			if err != nil {
				return fmt.Errorf("insert departure %s: %w", s.ID, err)
			}
			return concurrent(tag.RowsAffected())
		}
		tag, err := q.Exec(ctx, `UPDATE departures SET capacity_packages = $2, booked_packages = $3, state = $4,
			cancel_reason = $5, cancelled_at = $6, departed_at = $7, arrived_at = $8, version = $9
			WHERE id = $1 AND version = $9 - 1`, string(s.ID), s.CapacityPackages, s.BookedPackages, string(s.State),
			nullText(s.CancelReason), nullTime(s.CancelledAt), nullTime(s.DepartedAt), nullTime(s.ArrivedAt), s.Version)
		if err != nil {
			return fmt.Errorf("update departure %s: %w", s.ID, err)
		}
		return concurrent(tag.RowsAffected())
	})
}
