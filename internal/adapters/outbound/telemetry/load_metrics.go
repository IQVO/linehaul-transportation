package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/linehaul-transportation"

// loadsDispatchedName is the Tier 2 business metric of the context (the fleet's
// standard metrics convention, ADR 0010): every load dispatch attempt, by outcome.
const loadsDispatchedName = "linehaul.loads.dispatched"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeDispatched = "dispatched"
	outcomeRejected   = "rejected"
)

// LoadMetrics implements ports.LoadMetrics against the global MeterProvider.
// Until Setup installs a real provider the global one is a no-op, so recording
// is cheap and safe in tests and local runs.
type LoadMetrics struct {
	counter metric.Int64Counter
}

var _ ports.LoadMetrics = (*LoadMetrics)(nil)

// NewLoadMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewLoadMetrics() (*LoadMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		loadsDispatchedName,
		metric.WithDescription("Load dispatch attempts by outcome (dispatched or rejected; reason says why a rejection happened, "+
			"as a problem slug such as illegal-transition or not-found). "+
			"A falling dispatched share with a rising illegal-transition means operators try to dispatch loads that were never closed, "+
			"so freight is not leaving its origin node on schedule; a rising internal-error means the service itself is failing."),
		metric.WithUnit("{load}"),
	)
	if err != nil {
		return nil, err
	}
	return &LoadMetrics{counter: counter}, nil
}

// LoadDispatched counts a load that left its origin node.
func (m *LoadMetrics) LoadDispatched(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeDispatched)))
}

// LoadDispatchRejected counts a dispatch that moved no load, with the reason why.
func (m *LoadMetrics) LoadDispatchRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}
