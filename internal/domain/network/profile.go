package network

import (
	"fmt"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// Hours are the local opening hours of one weekday, "HH:mm".
type Hours struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Cutoff is one declared cutoff of a node: the local time by which packages
// are ready for handover. Days are lowercase weekday names; empty means every
// operating day. MaxOrders 0 means unset.
type Cutoff struct {
	LocalTime string   `json:"localTime"`
	Days      []string `json:"days,omitempty"`
	MaxOrders int      `json:"maxOrders,omitempty"`
}

// The two units of a throughput cap.
const (
	CapHour = "HOUR"
	CapDay  = "DAY"
)

// ThroughputCap is the number of orders a node handles per hour or per day.
type ThroughputCap struct {
	Value    int    `json:"value"`
	TimeUnit string `json:"timeUnit"`
}

// Profile is the declared operational profile of a node, as of one revision.
// Hours is keyed by lowercase weekday name; a missing weekday is closed.
type Profile struct {
	Revision int              `json:"revision"`
	Timezone string           `json:"timezone"`
	Hours    map[string]Hours `json:"hours"`
	Holidays []string         `json:"holidays"`
	Cutoffs  []Cutoff         `json:"cutoffs"`
	Cap      ThroughputCap    `json:"cap"`
}

// Validate checks every value of the profile; the error wraps
// shared.ErrValidation.
func (p Profile) Validate() error {
	if p.Revision < 1 || p.Revision > shared.MaxInt32 {
		return shared.Invalid("profileRevision %d out of range", p.Revision)
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil || p.Timezone == "" {
		return shared.Invalid("timezone %q is not an IANA zone", p.Timezone)
	}
	if err := p.validateHours(); err != nil {
		return err
	}
	for _, h := range p.Holidays {
		if _, err := time.Parse(time.DateOnly, h); err != nil {
			return shared.Invalid("holiday %q is not an ISO date", h)
		}
	}
	for _, c := range p.Cutoffs {
		if err := c.validate(); err != nil {
			return err
		}
	}
	if p.Cap.Value < 1 || p.Cap.Value > shared.MaxInt32 || (p.Cap.TimeUnit != CapHour && p.Cap.TimeUnit != CapDay) {
		return shared.Invalid("throughput cap %d per %q is invalid", p.Cap.Value, p.Cap.TimeUnit)
	}
	return nil
}

func (p Profile) validateHours() error {
	for day, h := range p.Hours {
		if _, ok := ParseWeekday(day); !ok {
			return shared.Invalid("weekday %q is unknown", day)
		}
		if _, ok := ParseClock(h.Start); !ok {
			return shared.Invalid("opening time %q of %s is not HH:mm", h.Start, day)
		}
		if _, ok := ParseClock(h.End); !ok {
			return shared.Invalid("closing time %q of %s is not HH:mm", h.End, day)
		}
	}
	return nil
}

func (c Cutoff) validate() error {
	if _, ok := ParseClock(c.LocalTime); !ok {
		return shared.Invalid("cutoff %q is not HH:mm", c.LocalTime)
	}
	for _, d := range c.Days {
		if _, ok := ParseWeekday(d); !ok {
			return shared.Invalid("cutoff day %q is unknown", d)
		}
	}
	if c.MaxOrders < 0 || c.MaxOrders > shared.MaxInt32 {
		return shared.Invalid("maxOrders %d out of range", c.MaxOrders)
	}
	return nil
}

// Location returns the profile's time zone.
func (p Profile) Location() (*time.Location, error) {
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: timezone %q", shared.ErrValidation, p.Timezone)
	}
	return loc, nil
}

// IsHoliday reports whether the local date of t (in the profile's zone, which
// the caller already applied) is a declared holiday.
func (p Profile) IsHoliday(day time.Time) bool {
	d := day.Format(time.DateOnly)
	for _, h := range p.Holidays {
		if h == d {
			return true
		}
	}
	return false
}

// OperatingHours returns the hours of a weekday in minutes since midnight and
// whether the node operates that day.
func (p Profile) OperatingHours(d time.Weekday) (start, end int, ok bool) {
	h, found := p.Hours[WeekdayName(d)]
	if !found {
		return 0, 0, false
	}
	s, ok1 := ParseClock(h.Start)
	e, ok2 := ParseClock(h.End)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return s, e, true
}

// AppliesOn reports whether the cutoff applies on weekday d: it lists d, or it
// lists no day at all.
func (c Cutoff) AppliesOn(d time.Weekday) bool {
	if len(c.Days) == 0 {
		return true
	}
	for _, n := range c.Days {
		if w, ok := ParseWeekday(n); ok && w == d {
			return true
		}
	}
	return false
}
