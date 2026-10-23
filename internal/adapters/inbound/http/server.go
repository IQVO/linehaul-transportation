// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 9457 problem documents for every error, the
// Idempotency-Key middleware on every POST, and the liveness/readiness
// endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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

// NewRouter builds the chi router for every operation of apis/openapi.yaml.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	// A NUL (U+0000) in the path, the query or a body is a uniform client error
	// here, never a Postgres 22021 -> 500; it runs before the idempotency
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	s.keyed(r, http.MethodPost, "/lane-schedules", s.handleDefineLaneSchedule)
	s.keyed(r, http.MethodPost, "/lane-schedules/{laneId}:retire", s.handleRetireLaneSchedule)
	s.keyed(r, http.MethodPost, "/departures/{departureId}:cancel", s.handleCancelDeparture)
	s.keyed(r, http.MethodPost, "/departures/{departureId}:change-capacity", s.handleChangeDepartureCapacity)
	s.keyed(r, http.MethodPost, "/loads", s.handleOpenLoad)
	s.keyed(r, http.MethodPost, "/loads/{loadId}/packages", s.handleAddPackage)
	s.keyed(r, http.MethodPost, "/loads/{loadId}:close", s.handleCloseLoad)
	s.keyed(r, http.MethodPost, "/loads/{loadId}:dispatch", s.handleDispatchLoad)
	s.keyed(r, http.MethodPost, "/loads/{loadId}:receive", s.handleReceiveLoad)
	r.Get("/departures", s.handleListDepartures)
	r.Get("/departures/{departureId}", s.handleGetDeparture)
	return r
}

// keyed registers a state-changing route behind the Idempotency-Key middleware
// (scope: the route pattern, e.g. "POST /loads/{loadId}:close"); a nil
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
