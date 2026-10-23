package network

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

func goodProfile() Profile {
	return Profile{
		Revision: 2, Timezone: "America/Sao_Paulo",
		Hours:    map[string]Hours{"monday": {Start: "06:00", End: "22:00"}},
		Holidays: []string{"2026-12-25"},
		Cutoffs:  []Cutoff{{LocalTime: "18:00", Days: []string{"monday"}, MaxOrders: 10}},
		Cap:      ThroughputCap{Value: 5, TimeUnit: CapHour},
	}
}

func TestWeekdayNamesRoundTrip(t *testing.T) {
	for d := time.Sunday; d <= time.Saturday; d++ {
		name := WeekdayName(d)
		for _, in := range []string{name, strings.ToUpper(name)} {
			got, ok := ParseWeekday(in)
			if !ok || got != d {
				t.Errorf("ParseWeekday(%q) = %v,%v want %v", in, got, ok, d)
			}
		}
	}
	if _, ok := ParseWeekday("funday"); ok {
		t.Fatal("unknown weekday accepted")
	}
	if _, ok := ParseWeekday(""); ok {
		t.Fatal("empty weekday accepted")
	}
}

func TestParseClock(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"00:00", 0, true}, {"23:59", 1439, true}, {"18:30", 1110, true}, {"09:05", 545, true},
		{"24:00", 0, false}, {"12:60", 0, false}, {"1:00", 0, false}, {"12-00", 0, false}, {"ab:cd", 0, false},
		{"", 0, false}, {"12:000", 0, false}, {"a2:00", 0, false}, {"1a:00", 0, false}, {"12:a0", 0, false}, {"12:0a", 0, false},
		{"20:00", 1200, true}, {"19:59", 1199, true}, {"23:00", 1380, true}, {"00:59", 59, true},
	}
	for _, c := range cases {
		got, ok := ParseClock(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseClock(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestProfileValidate(t *testing.T) {
	if err := goodProfile().Validate(); err != nil {
		t.Fatalf("good profile: %v", err)
	}
	cases := map[string]func(p *Profile){
		"revision zero":    func(p *Profile) { p.Revision = 0 },
		"revision too big": func(p *Profile) { p.Revision = shared.MaxInt32 + 1 },
		"zone unknown":     func(p *Profile) { p.Timezone = "Mars/Base" },
		"zone empty":       func(p *Profile) { p.Timezone = "" },
		"weekday unknown":  func(p *Profile) { p.Hours = map[string]Hours{"funday": {Start: "06:00", End: "07:00"}} },
		"opening bad":      func(p *Profile) { p.Hours = map[string]Hours{"monday": {Start: "6", End: "07:00"}} },
		"closing bad":      func(p *Profile) { p.Hours = map[string]Hours{"monday": {Start: "06:00", End: "25:00"}} },
		"holiday bad":      func(p *Profile) { p.Holidays = []string{"25/12/2026"} },
		"cutoff bad":       func(p *Profile) { p.Cutoffs = []Cutoff{{LocalTime: "6pm"}} },
		"cutoff day bad":   func(p *Profile) { p.Cutoffs = []Cutoff{{LocalTime: "18:00", Days: []string{"x"}}} },
		"maxOrders neg":    func(p *Profile) { p.Cutoffs = []Cutoff{{LocalTime: "18:00", MaxOrders: -1}} },
		"maxOrders big":    func(p *Profile) { p.Cutoffs = []Cutoff{{LocalTime: "18:00", MaxOrders: shared.MaxInt32 + 1}} },
		"cap zero":         func(p *Profile) { p.Cap.Value = 0 },
		"cap too big":      func(p *Profile) { p.Cap.Value = shared.MaxInt32 + 1 },
		"cap unit":         func(p *Profile) { p.Cap.TimeUnit = "WEEK" },
	}
	for name, mut := range cases {
		p := goodProfile()
		mut(&p)
		if err := p.Validate(); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want validation", name, err)
		}
	}
	edges := map[string]func(p *Profile){
		"revision one":    func(p *Profile) { p.Revision = 1 },
		"revision max":    func(p *Profile) { p.Revision = shared.MaxInt32 },
		"maxOrders max":   func(p *Profile) { p.Cutoffs = []Cutoff{{LocalTime: "18:00", MaxOrders: shared.MaxInt32}} },
		"cap one":         func(p *Profile) { p.Cap.Value = 1 },
		"maxOrders unset": func(p *Profile) { p.Cutoffs = []Cutoff{{LocalTime: "18:00"}} },
		"cap max day":     func(p *Profile) { p.Cap = ThroughputCap{Value: shared.MaxInt32, TimeUnit: CapDay} },
	}
	for name, mut := range edges {
		p := goodProfile()
		mut(&p)
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestProfileHelpers(t *testing.T) {
	p := goodProfile()
	loc, err := p.Location()
	if err != nil || loc.String() != "America/Sao_Paulo" {
		t.Fatalf("Location = %v, %v", loc, err)
	}
	bad := Profile{Timezone: "Mars/Base"}
	if _, err := bad.Location(); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("bad zone: %v", err)
	}
	if !p.IsHoliday(time.Date(2026, 12, 25, 10, 0, 0, 0, time.UTC)) || p.IsHoliday(time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("IsHoliday")
	}
	s, e, ok := p.OperatingHours(time.Monday)
	if !ok || s != 360 || e != 1320 {
		t.Fatalf("OperatingHours monday = %d,%d,%v", s, e, ok)
	}
	if _, _, ok := p.OperatingHours(time.Tuesday); ok {
		t.Fatal("tuesday is closed")
	}
	p.Hours["sunday"] = Hours{Start: "bad", End: "10:00"}
	if _, _, ok := p.OperatingHours(time.Sunday); ok {
		t.Fatal("bad opening must read as closed")
	}
	p.Hours["saturday"] = Hours{Start: "08:00", End: "bad"}
	if _, _, ok := p.OperatingHours(time.Saturday); ok {
		t.Fatal("bad closing must read as closed")
	}
}

func TestCutoffAppliesOn(t *testing.T) {
	every := Cutoff{LocalTime: "10:00"}
	some := Cutoff{LocalTime: "10:00", Days: []string{"monday", "FRIDAY", "bogus"}}
	for d := time.Sunday; d <= time.Saturday; d++ {
		if !every.AppliesOn(d) {
			t.Errorf("every day cutoff must apply on %v", d)
		}
		want := d == time.Monday || d == time.Friday
		if some.AppliesOn(d) != want {
			t.Errorf("some.AppliesOn(%v) = %v want %v", d, !want, want)
		}
	}
}

func TestNodeTypeValid(t *testing.T) {
	for _, ty := range []NodeType{FulfillmentCenter, InboundCrossDock, ReturnsCenter, SortationCenter, DeliveryStation, AirHub} {
		if !ty.Valid() {
			t.Errorf("%s must be valid", ty)
		}
	}
	if NodeType("").Valid() || NodeType("DEPOT").Valid() {
		t.Fatal("unknown type accepted")
	}
}

func TestNodeViewRegister(t *testing.T) {
	n := NewNodeView("n1")
	if n.Status != StatusProposed || n.Generation != 0 || n.Profile != nil {
		t.Fatalf("new view = %+v", n)
	}
	if err := n.ApplyRegistered("DEPOT", OperatorVendor, "UTC"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("bad type: %v", err)
	}
	if err := n.ApplyRegistered(FulfillmentCenter, "ALIEN", "UTC"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("bad operator: %v", err)
	}
	if err := n.ApplyRegistered(FulfillmentCenter, OperatorVendor, "America/Sao_Paulo"); err != nil {
		t.Fatal(err)
	}
	if n.Type != FulfillmentCenter || n.Operator != OperatorVendor || n.Timezone != "America/Sao_Paulo" {
		t.Fatalf("registered = %+v", n)
	}
	if err := n.ApplyRegistered(SortationCenter, OperatorNetwork, "UTC"); err != nil || n.Type != SortationCenter {
		t.Fatalf("network operator accepted: %v", err)
	}
}

func TestNodeViewProfileRevisions(t *testing.T) {
	n := NewNodeView("n1")
	p := goodProfile()
	if ok, err := n.ApplyProfile(p); !ok || err != nil || n.Profile.Revision != 2 {
		t.Fatalf("first profile: %v %v", ok, err)
	}
	old := goodProfile()
	old.Revision = 2
	if ok, err := n.ApplyProfile(old); ok || err != nil {
		t.Fatalf("same revision must be ignored: %v %v", ok, err)
	}
	old.Revision = 1
	if ok, _ := n.ApplyProfile(old); ok {
		t.Fatal("older revision must be ignored")
	}
	next := goodProfile()
	next.Revision = 3
	if ok, _ := n.ApplyProfile(next); !ok || n.Profile.Revision != 3 {
		t.Fatal("higher revision must apply")
	}
	bad := goodProfile()
	bad.Revision = 9
	bad.Timezone = "Mars/Base"
	if ok, err := n.ApplyProfile(bad); ok || !errors.Is(err, shared.ErrValidation) || n.Profile.Revision != 3 {
		t.Fatalf("invalid profile: %v %v", ok, err)
	}
}

func TestNodeViewLifecycle(t *testing.T) {
	n := NewNodeView("n1")
	steps := []struct {
		name    string
		do      func() bool
		changed bool
		status  Status
		gen     int
	}{
		{"suspend proposed", n.ApplySuspended, true, StatusSuspended, 0},
		{"suspend again", n.ApplySuspended, false, StatusSuspended, 0},
		{"activate", n.ApplyActivated, true, StatusActive, 1},
		{"activate again", n.ApplyActivated, false, StatusActive, 1},
		{"suspend", n.ApplySuspended, true, StatusSuspended, 1},
		{"reactivate", n.ApplyActivated, true, StatusActive, 2},
		{"retire", n.ApplyRetired, true, StatusRetired, 2},
		{"retire again", n.ApplyRetired, false, StatusRetired, 2},
		{"activate retired", n.ApplyActivated, false, StatusRetired, 2},
		{"suspend retired", n.ApplySuspended, false, StatusRetired, 2},
	}
	for _, s := range steps {
		if got := s.do(); got != s.changed || n.Status != s.status || n.Generation != s.gen {
			t.Errorf("%s: changed=%v status=%s gen=%d; want %v %s %d", s.name, got, n.Status, n.Generation, s.changed, s.status, s.gen)
		}
	}
}

func TestPickupEligible(t *testing.T) {
	p := goodProfile()
	ok := NodeView{ID: "n", Type: FulfillmentCenter, Operator: OperatorVendor, Status: StatusActive, Profile: &p}
	if !ok.PickupEligible() {
		t.Fatal("vendor FC active with profile must be eligible")
	}
	mutations := map[string]func(n *NodeView){
		"type":     func(n *NodeView) { n.Type = SortationCenter },
		"operator": func(n *NodeView) { n.Operator = OperatorNetwork },
		"status":   func(n *NodeView) { n.Status = StatusSuspended },
		"profile":  func(n *NodeView) { n.Profile = nil },
	}
	for name, m := range mutations {
		n := ok
		m(&n)
		if n.PickupEligible() {
			t.Errorf("%s must make the node ineligible", name)
		}
	}
}

func TestLaneView(t *testing.T) {
	l, err := NewLaneView("l1", "a", "b", ModeGround, 60)
	if err != nil || !l.Open || l.TransitMinutes != 60 {
		t.Fatalf("lane = %+v %v", l, err)
	}
	if _, err := NewLaneView("l1", "a", "b", ModeAir, shared.MaxInt32); err != nil {
		t.Fatalf("air lane at the ceiling: %v", err)
	}
	if _, err := NewLaneView("l1", "a", "b", ModeGround, 1); err != nil {
		t.Fatalf("one minute of transit is fine: %v", err)
	}
	bad := []struct {
		id, o, d string
		m        Mode
		tr       int
	}{
		{"", "a", "b", ModeGround, 1}, {"l", "", "b", ModeGround, 1}, {"l", "a", "", ModeGround, 1},
		{"l", "a", "b", "RAIL", 1}, {"l", "a", "b", ModeGround, 0}, {"l", "a", "b", ModeGround, shared.MaxInt32 + 1},
	}
	for _, b := range bad {
		if _, err := NewLaneView(b.id, b.o, b.d, b.m, b.tr); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%+v: err = %v", b, err)
		}
	}
	if !l.ApplyClosed() || l.Open {
		t.Fatal("close must report it was open")
	}
	if l.ApplyClosed() {
		t.Fatal("second close must report no change")
	}
}

func TestDefaultOutboundLane(t *testing.T) {
	types := map[string]NodeType{"sc1": SortationCenter, "sc2": SortationCenter, "ds": DeliveryStation}
	typeOf := func(id string) NodeType { return types[id] }
	lane := func(id, dest string, mode Mode, transit int, open bool) LaneView {
		return LaneView{ID: id, Origin: "fc", Destination: dest, Mode: mode, TransitMinutes: transit, Open: open}
	}
	if _, ok := DefaultOutboundLane("fc", nil, typeOf); ok {
		t.Fatal("no lanes, no default")
	}
	lanes := []LaneView{
		lane("l-ds", "ds", ModeGround, 10, true),
		lane("l-unknown", "ghost", ModeGround, 5, true),
		lane("l-air", "sc1", ModeAir, 1, true),
		lane("l-closed", "sc1", ModeGround, 1, false),
		{ID: "l-other", Origin: "other", Destination: "sc1", Mode: ModeGround, TransitMinutes: 1, Open: true},
		lane("l-sc2-slow", "sc2", ModeGround, 90, true),
		lane("l-sc1-b", "sc1", ModeGround, 60, true),
		lane("l-sc1-a", "sc1", ModeGround, 60, true),
	}
	got, ok := DefaultOutboundLane("fc", lanes, typeOf)
	if !ok || got.ID != "l-sc1-a" {
		t.Fatalf("default = %+v %v; want sort centre, lowest transit, lowest id", got, ok)
	}
	got, _ = DefaultOutboundLane("fc", lanes[:2], typeOf)
	if got.ID != "l-ds" {
		t.Fatalf("a known non-sort type beats an unknown one: %s", got.ID)
	}
	got, _ = DefaultOutboundLane("fc", lanes[1:2], typeOf)
	if got.ID != "l-unknown" {
		t.Fatalf("unknown destination is still a candidate: %s", got.ID)
	}
	slow := []LaneView{lane("a", "sc1", ModeGround, 90, true), lane("b", "sc2", ModeGround, 30, true)}
	if got, _ = DefaultOutboundLane("fc", slow, typeOf); got.ID != "b" {
		t.Fatalf("lowest transit wins among sort centres: %s", got.ID)
	}
}
