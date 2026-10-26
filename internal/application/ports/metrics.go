package ports

import "context"

// LoadMetrics records the business outcome of every load dispatch (Tier 2 of
// the fleet metrics convention). DispatchLoad calls it once per attempt that
// had something to do: LoadDispatched when a CLOSED load became DEPARTED,
// LoadDispatchRejected otherwise, with the problem slug that says why (the
// `type` suffix the REST adapter answers with: "illegal-transition",
// "not-found", ...). Dispatching a load that already departed is a no-op, not
// an attempt. The implementation lives in internal/adapters/outbound/telemetry;
// a nil Deps.Metrics records nothing.
type LoadMetrics interface {
	// LoadDispatched counts a load that left its origin node.
	LoadDispatched(ctx context.Context)
	// LoadDispatchRejected counts a dispatch that moved no load; reason is a
	// stable, low-cardinality slug.
	LoadDispatchRejected(ctx context.Context, reason string)
}
