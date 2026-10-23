package usecases

import (
	"context"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// ListDeparturesQuery is the input of ListDepartures; a nil From or To does not filter.
type ListDeparturesQuery struct {
	Origin      string
	Destination string
	From        *time.Time
	To          *time.Time
	// Limit 0 means repository.DefaultListLimit.
	Limit int
}

// ListDepartures lists departures by origin, destination and cutoff window
// (ADR 0002 A24).
func (s *Service) ListDepartures(ctx context.Context, q ListDeparturesQuery) (views.DepartureList, error) {
	if q.Limit < 0 || q.Limit > repository.MaxListLimit {
		return views.DepartureList{}, shared.Invalid("limit must be between 1 and %d", repository.MaxListLimit)
	}
	f := repository.DepartureFilter{Origin: q.Origin, Destination: q.Destination, Limit: q.Limit}
	if q.From != nil {
		f.CutoffFrom = *q.From
	}
	if q.To != nil {
		f.CutoffTo = *q.To
	}
	if q.From != nil && q.To != nil && q.From.After(*q.To) {
		return views.DepartureList{}, shared.Invalid("from is after to")
	}
	list, err := s.Departures.List(ctx, f)
	if err != nil {
		return views.DepartureList{}, err
	}
	return views.FromDepartures(list), nil
}

// GetDeparture returns one departure.
func (s *Service) GetDeparture(ctx context.Context, id string) (views.Departure, error) {
	d, err := s.Departures.Get(ctx, departure.ID(id))
	if err != nil {
		return views.Departure{}, err
	}
	return views.FromDeparture(d), nil
}

// CancelDeparture cancels a SCHEDULED departure with a reason, rebooks the
// assignments booked on it (BookingMoved or BookingFailed) and returns it. A
// CANCELLED departure is returned unchanged.
func (s *Service) CancelDeparture(ctx context.Context, id, reason string) (views.Departure, error) {
	var out views.Departure
	err := s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		b := s.newBookingSet(ctx)
		d, err := b.dep(departure.ID(id))
		if err != nil {
			return nil, err
		}
		if d.State() != departure.StateCancelled {
			if err := b.cancel([]departure.ID{d.ID()}, reason, true); err != nil {
				return nil, err
			}
		}
		out = views.FromDeparture(d)
		return b.events, nil
	})
	return out, err
}

// ChangeDepartureCapacity sets the package capacity of a SCHEDULED departure
// (gap G28, ADR 0002 A23).
func (s *Service) ChangeDepartureCapacity(ctx context.Context, id string, capacity int) (views.Departure, error) {
	var out views.Departure
	err := s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		d, err := s.Departures.Get(ctx, departure.ID(id))
		if err != nil {
			return nil, err
		}
		evs, err := d.ChangeCapacity(capacity, s.Clock.Now())
		if err != nil {
			return nil, err
		}
		if len(evs) > 0 {
			if err := s.Departures.Save(ctx, d); err != nil {
				return nil, err
			}
		}
		out = views.FromDeparture(d)
		return evs, nil
	})
	return out, err
}
