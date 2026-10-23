// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so
// a loaded aggregate never aliases stored state, a corrupt row can not become a
// live aggregate and the doubles keep the persistence contract of the real
// adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate, the unique indexes raise the domain's
// ErrPackageAlreadyLoaded and ErrLoadAlreadyExists.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store
// for the whole unit and restores the state taken at the start when the unit
// fails or panics, outbox and inbox included.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu   sync.Mutex
	data state
}

// state is everything a unit of work can change. Stored values are replaced,
// never edited in place, so copying the maps is enough.
type state struct {
	departures map[departure.ID]departure.Snapshot
	schedules  map[string]schedule.Snapshot
	loads      map[load.ID]load.Snapshot
	nodes      map[string]network.NodeView
	lanes      map[string]network.LaneView
	volumes    map[string]volume.Snapshot
	processed  map[string]time.Time
	outbox     []shared.Event
}

func newState() state {
	return state{
		departures: map[departure.ID]departure.Snapshot{}, schedules: map[string]schedule.Snapshot{},
		loads: map[load.ID]load.Snapshot{}, nodes: map[string]network.NodeView{}, lanes: map[string]network.LaneView{},
		volumes: map[string]volume.Snapshot{}, processed: map[string]time.Time{},
	}
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{data: newState()} }

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) capture() state {
	d := s.data
	return state{
		departures: cloneMap(d.departures), schedules: cloneMap(d.schedules), loads: cloneMap(d.loads),
		nodes: cloneMap(d.nodes), lanes: cloneMap(d.lanes), volumes: cloneMap(d.volumes), processed: cloneMap(d.processed),
		outbox: append([]shared.Event(nil), d.outbox...),
	}
}

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Departures returns the DepartureRepository double.
func (s *Store) Departures() *DepartureRepository { return &DepartureRepository{s} }

// Schedules returns the LaneScheduleRepository double.
func (s *Store) Schedules() *LaneScheduleRepository { return &LaneScheduleRepository{s} }

// Loads returns the LoadRepository double.
func (s *Store) Loads() *LoadRepository { return &LoadRepository{s} }

// Nodes returns the NodeViewRepository double.
func (s *Store) Nodes() *NodeViewRepository { return &NodeViewRepository{s} }

// Lanes returns the LaneViewRepository double.
func (s *Store) Lanes() *LaneViewRepository { return &LaneViewRepository{s} }

// Volumes returns the VolumeRepository double.
func (s *Store) Volumes() *VolumeRepository { return &VolumeRepository{s} }

// Inbox returns the ProcessedEvents double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Counts is how many rows each table of the store holds.
type Counts struct {
	Departures, Schedules, Loads, Nodes, Lanes, Volumes, Processed, OutboxEvents int
	// VersionSum is the sum of every aggregate version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate
// versions, so a test can prove that a failed use case stored and changed
// nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.data
	var versions int64
	for _, x := range d.departures {
		versions += x.Version
	}
	for _, x := range d.schedules {
		versions += x.Version
	}
	for _, x := range d.loads {
		versions += x.Version
	}
	for _, x := range d.volumes {
		versions += x.Version
	}
	return Counts{
		Departures: len(d.departures), Schedules: len(d.schedules), Loads: len(d.loads), Nodes: len(d.nodes), Lanes: len(d.lanes),
		Volumes: len(d.volumes), Processed: len(d.processed), OutboxEvents: len(d.outbox), VersionSum: versions,
	}
}
