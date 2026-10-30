package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// DepartureReader is the read surface of the application layer this adapter
// may reach: exactly the two queries behind GET /departures and
// GET /departures/{departureId}. *usecases.Service (the same instance the
// REST adapter serves) satisfies it; no write use case is reachable through
// it, which is what makes the read-only guarantee structural instead of a
// convention (internal/architecture's TestMCPServerIsReadOnly pins both the
// field set of Deps and the methods of every interface it holds).
type DepartureReader interface {
	ListDepartures(ctx context.Context, q usecases.ListDeparturesQuery) (views.DepartureList, error)
	GetDeparture(ctx context.Context, id string) (views.Departure, error)
}

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use cases the REST adapter exposes; the adapter
// never constructs an outbound adapter itself, and it holds no write use
// case at all (docs/adr/0008).
type Deps struct {
	Departures DepartureReader
}

// --- inputs -------------------------------------------------------------------

type departureIDInput struct {
	DepartureID string `json:"departureId" jsonschema:"the departure id, e.g. dep-7f3a"`
}

type listDeparturesInput struct {
	Origin      string `json:"origin,omitempty" jsonschema:"optional: keep only departures leaving this origin node id; omitted means every origin"`
	Destination string `json:"destination,omitempty" jsonschema:"optional: keep only departures bound for this destination node id; omitted means every destination"`
	From        string `json:"from,omitempty" jsonschema:"optional: earliest cutoffAt (an RFC 3339 instant such as 2026-10-24T08:00:00Z), inclusive; omitted means unbounded"`
	To          string `json:"to,omitempty" jsonschema:"optional: latest cutoffAt (an RFC 3339 instant), exclusive; omitted means unbounded"`
	Limit       int    `json:"limit,omitempty" jsonschema:"optional: at most this many departures, 1..1000; omitted or 0 means the default of 500"`
}

// --- handlers -----------------------------------------------------------------

// listDepartures mirrors GET /departures: filters are ANDed, from is inclusive
// and to exclusive on cutoffAt, ordered by cutoffAt then departureId. A from
// or to that is not an RFC 3339 instant is refused the same way the REST
// adapter refuses it (a validation error), and from after to likewise.
func (d Deps) listDepartures(ctx context.Context, in listDeparturesInput) (views.DepartureList, error) {
	q := usecases.ListDeparturesQuery{Origin: in.Origin, Destination: in.Destination, Limit: in.Limit}
	for name, raw := range map[string]string{"from": in.From, "to": in.To} {
		if raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return views.DepartureList{}, mapError(ctx, shared.Invalid("%s must be an RFC 3339 instant", name))
		}
		if name == "from" {
			q.From = &t
		} else {
			q.To = &t
		}
	}
	list, err := d.Departures.ListDepartures(ctx, q)
	if err != nil {
		return views.DepartureList{}, mapError(ctx, err)
	}
	return list, nil
}

// getDeparture mirrors GET /departures/{departureId}.
func (d Deps) getDeparture(ctx context.Context, in departureIDInput) (views.Departure, error) {
	v, err := d.Departures.GetDeparture(ctx, in.DepartureID)
	if err != nil {
		return views.Departure{}, mapError(ctx, err)
	}
	return v, nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the two read-only tools. Every tool is annotated
// read-only, idempotent and closed-world; TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0008).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

	addTool(server, &mcp.Tool{
		Name: "list_departures",
		Description: "List departures of the horizon by cutoffAt (then departureId), a page at a time, optionally only " +
			"those leaving one origin (origin), bound for one destination (destination) and/or whose cutoffAt falls in a " +
			"window [from, to) (RFC 3339 instants, from inclusive, to exclusive); at most limit rows (1..1000, default " +
			"500). Each departure carries its lane, kind (PICKUP, LINEHAUL, AIR), origin and destination nodes, " +
			"cutoff/departs/arrives, capacity, booked and remaining packages and its state (SCHEDULED, CLOSED, DEPARTED, " +
			"ARRIVED, CANCELLED). Read-only; a limit outside 1..1000 or a from after to fails with validation-failed, " +
			"and a from/to that is not an RFC 3339 instant likewise.",
		Annotations: readOnly,
	}, d.listDepartures)

	addTool(server, &mcp.Tool{
		Name: "get_departure",
		Description: "Read one departure by id: its lane, kind (PICKUP, LINEHAUL or AIR), origin and destination nodes, " +
			"cutoffAt (no booking after it), departsAt and arrivesAt, capacityPackages, bookedPackages, " +
			"remainingPackages and its state (SCHEDULED, CLOSED, DEPARTED, ARRIVED or CANCELLED), plus cancelReason " +
			"and cancelledAt once it was cancelled, departedAt once it left and arrivedAt once it arrived. Read-only; " +
			"an unknown id fails with not-found.",
		Annotations: readOnly,
	}, d.getDeparture)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
