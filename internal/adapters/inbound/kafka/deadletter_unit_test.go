package kafka

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/cloudevents"
)

// The dead-letter tests of deadletter_test.go drive the run loop with an
// injected deadLetter func. This file unit-tests the real publish path
// (publishDeadLetter -> writeDLQ) and Consumer.Run's wiring of the two.

// scriptDLQ is a DeadLetterWriter that records every message it is given and
// answers from a script of errors, one per attempt.
type scriptDLQ struct {
	msgs     []kafkago.Message
	errs     []error
	attempts atomic.Int64
}

func (w *scriptDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	i := int(w.attempts.Add(1)) - 1
	if i < len(w.errs) && w.errs[i] != nil {
		return w.errs[i]
	}
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *scriptDLQ) Close() error { return nil }

func headerOf(msg kafkago.Message, key string) (string, bool) {
	for _, h := range msg.Headers {
		if h.Key == key {
			return string(h.Value), true
		}
	}
	return "", false
}

func TestWriteDLQ(t *testing.T) {
	t.Run("publishes on the first attempt", func(t *testing.T) {
		w := &scriptDLQ{}
		if err := writeDLQ(context.Background(), w, kafkago.Message{Topic: "t.dlq"}); err != nil {
			t.Fatalf("writeDLQ = %v, want nil", err)
		}
		if len(w.msgs) != 1 || int(w.attempts.Load()) != 1 {
			t.Fatalf("attempts = %d, stored = %d, want one message on one attempt", w.attempts.Load(), len(w.msgs))
		}
	})

	t.Run("a permanent error is returned at once", func(t *testing.T) {
		boom := errors.New("broker down")
		w := &scriptDLQ{errs: []error{boom}}
		if err := writeDLQ(context.Background(), w, kafkago.Message{}); !errors.Is(err, boom) {
			t.Fatalf("writeDLQ = %v, want the broker error", err)
		}
		if int(w.attempts.Load()) != 1 {
			t.Fatalf("attempts = %d, want no retry behind a permanent error", w.attempts.Load())
		}
	})

	t.Run("retries while the auto-created topic has no leader", func(t *testing.T) {
		w := &scriptDLQ{errs: []error{kafkago.UnknownTopicOrPartition, kafkago.LeaderNotAvailable}}
		if err := writeDLQ(context.Background(), w, kafkago.Message{}); err != nil {
			t.Fatalf("writeDLQ = %v, want nil once the topic is ready", err)
		}
		if int(w.attempts.Load()) != 3 || len(w.msgs) != 1 {
			t.Fatalf("attempts = %d stored = %d, want the two failures then one publish", w.attempts.Load(), len(w.msgs))
		}
	})
}

func TestWriteDLQStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &scriptDLQ{errs: []error{kafkago.UnknownTopicOrPartition}}
	done := make(chan struct{})
	go func() {
		for w.attempts.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
		close(done)
	}()
	err := writeDLQ(ctx, w, kafkago.Message{})
	<-done
	if !errors.Is(err, kafkago.UnknownTopicOrPartition) || !errors.Is(err, context.Canceled) {
		t.Fatalf("writeDLQ = %v, want the joined not-ready and cancel errors", err)
	}
	if len(w.msgs) != 0 {
		t.Fatal("nothing may be published once the context is cancelled")
	}
}

// runUntilCommit runs c until the reader has committed want commits, then
// cancels the context and returns Run's result.
func runUntilCommit(t *testing.T, c *Consumer, rec *loopRecorder, want int) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for countCommits(rec) < want {
		select {
		case err := <-done:
			t.Fatalf("Run returned early: %v (events=%v)", err, rec.log())
		case <-deadline:
			t.Fatalf("timed out waiting for %d commits; events=%v", want, rec.log())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
		return nil
	}
}

