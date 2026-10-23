package postgres

import (
	"context"
	"fmt"

	"github.com/claudioed/linehaul-transportation/internal/application/outbox"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// Encoder turns a domain event into its CloudEvents outbox message
// (internal/adapters/kafka/wire.Encoder implements it).
type Encoder interface {
	Encode(ev shared.Event) (outbox.Message, error)
}

// EventOutbox is the Postgres ports.EventOutbox: it encodes each event once
// and inserts the rows through the OutboxRepo, in the transaction the ctx
// carries.
type EventOutbox struct {
	repo    *OutboxRepo
	encoder Encoder
}

// NewEventOutbox constructs an EventOutbox.
func NewEventOutbox(repo *OutboxRepo, encoder Encoder) *EventOutbox {
	return &EventOutbox{repo: repo, encoder: encoder}
}

// Append implements ports.EventOutbox.
func (o *EventOutbox) Append(ctx context.Context, events ...shared.Event) error {
	msgs := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		m, err := o.encoder.Encode(ev)
		if err != nil {
			return fmt.Errorf("encode %s: %w", ev.EventName(), err)
		}
		msgs = append(msgs, m)
	}
	return o.repo.Insert(ctx, msgs...)
}
