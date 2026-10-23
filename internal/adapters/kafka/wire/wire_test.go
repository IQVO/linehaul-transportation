package wire_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/wire"
	"github.com/claudioed/linehaul-transportation/internal/application/outbox"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

var (
	at     = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	saoPau = time.FixedZone("BRT", -3*3600) // an event built in local time must still be sent in UTC
	hdr    = shared.Header{Version: 3, At: at}
)

const fixedID = "7d0b2f0e-8f43-4b0e-9a2e-0b6d6f5e4c21"

// cases lists one event of every published type with the golden file that
// holds its exact JSON, the topic and the CloudEvents type it must carry.
var cases = []struct {
	name  string
	event shared.Event
	topic string
	typ   string
	key   string
}{
	{"DepartureScheduled", departure.DepartureScheduled{Header: hdr, DepartureID: "dep-1", LaneID: "lane-1", Kind: departure.KindPickup,
		OriginNodeID: "n-fc", DestinationNodeID: "n-sc", CutoffAt: at.Add(time.Hour).In(saoPau), DepartsAt: at.Add(time.Hour), ArrivesAt: at.Add(3 * time.Hour), CapacityPackages: 40},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.departure.DepartureScheduled", "dep-1"},
	{"DepartureCapacityChanged", departure.DepartureCapacityChanged{Header: hdr, DepartureID: "dep-1", CapacityPackages: 40, BookedPackages: 15, RemainingPackages: 25},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.departure.DepartureCapacityChanged", "dep-1"},
	{"DepartureCancelled", departure.DepartureCancelled{Header: hdr, DepartureID: "dep-1", Reason: "NODE_SUSPENDED", CancelledAt: at},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.departure.DepartureCancelled", "dep-1"},
	{"DepartureDeparted", departure.DepartureDeparted{Header: hdr, DepartureID: "dep-1", DepartedAt: at},
		wire.AnalyticsTopic, "com.retailnetwork.logistics.linehaul-transportation.departure.DepartureDeparted", "dep-1"},
	{"DepartureArrived", departure.DepartureArrived{Header: hdr, DepartureID: "dep-1", ArrivedAt: at},
		wire.AnalyticsTopic, "com.retailnetwork.logistics.linehaul-transportation.departure.DepartureArrived", "dep-1"},
	{"LoadClosed", load.LoadClosed{Header: hdr, LoadID: "load-1", DepartureID: "dep-1", PackageCount: 2},
		wire.AnalyticsTopic, "com.retailnetwork.logistics.linehaul-transportation.load.LoadClosed", "load-1"},
	{"LoadDeparted", load.LoadDeparted{Header: hdr, LoadID: "load-1", DepartureID: "dep-1", OriginNodeID: "n-fc", TrackingNumbers: []string{"TN1", "TN2"},
		DepartedAt: at.In(saoPau), DepartureKind: departure.KindPickup, NodeType: network.FulfillmentCenter},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted", "load-1"},
	{"LoadArrived", load.LoadArrived{Header: hdr, LoadID: "load-1", DepartureID: "dep-1", DestinationNodeID: "n-sc", TrackingNumbers: []string{"TN1", "TN2"},
		ArrivedAt: at, DepartureKind: departure.KindLinehaul, NodeType: network.SortationCenter},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived", "load-1"},
	{"LoadArrivedEmpty", load.LoadArrived{Header: hdr, LoadID: "load-2", DepartureID: "dep-2", DestinationNodeID: "n-sc",
		ArrivedAt: at, DepartureKind: departure.KindAir, NodeType: network.AirHub},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived", "load-2"},
	{"BookingMoved", volume.BookingMoved{Header: hdr, AssignmentID: "asg-1", FromDepartureID: "dep-1", ToDepartureID: "dep-2"},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.departure.BookingMoved", "dep-2"},
	{"BookingFailed", volume.BookingFailed{Header: hdr, AssignmentID: "asg-1", DepartureID: "dep-1", Reason: volume.ReasonDepartureFull},
		wire.EventsTopic, "com.retailnetwork.logistics.linehaul-transportation.departure.BookingFailed", "dep-1"},
}

func encoder() wire.Encoder { return wire.Encoder{NewID: func() string { return fixedID }} }

// TestEveryEventHasAnExactGoldenEnvelope compares the Kafka value of each
// published event byte for byte with its golden file (UPDATE_GOLDEN=1 rewrites
// them; read the diff before committing).
func TestEveryEventHasAnExactGoldenEnvelope(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := encoder().Encode(c.event)
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, c.name, m.Value)
			assertMessage(t, m, c.topic, c.typ, c.key)
			assertDecodes(t, m.Value, c.typ, c.key)
		})
	}
}

func assertGolden(t *testing.T, name string, value []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, append(value, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(value)+"\n" {
		t.Fatalf("envelope differs from %s:\n got %s\nwant %s", path, value, want)
	}
}

func assertMessage(t *testing.T, m outbox.Message, topic, typ, key string) {
	t.Helper()
	if m.Topic != topic || m.EventType != typ || m.Subject != key || string(m.Key) != key || m.EventID != fixedID {
		t.Fatalf("message = %+v", m)
	}
	if len(m.Headers) != 1 || m.Headers[0].Key != "content-type" || m.Headers[0].Value != cloudevents.MediaType {
		t.Fatalf("headers = %+v", m.Headers)
	}
}

func assertDecodes(t *testing.T, value []byte, typ, key string) {
	t.Helper()
	ev, err := cloudevents.Decode(value)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type() != typ || ev.Source() != "/retailnetwork/linehaul-transportation" || ev.Subject() != key || !ev.Time().Equal(at) {
		t.Fatalf("decoded = %v", ev)
	}
	if ev.DataSchema() == "" || ev.DataContentType() != "application/json" {
		t.Fatalf("dataschema/datacontenttype of %v", ev)
	}
}

// TestTheIDIsMintedPerOccurrence proves two encodes of one event get two ids.
func TestTheIDIsMintedPerOccurrence(t *testing.T) {
	n := 0
	e := wire.Encoder{NewID: func() string { n++; return []string{"a", "b"}[n-1] }}
	m1, err1 := e.Encode(cases[0].event)
	m2, err2 := e.Encode(cases[0].event)
	if err1 != nil || err2 != nil || m1.EventID != "a" || m2.EventID != "b" {
		t.Fatalf("ids = %q %q (%v %v)", m1.EventID, m2.EventID, err1, err2)
	}
}

type unknownEvent struct{ shared.Header }

func (unknownEvent) EventName() string { return "Unknown" }
func (unknownEvent) Entity() string    { return "departure" }
func (unknownEvent) SubjectID() string { return "x" }

func TestAnEventWithoutAnEncodingIsRefused(t *testing.T) {
	if _, err := encoder().Encode(unknownEvent{}); err == nil {
		t.Fatal("an unmapped event must not be published silently")
	}
}

func TestAnEventWithoutASubjectIsRefused(t *testing.T) {
	_, err := encoder().Encode(departure.DepartureDeparted{Header: hdr})
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

// TestStreamsMatchTheCatalogue proves the stream of every event name.
func TestStreamsMatchTheCatalogue(t *testing.T) {
	analytics := map[string]bool{"DepartureDeparted": true, "DepartureArrived": true, "LoadClosed": true}
	for _, c := range cases {
		want := cloudevents.StreamEvents
		if analytics[c.event.EventName()] {
			want = cloudevents.StreamAnalytics
		}
		if got := wire.Stream(c.event.EventName()); got != want || wire.TopicOf(got) != c.topic {
			t.Errorf("%s: stream %s topic %s", c.name, got, wire.TopicOf(got))
		}
	}
}

func TestPayloadArraysAreNeverNull(t *testing.T) {
	m, err := encoder().Encode(load.LoadDeparted{Header: hdr, LoadID: "l", DepartureID: "d", OriginNodeID: "n", DepartedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	var env struct{ Data map[string]json.RawMessage }
	if err := json.Unmarshal(m.Value, &env); err != nil {
		t.Fatal(err)
	}
	if string(env.Data["trackingNumbers"]) != "[]" {
		t.Fatalf("trackingNumbers = %s", env.Data["trackingNumbers"])
	}
}