func TestIsTopicNotReady(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unknown topic", kafkago.UnknownTopicOrPartition, true},
		{"no leader yet", kafkago.LeaderNotAvailable, true},
		{"broker error", errors.New("broker down"), false},
		{"write errors, all not ready", kafkago.WriteErrors{kafkago.UnknownTopicOrPartition, kafkago.LeaderNotAvailable}, true},
		{"write errors, one permanent", kafkago.WriteErrors{kafkago.UnknownTopicOrPartition, errors.New("broker down")}, false},
		{"empty write errors", kafkago.WriteErrors{}, false},
		{"wrapped unknown topic", errors.Join(kafkago.UnknownTopicOrPartition), true},
		{"a join that still has a not-ready member stays transient", errors.Join(errors.New("x"), kafkago.LeaderNotAvailable), true},
		{"wrapped broker error", errors.Join(errors.New("x"), errors.New("y")), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTopicNotReady(tc.err); got != tc.want {
				t.Errorf("isTopicNotReady(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestPublishDeadLetter(t *testing.T) {
	cause := errors.New("handler failed 5 times")
	original := kafkago.Message{
		Topic:     "retailnetwork.order-sourcing.events",
		Partition: 3,
		Offset:    42,
		Key:       []byte("as-1"),
		Value:     []byte("payload"),
		Headers:   []kafkago.Header{{Key: "content-type", Value: []byte("application/cloudevents+json")}},
	}
	w := &scriptDLQ{}

	if err := publishDeadLetter(context.Background(), w, "retailnetwork.order-sourcing.events.dlq", original, cause); err != nil {
		t.Fatalf("publishDeadLetter = %v, want nil", err)
	}
	if len(w.msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(w.msgs))
	}
	got := w.msgs[0]
	if got.Topic != "retailnetwork.order-sourcing.events.dlq" {
		t.Errorf("topic = %q, want the dlq topic", got.Topic)
	}
	if string(got.Key) != "as-1" || string(got.Value) != "payload" {
		t.Errorf("key/value = %q/%q, want the original's", got.Key, got.Value)
	}
	for key, want := range map[string]string{
		"x-dlq-source-topic":     "retailnetwork.order-sourcing.events",
		"x-dlq-source-partition": "3",
		"x-dlq-source-offset":    "42",
		"x-dlq-error":            "handler failed 5 times",
	} {
		v, ok := headerOf(got, key)
		if !ok || v != want {
			t.Errorf("header %s = (%q, %v), want %q", key, v, ok, want)
		}
	}
	failedAt, ok := headerOf(got, "x-dlq-failed-at")
	if !ok {
		t.Fatal("the x-dlq-failed-at header is missing")
	}
	if ts, err := time.Parse(time.RFC3339, failedAt); err != nil || ts.IsZero() {
		t.Errorf("x-dlq-failed-at = %q, want an RFC3339 instant", failedAt)
	}
	if _, ok := headerOf(got, "content-type"); !ok {
		t.Error("the original message's headers must be carried along")
	}
	// The source message must not be mutated by the header append.
	if n := len(original.Headers); n != 1 {
		t.Errorf("the original message gained headers (now %d), want its own 1", n)
	}
}

// TestConsumerRunDeadLettersToTheLaneTopic pins Consumer.Run's wiring: the
// loop retries a transient failure exactly domainMaxHandlerAttempts times,
// then publishes to Topic+DLQSuffix through the REAL publishDeadLetter (the
// message carries the x-dlq-* context) and commits past the message, so the
// partition is never blocked.
func TestConsumerRunDeadLettersToTheLaneTopic(t *testing.T) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID: "b1c2d3e4-0000-4000-8000-000000000001", Entity: "orders", EventName: "FulfillmentLocked",
		Subject: "as-1", Time: time.Now(), Stream: cloudevents.StreamEvents, Data: map[string]any{"assignmentId": "as-1"},
	})
	if err != nil {
		t.Fatalf("building the CloudEvent: %v", err)
	}
	rec := &loopRecorder{}
	msg := kafkago.Message{Topic: "retailnetwork.order-sourcing.events", Partition: 1, Offset: 7, Key: []byte("as-1"), Value: value}
	handlerCalls := 0
	dlq := &scriptDLQ{}
	c := &Consumer{
		Name:   "order-sourcing",
		Topic:  "retailnetwork.order-sourcing.events",
		Reader: &scriptedReader{rec: rec, msgs: []kafkago.Message{msg}},
		Handlers: map[string]HandlerFunc{
			cloudevents.Type("orders", "FulfillmentLocked"): func(context.Context, ce.Event) error {
				handlerCalls++
				return errors.New("transient")
			},
		},
		Logger: quietDeadLetterLogger(),
		DLQ:    dlq,
		sleep:  func(context.Context, time.Duration) error { return nil },
	}

	if err := runUntilCommit(t, c, rec, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled after shutdown", err)
	}

	if handlerCalls != domainMaxHandlerAttempts {
		t.Fatalf("handler called %d times, want exactly domainMaxHandlerAttempts=%d", handlerCalls, domainMaxHandlerAttempts)
	}
	if len(dlq.msgs) != 1 {
		t.Fatalf("dead-lettered %d messages, want 1", len(dlq.msgs))
	}
	dead := dlq.msgs[0]
	if dead.Topic != "retailnetwork.order-sourcing.events.dlq" {
		t.Errorf("dlq topic = %q, want the consumed topic + %s", dead.Topic, DLQSuffix)
	}
	if v, ok := headerOf(dead, "x-dlq-source-offset"); !ok || v != "7" {
		t.Errorf("x-dlq-source-offset = (%q, %v), want 7", v, ok)
	}
	if _, ok := headerOf(dead, "x-dlq-error"); !ok {
		t.Error("the dead-lettered message must carry the failure cause")
	}
	if commits := countCommits(rec); commits != 1 {
		t.Errorf("commits = %d, want the offset committed once after the dead-letter", commits)
	}
}

func countCommits(rec *loopRecorder) int {
	n := 0
	for _, e := range rec.log() {
		if len(e) >= 6 && e[:6] == "commit" {
			n++
		}
	}
	return n
}
