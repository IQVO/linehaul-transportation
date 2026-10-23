package usecases

import (
	"context"

	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// loadStep is one transition of a load together with its departure: withLoad
// loads both, lets step change them and saves, in the same unit of work, the
// ones whose version moved.
type loadStep func(ctx context.Context, l *load.Load, d *departure.Departure) (loadEvents, depEvents []shared.Event, err error)

func (s *Service) withLoad(ctx context.Context, id string, step loadStep) (views.Load, error) {
	var out views.Load
	err := s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		l, err := s.Loads.Get(ctx, load.ID(id))
		if err != nil {
			return nil, err
		}
		d, err := s.Departures.Get(ctx, l.DepartureID())
		if err != nil {
			return nil, err
		}
		lv, dv := l.Version(), d.Version()
		levs, devs, err := step(ctx, l, d)
		if err != nil {
			return nil, err
		}
		if l.Version() != lv {
			if err := s.Loads.Save(ctx, l); err != nil {
				return nil, err
			}
		}
		if d.Version() != dv {
			if err := s.Departures.Save(ctx, d); err != nil {
				return nil, err
			}
		}
		out = views.FromLoad(l)
		return append(levs, devs...), nil
	})
	return out, err
}

// OpenLoad opens the load of a SCHEDULED departure (ADR 0002 A19).
func (s *Service) OpenLoad(ctx context.Context, departureID string) (views.Load, error) {
	var out views.Load
	err := s.write(ctx, func(ctx context.Context) ([]shared.Event, error) {
		d, err := s.Departures.Get(ctx, departure.ID(departureID))
		if err != nil {
			return nil, err
		}
		_, gerr := s.Loads.GetByDeparture(ctx, d.ID())
		if gerr != nil && !isNotFound(gerr) {
			return nil, gerr
		}
		l, err := load.Open(s.IDs.NewLoadID(), d, gerr == nil)
		if err != nil {
			return nil, err
		}
		out = views.FromLoad(l)
		return nil, s.Loads.Save(ctx, l)
	})
	return out, err
}

// AddPackage adds a tracking number to an OPEN load. On a LINEHAUL or AIR
// departure the package is also booked on the departure (gap G15, ADR 0002 A20).
func (s *Service) AddPackage(ctx context.Context, loadID, trackingNumber string) (views.Load, error) {
	return s.withLoad(ctx, loadID, func(ctx context.Context, l *load.Load, d *departure.Departure) ([]shared.Event, []shared.Event, error) {
		other, err := s.Loads.FindActiveByTracking(ctx, trackingNumber)
		if err != nil && !isNotFound(err) {
			return nil, nil, err
		}
		otherActive := err == nil && other != l.ID()
		added, err := l.AddPackage(trackingNumber, otherActive, d.State())
		if err != nil || !added {
			return nil, nil, err
		}
		var devs []shared.Event
		if l.Kind() != departure.KindPickup {
			if devs, err = d.Book(s.Clock.Now()); err != nil {
				return nil, nil, err
			}
		}
		return nil, devs, nil
	})
}

// CloseLoad closes an OPEN load and its departure; a CLOSED load is returned unchanged.
func (s *Service) CloseLoad(ctx context.Context, loadID string) (views.Load, error) {
	return s.withLoad(ctx, loadID, func(ctx context.Context, l *load.Load, d *departure.Departure) ([]shared.Event, []shared.Event, error) {
		if l.State() == load.StateClosed {
			return nil, nil, nil
		}
		levs, err := l.Close(d.State(), s.Clock.Now())
		if err != nil {
			return nil, nil, err
		}
		return levs, nil, d.Close()
	})
}

// DispatchLoad dispatches a CLOSED load and records the departure of its trip;
// a DEPARTED load is returned unchanged.
func (s *Service) DispatchLoad(ctx context.Context, loadID string) (views.Load, error) {
	return s.withLoad(ctx, loadID, func(ctx context.Context, l *load.Load, d *departure.Departure) ([]shared.Event, []shared.Event, error) {
		if l.State() == load.StateDeparted {
			return nil, nil, nil
		}
		now := s.Clock.Now()
		levs, err := l.Dispatch(now)
		if err != nil {
			return nil, nil, err
		}
		devs, err := d.RecordDeparture(now)
		return levs, devs, err
	})
}

// ReceiveLoad receives a DEPARTED load and records the arrival of its trip; an
// ARRIVED load is returned unchanged.
func (s *Service) ReceiveLoad(ctx context.Context, loadID string) (views.Load, error) {
	return s.withLoad(ctx, loadID, func(ctx context.Context, l *load.Load, d *departure.Departure) ([]shared.Event, []shared.Event, error) {
		if l.State() == load.StateArrived {
			return nil, nil, nil
		}
		now := s.Clock.Now()
		levs, err := l.Receive(now)
		if err != nil {
			return nil, nil, err
		}
		devs, err := d.RecordArrival(now)
		return levs, devs, err
	})
}
