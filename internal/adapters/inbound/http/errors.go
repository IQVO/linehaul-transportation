package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
	"github.com/claudioed/linehaul-transportation/internal/domain/load"
	"github.com/claudioed/linehaul-transportation/internal/domain/schedule"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// problemBaseURI is the namespace of this service's RFC 7807 `type` URIs.
const problemBaseURI = "https://retailnetwork.example/problems/"

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type).
var errMalformedRequest = errors.New("request body is not valid JSON for this operation")

// errTooLarge marks a request body above maxBodyBytes (413 request-body-too-large).
var errTooLarge = errors.New("request body is too large")

// problem is the fixed (status, slug, title) of one error category; the
// detail comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the idempotency middleware writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key is invalid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
)

// Problems of the paged collection GET (fleet REST guideline sections 3 and 4).
var (
	invalidLimit          = problem{http.StatusBadRequest, "invalid-limit", "Invalid limit"}
	invalidCursor         = problem{http.StatusBadRequest, "invalid-cursor", "Invalid cursor"}
	unknownQueryParameter = problem{http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter"}
)

// problemCatalogue maps every typed error to its problem, in match order (the
// specific errors before the general ErrValidation / ErrIllegalTransition).
// Slugs and statuses are the table of ADR 0002.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{errTooLarge, problem{http.StatusRequestEntityTooLarge, "request-body-too-large", "Request body is too large"}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found"}},
	{departure.ErrInvalidDepartureTimes, problem{http.StatusUnprocessableEntity, "invalid-departure-times", "Departure times are invalid"}},
	{schedule.ErrOverlappingSlots, problem{http.StatusUnprocessableEntity, "overlapping-slots", "Slots of a lane overlap"}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
	{departure.ErrDepartureFull, problem{http.StatusConflict, "departure-full", "Departure is full"}},
	{departure.ErrLaneClosed, problem{http.StatusConflict, "lane-closed", "Lane is closed"}},
	{departure.ErrDepartureClosed, problem{http.StatusConflict, "departure-closed", "Departure is closed"}},
	{load.ErrPackageAlreadyLoaded, problem{http.StatusConflict, "package-already-loaded", "Package is already on an active load"}},
	{load.ErrLoadOverCapacity, problem{http.StatusConflict, "load-over-capacity", "Load is over capacity"}},
	{load.ErrLoadAlreadyExists, problem{http.StatusConflict, "load-already-exists", "The departure already has a load"}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "Illegal state transition"}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch it and retry"}},
}

// problemError is an error that already carries its problem (the adapter's own
// 400s for the paged collection).
type problemError struct {
	p      problem
	detail string
}

func (e problemError) Error() string { return e.detail }

// problemFor returns the problem of the first catalogue entry err matches,
// or the internal-error problem.
func problemFor(err error) problem {
	var pe problemError
	if errors.As(err, &pe) {
		return pe.p
	}
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
