package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

// LoadRepo is the Postgres ports.LoadRepository.
type LoadRepo struct{ pool *pgxpool.Pool }

// NewLoadRepo constructs a LoadRepo over pool.
func NewLoadRepo(pool *pgxpool.Pool) *LoadRepo { return &LoadRepo{pool: pool} }

const loadColumns = `id, departure_id, kind, origin_node_id, destination_node_id, origin_type, destination_type,
	capacity_packages, state, departed_at, arrived_at, version`

// Names of the constraints a Save can violate.
const (
	loadDepartureKey      = "loads_departure_id_key"
	loadActiveTrackingKey = "load_packages_active_tracking_key"
)

func scanLoad(row pgx.Row) (load.Snapshot, error) {
	var (
		s                                         load.Snapshot
		id, depID, kind, originType, destType, st string
		departedAt, arrivedAt                     *time.Time
	)
	if err := row.Scan(&id, &depID, &kind, &s.OriginNodeID, &s.DestinationNodeID, &originType, &destType,
		&s.CapacityPackages, &st, &departedAt, &arrivedAt, &s.Version); err != nil {
		return s, err
	}
	s.ID, s.DepartureID, s.Kind, s.State = load.ID(id), departure.ID(depID), departure.Kind(kind), load.State(st)
	s.OriginType, s.DestinationType = network.NodeType(originType), network.NodeType(destType)
	s.DepartedAt, s.ArrivedAt = fromNull(departedAt), fromNull(arrivedAt)
	return s, nil
}

// fetch loads one row by the given where clause and its tracking numbers.
func (r *LoadRepo) fetch(ctx context.Context, what, where string, arg any) (*load.Load, error) {
	q := queryFor(ctx, r.pool)
	s, err := scanLoad(q.QueryRow(ctx, `SELECT `+loadColumns+` FROM loads WHERE `+where, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("load of %s: %w", what, ports.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT tracking_number FROM load_packages WHERE load_id = $1 ORDER BY position`, string(s.ID))
	if err != nil {
		return nil, fmt.Errorf("read packages of load %s: %w", s.ID, err)
	}
	defer rows.Close()
	s.TrackingNumbers = []string{}
	for rows.Next() {
		var tn string
		if err := rows.Scan(&tn); err != nil {
			return nil, fmt.Errorf("read package of load %s: %w", s.ID, err)
		}
		s.TrackingNumbers = append(s.TrackingNumbers, tn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return load.Restore(s)
}

// Get implements ports.LoadRepository.
func (r *LoadRepo) Get(ctx context.Context, id load.ID) (*load.Load, error) {
	return r.fetch(ctx, string(id), "id = $1", string(id))
}

// GetByDeparture implements ports.LoadRepository.
func (r *LoadRepo) GetByDeparture(ctx context.Context, id departure.ID) (*load.Load, error) {
	return r.fetch(ctx, "departure "+string(id), "departure_id = $1", string(id))
}

// FindActiveByTracking implements ports.LoadRepository.
func (r *LoadRepo) FindActiveByTracking(ctx context.Context, trackingNumber string) (load.ID, error) {
	var id string
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT load_id FROM load_packages WHERE tracking_number = $1 AND active`, trackingNumber).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("tracking number %s on an active load: %w", trackingNumber, ports.ErrNotFound)
	}
	return load.ID(id), err
}

// Save implements ports.LoadRepository.
func (r *LoadRepo) Save(ctx context.Context, l *load.Load) error {
	s := l.Snapshot()
	return inTx(ctx, r.pool, func(q querier) error {
		if err := r.saveRow(ctx, q, s); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM load_packages WHERE load_id = $1`, string(s.ID)); err != nil {
			return fmt.Errorf("replace packages of load %s: %w", s.ID, err)
		}
		_, err := q.Exec(ctx, `INSERT INTO load_packages (load_id, position, tracking_number, active)
			SELECT $1, t.ord - 1, t.tn, $3 FROM unnest($2::text[]) WITH ORDINALITY AS t(tn, ord)`,
			string(s.ID), s.TrackingNumbers, s.State == load.StateOpen || s.State == load.StateClosed)
		if violatedConstraint(err) == loadActiveTrackingKey {
			return fmt.Errorf("load %s: %w", s.ID, load.ErrPackageAlreadyLoaded)
		}
		if err != nil {
			return fmt.Errorf("insert packages of load %s: %w", s.ID, err)
		}
		return nil
	})
}

func (r *LoadRepo) saveRow(ctx context.Context, q querier, s load.Snapshot) error {
	args := []any{string(s.ID), string(s.DepartureID), string(s.Kind), s.OriginNodeID, s.DestinationNodeID,
		string(s.OriginType), string(s.DestinationType), s.CapacityPackages, string(s.State),
		nullTime(s.DepartedAt), nullTime(s.ArrivedAt), s.Version}
	if s.Version == 1 {
		tag, err := q.Exec(ctx, `INSERT INTO loads (`+loadColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (id) DO NOTHING`, args...)
		if violatedConstraint(err) == loadDepartureKey {
			return fmt.Errorf("departure %s: %w", s.DepartureID, load.ErrLoadAlreadyExists)
		}
		if err != nil {
			return fmt.Errorf("insert load %s: %w", s.ID, err)
		}
		return concurrent(tag.RowsAffected())
	}
	tag, err := q.Exec(ctx, `UPDATE loads SET state = $2, departed_at = $3, arrived_at = $4, version = $5
		WHERE id = $1 AND version = $5 - 1`, string(s.ID), string(s.State), nullTime(s.DepartedAt), nullTime(s.ArrivedAt), s.Version)
	if err != nil {
		return fmt.Errorf("update load %s: %w", s.ID, err)
	}
	return concurrent(tag.RowsAffected())
}
