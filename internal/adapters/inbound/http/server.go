// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 9457 problem documents for every error, the
// Idempotency-Key middleware on every POST, and the liveness/readiness
// endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/linehaul-transportation/internal/adapters/inbound/requestlog"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
)

// Server holds the use cases the handlers call.
type Server struct {
	Service *usecases.Service

	// Idempotency wraps every POST (the fleet Idempotency-Key middleware, built
	// by cmd/ over the Postgres pool); the shipped binary always sets it, nil
	// (handler unit tests) leaves the routes unwrapped.
	Idempotency Idempotency
	// Readiness backs GET /readyz: the drain gate plus the Postgres and Kafka
	// checks (NewReadiness). The shipped binary always builds it with
	// NewReadiness; nil (handler unit tests) means always ready.
	Readiness *Readiness
}

// DefaultServiceName names the server spans and the HTTP metrics when the
// composition root passes no service name.
const DefaultServiceName = "linehaul-transportation"

// NewRouter builds the chi router for every operation of apis/openapi.yaml. A nil
// logger defaults to slog.Default(); an empty serviceName to DefaultServiceName.
//
// Middleware order is the fleet observability standard: RequestID, then otelchi
// (the server span, named after the chi route pattern), then the
// request-duration histogram, then RequestLogger (it runs inside the span, so
// its record carries trace_id and span_id), then Recoverer. telemetry.Setup must
// have run before this call: otelchimetric binds the global MeterProvider when
// it is constructed.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	// A NUL (U+0000) in the path, the query or a body is a uniform client error
	// here, never a Postgres 22021 -> 500; it runs before the idempotency
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	s.keyed(r, http.MethodPost, "/lane-schedules", s.handleDefineLaneSchedule)
	s.keyed(r, http.MethodPost, "/lane-schedules/{laneId}/retirements", s.handleRetireLaneSchedule)
	s.keyed(r, http.MethodPost, "/departures/{departureId}/cancellations", s.handleCancelDeparture)
	r.Put("/departures/{departureId}/capacity", s.handleChangeDepartureCapacity)
	s.keyed(r, http.MethodPost, "/loads", s.handleOpenLoad)
	s.keyed(r, http.MethodPost, "/loads/{loadId}/packages", s.handleAddPackage)
	s.keyed(r, http.MethodPost, "/loads/{loadId}/closures", s.handleCloseLoad)
	s.keyed(r, http.MethodPost, "/loads/{loadId}/dispatches", s.handleDispatchLoad)
	s.keyed(r, http.MethodPost, "/loads/{loadId}/receipts", s.handleReceiveLoad)
	r.Get("/departures", s.handleListDepartures)
	r.Get("/departures/{departureId}", s.handleGetDeparture)
	return r
}

// keyed registers a state-changing route behind the Idempotency-Key middleware
// (scope: the route pattern, e.g. "POST /loads/{loadId}/closures"); a nil
// Idempotency (handler unit tests only) leaves it unwrapped.
func (s *Server) keyed(r chi.Router, method, pattern string, h http.HandlerFunc) {
	if s.Idempotency == nil {
		r.MethodFunc(method, pattern, h)
		return
	}
	r.With(s.Idempotency(method+" "+pattern)).MethodFunc(method, pattern, h)
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
