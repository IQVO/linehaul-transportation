// Package usecases holds one use case per command and query of the model and
// per REST operation, plus the policies (PickupFromCutoffs, BookOnLock,
// HorizonRoll) and the Kafka-driven handlers that apply consumed events. Every
// write runs in one UnitOfWork: the aggregates, the inbox claim and the outbox
// rows commit together.
package usecases

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// MaxConflictRetries is how many times a consumer-driven use case is retried
// on ErrConcurrentUpdate (ADR 0002 A26).
const MaxConflictRetries = 5

// Deps are the ports and settings a Service needs.
type Deps struct {
	UoW        ports.UnitOfWork
	Departures ports.DepartureRepository
	Schedules  ports.LaneScheduleRepository
	Loads      ports.LoadRepository
	Nodes      ports.NodeViewRepository
	Lanes      ports.LaneViewRepository
	Volumes    ports.VolumeRepository
	Inbox      ports.ProcessedEvents
	Outbox     ports.EventOutbox
	Clock      ports.Clock
	IDs        ports.IDGenerator
	// PackagesPerOrder converts the order cap of a pickup into packages (ADR
	// 0002 A9). Zero means 1.
	PackagesPerOrder int
	// Logger receives the per-item refusals of the policies. Nil discards.
	Logger *slog.Logger
}

// Service is the application layer of linehaul-transportation.
type Service struct {
	Deps
}

// New returns a Service, filling the optional settings of deps.
func New(deps Deps) *Service {
	if deps.PackagesPerOrder == 0 {
		deps.PackagesPerOrder = 1
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{Deps: deps}
}

// write runs fn in one unit of work and appends the events it returns to the
// outbox in the same transaction.
func (s *Service) write(ctx context.Context, fn func(ctx context.Context) ([]shared.Event, error)) error {
	return s.UoW.Do(ctx, func(ctx context.Context) error {
		evs, err := fn(ctx)
		if err != nil {
			return err
		}
		return s.Outbox.Append(ctx, evs...)
	})
}

// consume runs fn as the effect of one consumed event: the CloudEvents id is
// claimed in the inbox in the same transaction, a repeated id does nothing, and
// an optimistic-lock conflict is retried (ADR 0003).
func (s *Service) consume(ctx context.Context, consumer, eventID string, fn func(ctx context.Context) ([]shared.Event, error)) error {
	var err error
	for attempt := 0; attempt < MaxConflictRetries; attempt++ {
		err = s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
			first, cerr := s.Inbox.Claim(ctx, consumer, eventID, s.Clock.Now())
			if cerr != nil || !first {
				return nil, cerr
			}
			return fn(ctx)
		})
		if !errors.Is(err, ports.ErrConcurrentUpdate) {
			return err
		}
	}
	return err
}

func isNotFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }
