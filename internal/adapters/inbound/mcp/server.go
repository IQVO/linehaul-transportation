// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0008-mcp-server-adoption.md the surface is READ-ONLY: every
// tool reads departure data (list_departures, get_departure); defining lane
// schedules, cancelling or re-capacitating departures and the whole load
// lifecycle stay on REST and on the event consumers, and governance_test.go
// fails the build on a write-verb tool name. This package depends inward on
// the application layer and the domain only, never on an outbound adapter or
// the REST adapter, and nothing else may depend on it
// (internal/architecture's TestMCPAdapterDependencyRule;
// TestMCPServerIsReadOnly pins that no write use case is reachable). The
// composition root (cmd/mcp) wires the concrete Postgres repositories into the
// read use cases. There is no auth of any kind (fleet-wide revert 2026-09-11;
// TestNoAuthMiddlewareReintroduced).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "linehaul-transportation-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Linehaul transportation of the retail network (read-only): page through the departure horizon with " +
	"list_departures (optionally by origin node, destination node and cutoffAt window from/to, newest windows first, " +
	"at most `limit` rows) or read one departure with get_departure -- its lane, kind (PICKUP, LINEHAUL or AIR), " +
	"origin and destination nodes, cutoff/departs/arrives instants, capacity, booked and remaining packages, its " +
	"state (SCHEDULED, CLOSED, DEPARTED, ARRIVED or CANCELLED) and, once it ended, the cancel reason and the " +
	"departed/arrived instants. Nothing here changes data: scheduling lanes, cancelling or re-capacitating a " +
	"departure and the load lifecycle are REST operations of linehaul-transportation, and departures are generated " +
	"by the horizon roll itself."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
