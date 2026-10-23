// Package wire turns a domain event into the outbox message of its CloudEvents
// envelope: the type, the stream (topic), the key and the camelCase `data` of
// ADR 0004 and apis/asyncapi.yaml. The CloudEvents id is minted here, once, and
// persisted with the outbox row, so a relay retry republishes the same id.
package wire

import (
	"fmt"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/linehaul-transportation/internal/application/outbox"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
	"github.com/claudioed/linehaul-transportation/internal/domain/volume"
)

// Topic names of the two streams.
const (
	EventsTopic    = "retailnetwork." + cloudevents.Context + "." + cloudevents.StreamEvents
	AnalyticsTopic = "retailnetwork." + cloudevents.Context + "." + cloudevents.StreamAnalytics
)

// schemaVersion is the dataschema version of every type published today.
const schemaVersion = 1

// analyticsOnly lists the events that go to the analytics stream only.
var analyticsOnly = map[string]bool{"DepartureDeparted": true, "DepartureArrived": true, "LoadClosed": true}

// Encoder builds outbox messages. NewID mints the CloudEvents id of one occurrence.
type Encoder struct {
	NewID func() string
}

// Stream returns the stream an event is published on.
func Stream(eventName string) string {
	if analyticsOnly[eventName] {
		return cloudevents.StreamAnalytics
	}
	return cloudevents.StreamEvents
}

// TopicOf returns the topic of a stream.
func TopicOf(stream string) string {
	if stream == cloudevents.StreamAnalytics {
		return AnalyticsTopic
	}
	return EventsTopic
}

// Encode returns the outbox message of one event.
func (e Encoder) Encode(ev shared.Event) (outbox.Message, error) {
	data, err := dataOf(ev)
	if err != nil {
		return outbox.Message{}, err
	}
	stream := Stream(ev.EventName())
	id := e.NewID()
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(),
		Time: ev.OccurredAt(), Stream: stream, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, err
	}
	h := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID:    id,
		Topic:      TopicOf(stream),
		EventType:  cloudevents.Type(ev.Entity(), ev.EventName()),
		Subject:    ev.SubjectID(),
		Key:        []byte(ev.SubjectID()),
		DataSchema: cloudevents.DataSchema(stream, ev.EventName(), schemaVersion),
		Value:      value,
		Headers:    []outbox.Header{{Key: h.Key, Value: string(h.Value)}},
	}, nil
}

func utc(t time.Time) time.Time { return t.UTC() }

// strs returns s, or an empty non-nil slice: arrays are always present.
func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func dataOf(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case departure.DepartureScheduled:
		return scheduled{string(e.DepartureID), e.LaneID, string(e.Kind), e.OriginNodeID, e.DestinationNodeID,
			utc(e.CutoffAt), utc(e.DepartsAt), utc(e.ArrivesAt), e.CapacityPackages}, nil
	case departure.DepartureCapacityChanged:
		return capacityChanged{string(e.DepartureID), e.CapacityPackages, e.BookedPackages, e.RemainingPackages}, nil
	case departure.DepartureCancelled:
		return cancelled{string(e.DepartureID), e.Reason, utc(e.CancelledAt)}, nil
	case departure.DepartureDeparted:
		return departed{string(e.DepartureID), utc(e.DepartedAt)}, nil
	case departure.DepartureArrived:
		return arrived{string(e.DepartureID), utc(e.ArrivedAt)}, nil
	case load.LoadClosed:
		return loadClosed{string(e.LoadID), string(e.DepartureID), e.PackageCount}, nil
	case load.LoadDeparted:
		return loadDeparted{string(e.LoadID), string(e.DepartureID), e.OriginNodeID, strs(e.TrackingNumbers),
			utc(e.DepartedAt), string(e.DepartureKind), string(e.NodeType)}, nil
	case load.LoadArrived:
		return loadArrived{string(e.LoadID), string(e.DepartureID), e.DestinationNodeID, strs(e.TrackingNumbers),
			utc(e.ArrivedAt), string(e.DepartureKind), string(e.NodeType)}, nil
	case volume.BookingMoved:
		return bookingMoved{e.AssignmentID, string(e.FromDepartureID), string(e.ToDepartureID)}, nil
	case volume.BookingFailed:
		return bookingFailed{e.AssignmentID, string(e.DepartureID), string(e.Reason)}, nil
	}
	return nil, fmt.Errorf("wire: no encoding for event %T", ev)
}

type scheduled struct {
	DepartureID       string    `json:"departureId"`
	LaneID            string    `json:"laneId"`
	Kind              string    `json:"kind"`
	OriginNodeID      string    `json:"originNodeId"`
	DestinationNodeID string    `json:"destinationNodeId"`
	CutoffAt          time.Time `json:"cutoffAt"`
	DepartsAt         time.Time `json:"departsAt"`
	ArrivesAt         time.Time `json:"arrivesAt"`
	CapacityPackages  int       `json:"capacityPackages"`
}

type capacityChanged struct {
	DepartureID       string `json:"departureId"`
	CapacityPackages  int    `json:"capacityPackages"`
	BookedPackages    int    `json:"bookedPackages"`
	RemainingPackages int    `json:"remainingPackages"`
}

type cancelled struct {
	DepartureID string    `json:"departureId"`
	Reason      string    `json:"reason"`
	CancelledAt time.Time `json:"cancelledAt"`
}

type departed struct {
	DepartureID string    `json:"departureId"`
	DepartedAt  time.Time `json:"departedAt"`
}

type arrived struct {
	DepartureID string    `json:"departureId"`
	ArrivedAt   time.Time `json:"arrivedAt"`
}

type loadClosed struct {
	LoadID       string `json:"loadId"`
	DepartureID  string `json:"departureId"`
	PackageCount int    `json:"packageCount"`
}

type loadDeparted struct {
	LoadID          string    `json:"loadId"`
	DepartureID     string    `json:"departureId"`
	OriginNodeID    string    `json:"originNodeId"`
	TrackingNumbers []string  `json:"trackingNumbers"`
	DepartedAt      time.Time `json:"departedAt"`
	DepartureKind   string    `json:"departureKind"`
	NodeType        string    `json:"nodeType"`
}

type loadArrived struct {
	LoadID            string    `json:"loadId"`
	DepartureID       string    `json:"departureId"`
	DestinationNodeID string    `json:"destinationNodeId"`
	TrackingNumbers   []string  `json:"trackingNumbers"`
	ArrivedAt         time.Time `json:"arrivedAt"`
	DepartureKind     string    `json:"departureKind"`
	NodeType          string    `json:"nodeType"`
}

type bookingMoved struct {
	AssignmentID    string `json:"assignmentId"`
	FromDepartureID string `json:"fromDepartureId"`
	ToDepartureID   string `json:"toDepartureId"`
}

type bookingFailed struct {
	AssignmentID string `json:"assignmentId"`
	DepartureID  string `json:"departureId"`
	Reason       string `json:"reason"`
}
