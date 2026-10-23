package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
)

// ScheduleRepo is the Postgres ports.LaneScheduleRepository.
type ScheduleRepo struct{ pool *pgxpool.Pool }

// NewScheduleRepo constructs a ScheduleRepo over pool.
func NewScheduleRepo(pool *pgxpool.Pool) *ScheduleRepo { return &ScheduleRepo{pool: pool} }

// slotJSON is the stored shape of one weekly slot.
type slotJSON struct {
	Day              int    `json:"day"`
	Cutoff           string `json:"cutoff"`
	Departs          string `json:"departs"`
	Arrives          string `json:"arrives"`
	CapacityPackages int    `json:"capacityPackages"`
}

func scanSchedule(row pgx.Row) (*schedule.LaneSchedule, error) {
	var (
		s     schedule.Snapshot
		state string
		raw   []byte
	)
	if err := row.Scan(&s.LaneID, &state, &raw, &s.Version); err != nil {
		return nil, err
	}
	var slots []slotJSON
	if err := json.Unmarshal(raw, &slots); err != nil {
		return nil, fmt.Errorf("decode slots of lane %s: %w", s.LaneID, err)
	}
	s.State = schedule.State(state)
	s.Slots = make([]schedule.Slot, len(slots))
	for i, v := range slots {
		s.Slots[i] = schedule.Slot{Day: time.Weekday(v.Day), Cutoff: v.Cutoff, Departs: v.Departs, Arrives: v.Arrives, CapacityPackages: v.CapacityPackages}
	}
	return schedule.Restore(s)
}

// Get implements ports.LaneScheduleRepository.
func (r *ScheduleRepo) Get(ctx context.Context, laneID string) (*schedule.LaneSchedule, error) {
	s, err := scanSchedule(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT lane_id, state, slots, version FROM lane_schedules WHERE lane_id = $1`, laneID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lane schedule %s: %w", laneID, ports.ErrNotFound)
	}
	return s, err
}

// ListActive implements ports.LaneScheduleRepository.
func (r *ScheduleRepo) ListActive(ctx context.Context) ([]*schedule.LaneSchedule, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT lane_id, state, slots, version FROM lane_schedules WHERE state = 'ACTIVE' ORDER BY lane_id`)
	if err != nil {
		return nil, fmt.Errorf("list lane schedules: %w", err)
	}
	defer rows.Close()
	out := []*schedule.LaneSchedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("read lane schedule: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Save implements ports.LaneScheduleRepository.
func (r *ScheduleRepo) Save(ctx context.Context, s *schedule.LaneSchedule) error {
	sn := s.Snapshot()
	slots := make([]slotJSON, len(sn.Slots))
	for i, v := range sn.Slots {
		slots[i] = slotJSON{Day: int(v.Day), Cutoff: v.Cutoff, Departs: v.Departs, Arrives: v.Arrives, CapacityPackages: v.CapacityPackages}
	}
	raw, err := json.Marshal(slots)
	if err != nil {
		return fmt.Errorf("encode slots of lane %s: %w", sn.LaneID, err)
	}
	return inTx(ctx, r.pool, func(q querier) error {
		if sn.Version == 1 {
			tag, err := q.Exec(ctx, `INSERT INTO lane_schedules (lane_id, state, slots, version) VALUES ($1, $2, $3, $4)
				ON CONFLICT (lane_id) DO NOTHING`, sn.LaneID, string(sn.State), raw, sn.Version)
			if err != nil {
				return fmt.Errorf("insert lane schedule %s: %w", sn.LaneID, err)
			}
			return concurrent(tag.RowsAffected())
		}
		tag, err := q.Exec(ctx, `UPDATE lane_schedules SET state = $2, slots = $3, version = $4
			WHERE lane_id = $1 AND version = $4 - 1`, sn.LaneID, string(sn.State), raw, sn.Version)
		if err != nil {
			return fmt.Errorf("update lane schedule %s: %w", sn.LaneID, err)
		}
		return concurrent(tag.RowsAffected())
	})
}
