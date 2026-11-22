//go:build integration

package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

// No problem type of this service is OUTCOME (ADR 0015): every use case refuses
// before its first Save. The verdict is nevertheless implemented and proven on
// Postgres with a handler that writes, marks the capture by hand and refuses,
// so the first refusal that commits state gets its writes kept with the stored
// answer instead of silently rolled back.
func TestAnOutcomeRefusalCommitsItsWritesWithItsStoredAnswer(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE outcome_writes (tag text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		tx, ok := pgtx.From(r.Context())
		if !ok {
			t.Error("no transaction bound into the request context")
			return
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO outcome_writes (tag) VALUES ('turned-away')`); err != nil {
			t.Error(err)
		}
		w.(replayMarker).MarkReplay(replayOutcome)
		writeProblem(w, r, problem{http.StatusConflict, "turned-away", "Turned away"}, "recorded")
	})
	mw := RequireIdempotencyKey(pool, pgtx.With)("POST /outcome")(h)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/outcome", strings.NewReader(`{"n":1}`))
		req.Header.Set(IdempotencyKeyHeader, "key-outcome")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}
	first := send()
	for range 3 {
		again := send()
		body, _ := io.ReadAll(again.Body)
		if again.Code != http.StatusConflict || again.Header().Get(IdempotencyReplayedHeader) != "true" || string(body) != first.Body.String() {
			t.Fatalf("replay = %d %q, want the stored 409", again.Code, body)
		}
	}
	var writes, rows int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outcome_writes), (SELECT count(*) FROM idempotency_keys WHERE key = 'key-outcome' AND response_status = 409)`).Scan(&writes, &rows); err != nil {
		t.Fatal(err)
	}
	if first.Code != http.StatusConflict || calls != 1 || writes != 1 || rows != 1 {
		t.Fatalf("status %d, handler ran %d, committed writes %d, stored rows %d, want 409, 1, 1, 1", first.Code, calls, writes, rows)
	}
}
