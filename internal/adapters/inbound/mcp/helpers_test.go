package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/mcp"
	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/network"
)

// now is the fixture's fixed "now".
var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// harness is a connected in-memory MCP client over the real server wired to
// the SAME application-layer read methods the REST adapter calls, exactly as
// cmd/mcp wires them (minus Postgres). The write side of the store exists
// ONLY here, to seed data the way the REST adapter and the consumers would:
// the MCP surface itself has none.
type harness struct {
	session *sdk.ClientSession
	store   *memory.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore()
	service := usecases.New(usecases.Deps{Departures: store.Departures()})
	deps := inboundmcp.Deps{Departures: service}
	return &harness{session: connectSession(t, deps), store: store}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// seedDeparture schedules one SCHEDULED departure on lane-1 (n-a -> n-b) whose
// cutoffAt is now plus in, the way the horizon roll's write path would.
func (h *harness) seedDeparture(t *testing.T, id string, in time.Duration, capacity int) {
	t.Helper()
	d, _, err := departure.Schedule(departure.Spec{
		ID: departure.ID(id), LaneID: "lane-1", Kind: departure.KindLinehaul, OriginNodeID: "n-a", DestinationNodeID: "n-b",
		OriginType: network.SortationCenter, DestinationType: network.SortationCenter,
		CutoffAt: now.Add(in), DepartsAt: now.Add(in), ArrivesAt: now.Add(in + time.Hour), CapacityPackages: capacity,
	}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.Departures().Save(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
