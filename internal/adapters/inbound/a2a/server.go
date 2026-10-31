// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter over the
// same application-layer read use cases the REST and MCP adapters use. It is
// built on the official A2A Go SDK (a2aproject/a2a-go/v2) and speaks JSON-RPC
// 2.0 over HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0011-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between bounded contexts: an agent client sends a goal-shaped skill request
// as a structured message part and this agent answers from its own state
// through its own use cases. It is not a data-sync channel: facts between
// contexts still travel exclusively as CloudEvents on Kafka, published through
// the transactional outbox.
//
// This agent is READ-ONLY, exactly like the MCP adapter (ADR 0008): both
// skills read departures. Scheduling lanes, cancelling or re-capacitating a
// departure and the load lifecycle stay REST operations. This package holds a
// read interface only, never calls another bounded context and contains no
// outbound A2A client (internal/architecture's TestA2AAdapterIsReadOnly).
//
// Like the MCP adapter, this package depends inward on the application layer
// and the domain only, never on an outbound adapter. The composition root
// (cmd/a2a) wires the concrete Postgres repository into the read use cases.
// There is no authentication of any kind (fleet-wide decision;
// TestNoAuthMiddlewareReintroduced).
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// DepartureReader is the read surface of the application layer this adapter
// may reach: exactly the two queries behind GET /departures and
// GET /departures/{departureId}. *usecases.Service (the same type the REST and
// MCP adapters serve) satisfies it; no write use case is reachable through it,
// which makes the read-only guarantee structural instead of a convention.
type DepartureReader interface {
	ListDepartures(ctx context.Context, q usecases.ListDeparturesQuery) (views.DepartureList, error)
	GetDeparture(ctx context.Context, id string) (views.Departure, error)
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries the SAME read use cases the REST and MCP adapters use; the
// adapter never constructs an outbound adapter itself and holds no write use
// case at all.
type Deps struct {
	Departures DepartureReader
}

// Card builds this agent's Agent Card (ADR 0011). The surface is two curated,
// goal-shaped READ skills derived from the MCP tools list_departures and
// get_departure (ADR 0008): this context's MCP charter is entirely read-only,
// so the agent is read-only too and never widens what the context exposes.
// The card declares no security schemes on purpose: this surface is
// unauthenticated by the same fleet-wide decision as REST and MCP.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "linehaul-transportation",
		Description: "Linehaul Transportation agent of the retail network (read-only): it answers which departures leave a node and how much capacity they still have, and where one departure stands (lane, kind, cutoff/departs/arrives, capacity, booked and remaining packages, state and the cancel/depart/arrive trail). It never schedules lanes, cancels or re-capacitates departures or changes a load: those stay REST operations of this context.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/linehaul-transportation",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillListDepartures,
				Name:        "Find departures and their remaining capacity",
				Description: "Read the departure horizon by cutoffAt (then departureId), optionally only departures leaving one origin node (origin), bound for one destination node (destination) and/or whose cutoffAt falls in a window [from, to) (RFC 3339 instants, from inclusive, to exclusive); at most limit rows (1..1000, default 500). Each departure carries its lane, kind (PICKUP, LINEHAUL, AIR), nodes, cutoff/departs/arrives, capacity, booked and remaining packages and state (SCHEDULED, CLOSED, DEPARTED, ARRIVED, CANCELLED). Persists nothing.",
				Tags:        []string{"linehaul", "departures", "capacity", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"list_departures","arguments":{}}`,
					`{"skill":"list_departures","arguments":{"origin":"n-a","from":"2026-10-24T08:00:00Z","to":"2026-10-25T08:00:00Z","limit":20}}`,
				},
			},
			{
				ID:          skillGetDeparture,
				Name:        "Look up where one departure stands",
				Description: "Read one departure by its departureId: lane, kind, origin and destination nodes, cutoffAt (no booking after it), departsAt, arrivesAt, capacityPackages, bookedPackages, remainingPackages and state, plus cancelReason and cancelledAt once cancelled, departedAt once it left and arrivedAt once it arrived. Answers a 404 problem when no departure has that id. Persists nothing.",
				Tags:        []string{"linehaul", "departures", "status", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"get_departure","arguments":{"departureId":"dep-7f3a"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response reads, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (ADR 0011), mirroring ADR 0008's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
