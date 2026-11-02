package http

import (
	"net/http"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http/page"
	"github.com/claudioed/linehaul-transportation/internal/application/repository"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/application/views"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// handleListDepartures serves GET /departures: filters origin, destination,
// from and to, keyset-paged by (cutoffAt, departureId) as {items,nextCursor}.
func (s *Server) handleListDepartures(w http.ResponseWriter, r *http.Request) {
	q, err := listQuery(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, next, err := s.Service.ListDeparturesPage(r.Context(), q)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := page.Page[views.Departure]{Items: v.Departures}
	if out.Items == nil {
		out.Items = []views.Departure{}
	}
	if next != nil {
		out.NextCursor = page.Encode(page.Position{Sort: next.CutoffAt.UTC().Format(time.RFC3339Nano), ID: next.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

func listQuery(r *http.Request) (usecases.ListDeparturesQuery, error) {
	if name := page.Unknown(r, "origin", "destination", "from", "to"); name != "" {
		return usecases.ListDeparturesQuery{}, problemError{unknownQueryParameter, "query parameter " + name + " is not declared by this operation"}
	}
	limit, err := page.Limit(r)
	if err != nil {
		return usecases.ListDeparturesQuery{}, problemError{invalidLimit, err.Error()}
	}
	after, err := page.After(r)
	if err != nil {
		return usecases.ListDeparturesQuery{}, problemError{invalidCursor, err.Error()}
	}
	values := r.URL.Query()
	q := usecases.ListDeparturesQuery{Origin: values.Get("origin"), Destination: values.Get("destination"), Limit: limit}
	if after != nil {
		at, err := time.Parse(time.RFC3339Nano, after.Sort)
		if err != nil {
			return q, problemError{invalidCursor, page.ErrInvalidCursor.Error()}
		}
		q.After = &repository.DepartureCursor{CutoffAt: at, ID: after.ID}
	}
	for name, dest := range map[string]**time.Time{"from": &q.From, "to": &q.To} {
		if !values.Has(name) {
			continue
		}
		t, err := time.Parse(time.RFC3339, values.Get(name))
		if err != nil {
			return q, shared.Invalid("%s must be an RFC 3339 instant", name)
		}
		*dest = &t
	}
	return q, nil
}
