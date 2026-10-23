package schedule

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func slot(day time.Weekday, cutoff, departs, arrives string, capacity int) Slot {
	return Slot{Day: day, Cutoff: cutoff, Departs: departs, Arrives: arrives, CapacityPackages: capacity}
}

func okSlots() []Slot {
	return []Slot{slot(time.Monday, "18:00", "18:30", "22:45", 100), slot(time.Tuesday, "18:00", "18:30", "22:45", 100)}
}

func TestDefineValidates(t *testing.T) {
	cases := []struct {
		name   string
		laneID string
		slots  []Slot
		target error
	}{
		{"lane id", "bad id", okSlots(), shared.ErrValidation},
		{"no slots", "l1", nil, shared.ErrValidation},
		{"too many slots", "l1", manySlots(MaxSlots + 1), shared.ErrValidation},
		{"day below range", "l1", []Slot{slot(time.Weekday(-1), "10:00", "11:00", "12:00", 1)}, shared.ErrValidation},
		{"day above range", "l1", []Slot{slot(time.Weekday(7), "10:00", "11:00", "12:00", 1)}, shared.ErrValidation},
		{"capacity zero", "l1", []Slot{slot(time.Monday, "10:00", "11:00", "12:00", 0)}, shared.ErrValidation},
		{"capacity over ceiling", "l1", []Slot{slot(time.Monday, "10:00", "11:00", "12:00", shared.MaxInt32+1)}, shared.ErrValidation},
		{"bad cutoff", "l1", []Slot{slot(time.Monday, "10", "11:00", "12:00", 1)}, shared.ErrValidation},
		{"bad departs", "l1", []Slot{slot(time.Monday, "10:00", "25:00", "12:00", 1)}, shared.ErrValidation},
		{"bad arrives", "l1", []Slot{slot(time.Monday, "10:00", "11:00", "xx:yy", 1)}, shared.ErrValidation},
		{"cutoff after departs", "l1", []Slot{slot(time.Monday, "11:01", "11:00", "12:00", 1)}, departure.ErrInvalidDepartureTimes},
	}
	for _, c := range cases {
		s, err := Define(c.laneID, c.slots)
		wantErr(t, c.name, err, c.target)
		if s != nil {
			t.Errorf("%s: a refused define returns nothing", c.name)
		}
	}
}

func manySlots(n int) []Slot {
	out := make([]Slot, 0, n)
	for i := 0; i < n; i++ {
		day := time.Weekday(i % 7)
		minute := (i / 7) * 2
		hhmm := time.Date(2026, 1, 1, minute/60, minute%60, 0, 0, time.UTC).Format("15:04")
		out = append(out, slot(day, hhmm, hhmm, "23:59", 1))
	}
	return out
}

func TestDefineBoundaries(t *testing.T) {
	cases := map[string][]Slot{
		"cutoff equals departs": {slot(time.Monday, "10:00", "10:00", "12:00", 1)},
		"capacity ceiling":      {slot(time.Monday, "10:00", "11:00", "12:00", shared.MaxInt32)},
		"exactly max slots":     manySlots(MaxSlots),
		"every weekday":         {slot(time.Sunday, "01:00", "02:00", "03:00", 1), slot(time.Saturday, "01:00", "02:00", "03:00", 1)},
		"same time other days":  {slot(time.Monday, "10:00", "11:00", "12:00", 1), slot(time.Friday, "10:00", "11:00", "12:00", 1)},
		"separated slots":       {slot(time.Monday, "08:00", "09:00", "10:00", 1), slot(time.Monday, "09:01", "10:00", "11:00", 1)},
	}
	for name, slots := range cases {
		s, err := Define("l1", slots)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if s.State() != StateActive || s.Version() != 1 || s.LaneID() != "l1" || len(s.Slots()) != len(slots) {
			t.Errorf("%s: schedule = %+v", name, s.Snapshot())
		}
	}
}

