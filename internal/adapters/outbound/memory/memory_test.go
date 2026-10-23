package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/repocontract"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

// TestRepositoryContract runs the repository contract against the doubles.
func TestRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Fixture {
		s := memory.NewStore()
		return repocontract.Fixture{
			Departures: s.Departures(), Schedules: s.Schedules(), Loads: s.Loads(), Nodes: s.Nodes(), Lanes: s.Lanes(),
			Volumes: s.Volumes(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
			OutboxCount: func() (int, error) { return len(s.Outbox().Events()), nil },
		}
	})
}

func TestClockAndSequentialIDs(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	c := memory.NewClock(start)
	c.Advance(time.Hour)
	if !c.Now().Equal(start.Add(time.Hour)) {
		t.Fatalf("now = %v", c.Now())
	}
	c.Set(start)
	if !c.Now().Equal(start) {
		t.Fatalf("now = %v", c.Now())
	}
	ids := &memory.SequentialIDs{}
	if a, b := ids.NewLoadID(), ids.NewLoadID(); a != "load-1" || b != "load-2" {
		t.Fatalf("ids = %s %s", a, b)
	}
}

func TestStoredViewsDoNotAliasTheCaller(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	n := repocontract.NewNodeView("n-fc")
	if err := s.Nodes().Save(ctx, n); err != nil {
		t.Fatal(err)
	}
	n.Profile.Hours["monday"] = network.Hours{Start: "00:00", End: "01:00"}
	n.Profile.Cutoffs[1].Days[0] = "sunday"
	n.Profile.Holidays[0] = "1999-01-01"
	got, _ := s.Nodes().Get(ctx, "n-fc")
	if got.Profile.Hours["monday"].Start != "06:00" || got.Profile.Cutoffs[1].Days[0] != "monday" || got.Profile.Holidays[0] != "2026-12-25" {
		t.Fatalf("the store aliased the caller's profile: %+v", got.Profile)
	}
	got.Profile.Hours["monday"] = network.Hours{}
	again, _ := s.Nodes().Get(ctx, "n-fc")
	if again.Profile.Hours["monday"].Start != "06:00" {
		t.Fatal("the store aliased the returned profile")
	}
}

func TestCountsTrackEveryTable(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	d := repocontract.NewDeparture(t, "dep-1", "n-fc", departure.KindPickup, time.Hour, 2)
	_ = s.Departures().Save(ctx, d)
	_ = s.Schedules().Save(ctx, repocontract.NewSchedule(t, "lane-1"))
	_ = s.Loads().Save(ctx, repocontract.NewLoad(t, "load-1", d))
	_ = s.Nodes().Save(ctx, repocontract.NewNodeView("n"))
	_ = s.Lanes().Save(ctx, network.LaneView{ID: "l", Origin: "a", Destination: "b", Mode: network.ModeGround, TransitMinutes: 1, Open: true})
	_ = s.Volumes().Save(ctx, repocontract.NewVolume(t, "asg-1", "n", "dep-1"))
	_, _ = s.Inbox().Claim(ctx, "c", "e", time.Time{})
	got := s.Counts()
	want := memory.Counts{Departures: 1, Schedules: 1, Loads: 1, Nodes: 1, Lanes: 1, Volumes: 1, Processed: 1, VersionSum: 4}
	if got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
}

func TestUnitOfWorkRestoresOnError(t *testing.T) {
	s := memory.NewStore()
	boom := errors.New("boom")
	err := s.UnitOfWork().Do(context.Background(), func(ctx context.Context) error {
		_ = s.Nodes().Save(ctx, repocontract.NewNodeView("n"))
		_, _ = s.Inbox().Claim(ctx, "c", "e", time.Time{})
		return boom
	})
	if !errors.Is(err, boom) || s.Counts().Nodes != 0 || s.Counts().Processed != 0 {
		t.Fatalf("err = %v counts = %+v", err, s.Counts())
	}
}
