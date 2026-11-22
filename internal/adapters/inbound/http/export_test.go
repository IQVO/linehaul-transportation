package http

import (
	"net/http"
	"slices"
)

// ReplayEntry is one problem type of the service as the replay-class tests
// need it: the typed error that makes the production writer answer it.
type ReplayEntry struct {
	Err    error
	Slug   string
	Status int
	Class  string
}

// ReplayCatalogue returns every problem type a handler behind the
// Idempotency-Key middleware answers through writeError, read from the
// production catalogue, one entry per slug.
func ReplayCatalogue() []ReplayEntry {
	var out []ReplayEntry
	for _, e := range problemCatalogue {
		if !slices.ContainsFunc(out, func(x ReplayEntry) bool { return x.Slug == e.p.slug }) {
			out = append(out, ReplayEntry{Err: e.err, Slug: e.p.slug, Status: e.p.status, Class: e.replay.String()})
		}
	}
	return out
}

// WriteError answers err through the production error writer, so the replay
// class of its problem marks the capturing writer of the middleware.
func WriteError(w http.ResponseWriter, r *http.Request, err error) { writeError(w, r, err) }