func TestOverlapRule(t *testing.T) {
	cases := []struct {
		name  string
		slots []Slot
		want  bool
	}{
		{"identical", []Slot{slot(time.Monday, "10:00", "11:00", "12:00", 1), slot(time.Monday, "10:00", "11:00", "13:00", 1)}, true},
		{"touching endpoint", []Slot{slot(time.Monday, "10:00", "11:00", "12:00", 1), slot(time.Monday, "11:00", "12:00", "13:00", 1)}, true},
		{"touching, given in reverse", []Slot{slot(time.Monday, "11:00", "12:00", "13:00", 1), slot(time.Monday, "10:00", "11:00", "12:00", 1)}, true},
		{"one minute apart", []Slot{slot(time.Monday, "10:00", "11:00", "12:00", 1), slot(time.Monday, "11:01", "12:00", "13:00", 1)}, false},
		{"contained", []Slot{slot(time.Monday, "08:00", "20:00", "21:00", 1), slot(time.Monday, "10:00", "11:00", "12:00", 1)}, true},
		{"contains, given in reverse", []Slot{slot(time.Monday, "10:00", "11:00", "12:00", 1), slot(time.Monday, "08:00", "20:00", "21:00", 1)}, true},
		{"partial", []Slot{slot(time.Monday, "10:00", "12:00", "13:00", 1), slot(time.Monday, "11:00", "13:00", "14:00", 1)}, true},
		{"different days", []Slot{slot(time.Monday, "10:00", "11:00", "12:00", 1), slot(time.Tuesday, "10:00", "11:00", "12:00", 1)}, false},
		{"zero-length twice", []Slot{slot(time.Monday, "10:00", "10:00", "12:00", 1), slot(time.Monday, "10:00", "10:00", "12:00", 1)}, true},
		{"third overlaps the first", []Slot{slot(time.Monday, "08:00", "09:00", "10:00", 1), slot(time.Monday, "12:00", "13:00", "14:00", 1), slot(time.Monday, "08:30", "08:45", "10:00", 1)}, true},
	}
	for _, c := range cases {
		_, err := Define("l1", c.slots)
		if c.want {
			wantErr(t, c.name, err, ErrOverlappingSlots)
		} else if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestRedefineAndRetire(t *testing.T) {
	s, err := Define("l1", okSlots())
	wantNoErr(t, "define", err)
	replacement := []Slot{slot(time.Friday, "10:00", "11:00", "12:00", 7)}
	wantNoErr(t, "redefine", s.Redefine(replacement))
	if s.Version() != 2 || len(s.Slots()) != 1 || s.Slots()[0].Day != time.Friday {
		t.Fatalf("after redefine: %+v", s.Snapshot())
	}
	wantErr(t, "invalid replacement", s.Redefine(nil), shared.ErrValidation)
	wantErr(t, "overlapping replacement", s.Redefine([]Slot{replacement[0], replacement[0]}), ErrOverlappingSlots)
	if s.Version() != 2 || s.Slots()[0].CapacityPackages != 7 {
		t.Fatal("a refused redefine changes nothing")
	}
	wantNoErr(t, "retire", s.Retire())
	if s.State() != StateRetired || s.Version() != 3 {
		t.Fatalf("after retire: %+v", s.Snapshot())
	}
	wantErr(t, "retire again", s.Retire(), shared.ErrIllegalTransition)
	wantErr(t, "redefine retired", s.Redefine(okSlots()), shared.ErrIllegalTransition)
	if s.Version() != 3 {
		t.Fatal("a refused command changes nothing")
	}
}

func TestSlotsAreCopied(t *testing.T) {
	in := okSlots()
	s, _ := Define("l1", in)
	in[0].CapacityPackages = 999
	out := s.Slots()
	out[1].CapacityPackages = 888
	if s.Slots()[0].CapacityPackages != 100 || s.Slots()[1].CapacityPackages != 100 {
		t.Fatal("the aggregate must own its slots")
	}
	snap := s.Snapshot()
	snap.Slots[0].CapacityPackages = 777
	if s.Slots()[0].CapacityPackages != 100 {
		t.Fatal("the snapshot must not alias the aggregate")
	}
}

func TestSnapshotRestore(t *testing.T) {
	s, _ := Define("l1", okSlots())
	got, err := Restore(s.Snapshot())
	wantNoErr(t, "restore", err)
	if g, w := got.Snapshot(), s.Snapshot(); g.LaneID != w.LaneID || g.State != w.State || g.Version != w.Version || len(g.Slots) != len(w.Slots) {
		t.Fatalf("round trip %+v -> %+v", w, g)
	}
	_ = s.Retire()
	got, err = Restore(s.Snapshot())
	wantNoErr(t, "restore retired", err)
	if got.State() != StateRetired {
		t.Fatal("retired state lost")
	}
	good := func() Snapshot { return Snapshot{LaneID: "l1", State: StateActive, Slots: okSlots(), Version: 1} }
	if _, err := Restore(good()); err != nil {
		t.Fatalf("version one: %v", err)
	}
	bad := map[string]func(*Snapshot){
		"lane id":      func(s *Snapshot) { s.LaneID = "" },
		"state":        func(s *Snapshot) { s.State = "PAUSED" },
		"version":      func(s *Snapshot) { s.Version = 0 },
		"no slots":     func(s *Snapshot) { s.Slots = nil },
		"overlap":      func(s *Snapshot) { s.Slots = []Slot{okSlots()[0], okSlots()[0]} },
		"slot invalid": func(s *Snapshot) { s.Slots = []Slot{slot(time.Monday, "10:00", "11:00", "12:00", 0)} },
	}
	for name, mut := range bad {
		sn := good()
		mut(&sn)
		if _, err := Restore(sn); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func wantErr(t *testing.T, label string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v, want %v", label, err, target)
	}
}

func wantNoErr(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", label, err)
	}
}
