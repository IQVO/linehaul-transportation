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
	Idempotency func(http.Handler) http.Handler
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

	r.Group(func(r chi.Router) {
		if s.Idempotency != nil {
			r.Use(s.Idempotency)
		}
		r.Post("/lane-schedules", s.handleDefineLaneSchedule)
		r.Post("/lane-schedules/{laneId}:retire", s.handleRetireLaneSchedule)
		r.Post("/departures/{departureId}:cancel", s.handleCancelDeparture)
		r.Post("/departures/{departureId}:change-capacity", s.handleChangeDepartureCapacity)
		r.Post("/loads", s.handleOpenLoad)
		r.Post("/loads/{loadId}/packages", s.handleAddPackage)
		r.Post("/loads/{loadId}:close", s.handleCloseLoad)
		r.Post("/loads/{loadId}:dispatch", s.handleDispatchLoad)
		r.Post("/loads/{loadId}:receive", s.handleReceiveLoad)
	})
	r.Get("/departures", s.handleListDepartures)
	r.Get("/departures/{departureId}", s.handleGetDeparture)
	return r
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
