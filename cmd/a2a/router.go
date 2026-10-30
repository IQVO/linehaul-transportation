package main

import (
	"log/slog"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	inbounda2a "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/a2a"
	"github.com/claudioed/linehaul-transportation/internal/adapters/inbound/requestlog"
)

// newAgent composes the A2A surface over the read side of the service: the
// SAME read use cases the REST and MCP adapters use (deps), the Agent Card
// advertising agentURL, and the HTTP router around them. The adapter never
// writes, so no EventPublisher, UnitOfWork or Clock is wired, and it calls no
// other context.
func newAgent(deps inbounda2a.Deps, agentURL, version string, logger *slog.Logger, serviceName string) http.Handler {
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: agentURL, Version: version})
	return newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), card, logger, serviceName)
}

// newRouter wraps the A2A handler in the process's HTTP surface:
//
//   - GET /healthz answers 200 {"status":"ok"}, so the Kubernetes
//     liveness/readiness probes have a cheap target. A2A is unauthenticated by
//     decision, so probes need no credentials, and the route leaks no skill,
//     resource or state.
//   - The Agent Card is served at the well-known path
//     /.well-known/agent-card.json (the discovery contract, ADR 0011).
//   - The JSON-RPC endpoint is mounted at BOTH "/" (the canonical root) and
//     "/a2a" (the fleet's <CTX>_A2A_ENDPOINT convention, mirroring cmd/mcp's
//     dual "/mcp" mount).
//
// The middleware order is the fleet observability standard, identical to the
// REST and MCP routers: RequestID, otelchi, the request-duration histogram,
// RequestLogger, Recoverer. telemetry.Setup must have run before this call.
//
// No auth middleware is ever added here (TestNoAuthMiddlewareReintroduced in
// internal/architecture).
func newRouter(a2aHandler http.Handler, card *a2a.AgentCard, logger *slog.Logger, serviceName string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	r.Mount("/a2a", a2aHandler)
	r.Mount("/", a2aHandler)
	return r
}
