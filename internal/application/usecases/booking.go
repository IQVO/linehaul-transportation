package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// bookingSet is the working set of one transaction that books, rebooks and
// cancels. It loads each departure once and saves after every change, so a
// departure keeps one version line inside the transaction and the repository
// always answers with the current state (ADR 0002 A14, A15).
type bookingSet struct {
	s      *Service
	ctx    context.Context
	now    time.Time
	deps   map[departure.ID]*departure.Departure
	events []shared.Event
}

func (s *Service) newBookingSet(ctx context.Context) *bookingSet {
	return &bookingSet{s: s, ctx: ctx, now: s.Clock.Now(), deps: map[departure.ID]*departure.Departure{}}
}

func (b *bookingSet) dep(id departure.ID) (*departure.Departure, error) {
	if d, ok := b.deps[id]; ok {
		return d, nil
	}
	d, err := b.s.Departures.Get(b.ctx, id)
	if err != nil {
		return nil, err
	}
	b.deps[id] = d
	return d, nil
}

func (b *bookingSet) save(d *departure.Departure) error {
	b.deps[d.ID()] = d
	return b.s.Departures.Save(b.ctx, d)
}

// next returns the next departure to take a booking: same origin, PICKUP,
// SCHEDULED, cutoff after now and after the failed departure's cutoff, with room.
func (b *bookingSet) next(origin string, after time.Time) (*departure.Departure, error) {
	if after.Before(b.now) {
		after = b.now
	}
	list, err := b.s.Departures.List(b.ctx, repository.DepartureFilter{
		Origin: origin, Kind: departure.KindPickup, State: departure.StateScheduled, CutoffAfter: after, HasRoom: true, Limit: 1,
	})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return b.dep(list[0].ID())
}

// bookOn books one package on d and saves it.
func (b *bookingSet) bookOn(d *departure.Departure) error {
	evs, err := d.Book(b.now)
	if err != nil {
		return err
	}
	b.events = append(b.events, evs...)
	return b.save(d)
}

func reasonOf(d *departure.Departure, err error) volume.Reason {
	switch {
	case d.State() == departure.StateCancelled:
		return volume.ReasonDepartureCancelled
	case errors.Is(err, departure.ErrDepartureFull):
		return volume.ReasonDepartureFull
	}
	return volume.ReasonDepartureClosed
}

// place books the locked assignment on its pickup departure, or rebooks it
// onto the next departure from the same origin, or records the failure.
func (b *bookingSet) place(vol *volume.ExpectedVolume, target departure.ID, origin string) error {
	d, err := b.dep(target)
	if isNotFound(err) {
		return b.rebookOrFail(vol, target, origin, volume.ReasonDepartureUnknown, time.Time{})
	}
	if err != nil {
		return err
	}
	berr := b.bookOn(d)
	if berr == nil {
		return vol.Book(d.ID())
	}
	if !errors.Is(berr, departure.ErrDepartureFull) && !errors.Is(berr, departure.ErrDepartureClosed) {
		return berr
	}
	return b.rebookOrFail(vol, target, d.OriginNodeID(), reasonOf(d, berr), d.CutoffAt())
}

// rebookOrFail moves the booking of vol, which cannot stay on from, to the next
// departure from origin (BookingMoved) or records BookingFailed.
func (b *bookingSet) rebookOrFail(vol *volume.ExpectedVolume, from departure.ID, origin string, reason volume.Reason, after time.Time) error {
	next, err := b.next(origin, after)
	if err != nil {
		return err
	}
	if next == nil {
		evs, ferr := vol.Fail(from, reason, b.now)
		b.events = append(b.events, evs...)
		return ferr
	}
	if err := b.bookOn(next); err != nil {
		return err
	}
	evs, err := vol.Rebook(from, next.ID(), b.now)
	b.events = append(b.events, evs...)
	return err
}

// cancel cancels the departures, then rebooks every assignment booked on them
// (ADR 0002 A15). strict makes a departure that is not SCHEDULED an error;
// otherwise it is skipped.
func (b *bookingSet) cancel(ids []departure.ID, reason string, strict bool) error {
	var cancelled []*departure.Departure
	for _, id := range ids {
		d, err := b.dep(id)
		if err != nil {
			return err
		}
		evs, err := d.Cancel(reason, b.now)
		if errors.Is(err, shared.ErrIllegalTransition) && !strict {
			continue
		}
		if err != nil {
			return err
		}
		b.events = append(b.events, evs...)
		if err := b.save(d); err != nil {
			return err
		}
		cancelled = append(cancelled, d)
	}
	for _, d := range cancelled {
		if err := b.moveBookingsOff(d); err != nil {
			return err
		}
	}
	return nil
}

func (b *bookingSet) moveBookingsOff(d *departure.Departure) error {
	vols, err := b.s.Volumes.ListBookedOn(b.ctx, d.ID())
	if err != nil {
		return err
	}
	for _, vol := range vols {
		// Release fails only when the departure holds nothing: the row and the
		// departure disagree, and the booking is moved off anyway.
		if _, rerr := d.Release(b.now); rerr == nil {
			if err := b.save(d); err != nil {
				return err
			}
		}
		if err := b.rebookOrFail(vol, d.ID(), d.OriginNodeID(), volume.ReasonDepartureCancelled, d.CutoffAt()); err != nil {
			return err
		}
		if err := b.s.Volumes.Save(b.ctx, vol); err != nil {
			return err
		}
	}
	return nil
}
