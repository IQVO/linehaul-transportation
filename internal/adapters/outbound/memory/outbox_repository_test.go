package memory_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/outbox"
)

func msg(eventID string) outbox.Message {
	return outbox.Message{EventID: eventID, EventType: "DepartureScheduled", Key: []byte("dep-1"), Value: []byte("{}")}
}

// Messages is the inspection helper the BDD suite and the relay tests read:
// it must return every row in insertion order, published or not, and the
// caller must not be able to mutate the store through it.
func TestOutboxMessagesReturnsEveryRowInInsertionOrder(t *testing.T) {
	r := memory.NewOutboxRepo()
	ctx := context.Background()
	if got := r.Messages(); len(got) != 0 {
		t.Fatalf("an empty outbox returned %d messages", len(got))
	}
	if err := r.Insert(ctx, msg("e-1"), msg("e-2")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := r.Drain(ctx, 1, 0, func(context.Context, outbox.Message) error { return nil }); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := r.Insert(ctx, msg("e-3")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got := r.Messages()
	want := []string{"e-1", "e-2", "e-3"}
	if len(got) != len(want) {
		t.Fatalf("Messages() = %d rows, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].EventID != id {
			t.Errorf("Messages()[%d] = %s, want %s (insertion order, published or not)", i, got[i].EventID, id)
		}
	}
	got[0].EventID = "mutated"
	if r.Messages()[0].EventID != "e-1" {
		t.Error("mutating the returned slice changed the store: Messages must hand out a copy")
	}
}

// Snapshot restores the outbox for a scenario's teardown (the BDD world and
// the unit-of-work rollback use it): everything after the snapshot is undone.
func TestOutboxSnapshotRestoresRowsAndTheirState(t *testing.T) {
	r := memory.NewOutboxRepo()
	ctx := context.Background()
	if err := r.Insert(ctx, msg("e-1")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	restore := r.Snapshot()

	if err := r.Insert(ctx, msg("e-2")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := r.Drain(ctx, 10, 0, func(context.Context, outbox.Message) error {
		return errors.New("send failed")
	}); err == nil {
		t.Fatal("drain should have failed")
	}
	if r.Unpublished() != 2 || r.LastError(0) == "" {
		t.Fatalf("after the failed drain: unpublished = %d, lastError(0) = %q", r.Unpublished(), r.LastError(0))
	}

	restore()
	if ids := messageIDs(r.Messages()); !reflect.DeepEqual(ids, []string{"e-1"}) {
		t.Fatalf("after restore Messages = %v, want only the row the snapshot held", ids)
	}
	if r.Unpublished() != 1 {
		t.Fatalf("after restore Unpublished = %d, want 1", r.Unpublished())
	}
	if r.LastError(0) != "" {
		t.Fatalf("after restore LastError(0) = %q, want the clean stored state", r.LastError(0))
	}
}

func messageIDs(ms []outbox.Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.EventID
	}
	return out
}
