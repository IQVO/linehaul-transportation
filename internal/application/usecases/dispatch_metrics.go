package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// rejectionReasons maps a dispatch failure to the problem slug the REST adapter
// answers with (same vocabulary as apis/openapi.yaml), in match order.
var rejectionReasons = []struct {
	err    error
	reason string
}{
	{ports.ErrNotFound, "not-found"},
	{shared.ErrValidation, "validation-failed"},
	{shared.ErrIllegalTransition, "illegal-transition"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// noMetrics is the LoadMetrics of a Deps without one.
type noMetrics struct{}

func (noMetrics) LoadDispatched(context.Context)               {}
func (noMetrics) LoadDispatchRejected(context.Context, string) {}

// metrics returns the configured LoadMetrics or a no-op.
func (d Deps) metrics() ports.LoadMetrics {
	if d.Metrics != nil {
		return d.Metrics
	}
	return noMetrics{}
}

// recordDispatch counts one dispatch attempt: rejected when err is not nil,
// dispatched when a load really left, nothing for a replay (dispatched false,
// no error).
func (d Deps) recordDispatch(ctx context.Context, dispatched bool, err error) {
	switch {
	case err != nil:
		d.metrics().LoadDispatchRejected(ctx, rejectionReason(err))
	case dispatched:
		d.metrics().LoadDispatched(ctx)
	}
}

func rejectionReason(err error) string {
	for _, r := range rejectionReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
