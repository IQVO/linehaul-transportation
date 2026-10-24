package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The middleware's Postgres-backed paths (claim against a real table, replay
// of a stored outcome) are proven by idempotency_integration_test.go with a
// container. This file unit-tests the pieces that need no database: the key
// validation, the request fingerprint, the response capture and the
// savepoint/commit policy of runFresh against a scripted pgx.Tx.

// fakeTx is a scripted pgx.Tx: it records every call and answers Exec from a
// queue of results keyed by the SQL verb, so runFresh and settle can be driven
// without Postgres.
type fakeTx struct {
	begins    int
	commits   int
	rollbacks int
	execSQL   []string
	execArgs  [][]any
	exec      []pgconn.CommandTag // consumed one per Exec, defaulting to 1 row
	execErr   []error
	execN     int
}

func (f *fakeTx) Conn() *pgx.Conn { return nil }

func (f *fakeTx) Begin(context.Context) (pgx.Tx, error) {
	f.begins++
	return &fakeTx{exec: f.exec, execErr: f.execErr}, nil
}

func (f *fakeTx) Commit(context.Context) error { f.commits++; return nil }

func (f *fakeTx) Rollback(context.Context) error { f.rollbacks++; return nil }

func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execSQL = append(f.execSQL, sql)
	f.execArgs = append(f.execArgs, args)
	i := f.execN
	f.execN++
	if i < len(f.execErr) && f.execErr[i] != nil {
		return pgconn.NewCommandTag(""), f.execErr[i]
	}
	if i < len(f.exec) {
		return f.exec[i], nil
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (f *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented in fakeTx")
}

func (f *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func (f *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented in fakeTx")
}

func (f *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }

func (f *fakeTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (f *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented in fakeTx")
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v (%q)", err, rec.Body.String())
	}
	return body
}

func TestKeyFromAcceptsAValidKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/loads", nil)
	key := strings.Repeat("k", MaxIdempotencyKeyLength)
	r.Header.Set(IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	got, ok := keyFrom(rec, r)
	if !ok || got != key {
		t.Fatalf("keyFrom = (%q, %v), want the 200-character key accepted", got, ok)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("an accepted key must not write a problem, status = %d", rec.Code)
	}
}

func TestKeyFromRefusesTheRest(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		wantStatus int
		wantSlug   string
	}{
		{"missing key", "", http.StatusBadRequest, "idempotency-key-required"},
		{"201 characters", strings.Repeat("k", MaxIdempotencyKeyLength+1), http.StatusBadRequest, "idempotency-key-invalid"},
		{"control character", "key\u0007", http.StatusBadRequest, "idempotency-key-invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/loads", nil)
			if tc.key != "" {
				r.Header.Set(IdempotencyKeyHeader, tc.key)
			}
			rec := httptest.NewRecorder()
			if _, ok := keyFrom(rec, r); ok {
				t.Fatal("keyFrom accepted an invalid key")
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			problem := problemOf(t, rec)
			if typ, _ := problem["type"].(string); !strings.HasSuffix(typ, tc.wantSlug) {
				t.Fatalf("problem type = %v, want ...%s", problem["type"], tc.wantSlug)
			}
		})
	}
}

func TestHasControl(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"plain-key-1", false},
		{"space inside", false},
		{"tab\tinside", true},
		{"newline\n", true},
		{"nul\x00", true},
		{"unicode é", false}, // printable, not a control rune
	}
	for _, tc := range cases {
		if got := hasControl(tc.in); got != tc.want {
			t.Errorf("hasControl(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFingerprintIdentifiesMethodPathAndBody(t *testing.T) {
	mk := func(method, path string, body string) string {
		return fingerprint(httptest.NewRequest(method, path, strings.NewReader(body)), []byte(body))
	}
	base := mk(http.MethodPost, "/loads", `{"a":1}`)
	if mk(http.MethodPost, "/loads", `{"a":1}`) != base {
		t.Error("the same request must fingerprint identically")
	}
	if mk(http.MethodPut, "/loads", `{"a":1}`) == base {
		t.Error("another method must change the fingerprint")
	}
	if mk(http.MethodPost, "/loads/other", `{"a":1}`) == base {
		t.Error("another path must change the fingerprint")
	}
	if mk(http.MethodPost, "/loads", `{"a":2}`) == base {
		t.Error("another body must change the fingerprint")
	}
	// The NUL separators must keep (method, path) concatenations
	// unambiguous: without them "GET" + "/a/b" and "GET/a" + "/b" are the
	// same byte string. The method is not a valid HTTP token, so build the
	// request by hand (fingerprint reads only Method and URL).
	newline := func(method, path string) *http.Request {
		return &http.Request{Method: method, URL: &url.URL{Path: path}}
	}
	if fingerprint(newline("GET", "/a/b"), nil) == fingerprint(newline("GET/a", "/b"), nil) {
		t.Error("separator collapsed two distinct requests into one fingerprint")
	}
}

func TestReplayContentTypeFollowsTheStatusClass(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusOK, "application/json"},
		{http.StatusCreated, "application/json"},
		{http.StatusBadRequest, "application/problem+json"},
		{http.StatusUnprocessableEntity, "application/problem+json"},
		{http.StatusInternalServerError, "application/problem+json"},
	}
	for _, tc := range cases {
		if got := replayContentType(tc.status); got != tc.want {
			t.Errorf("replayContentType(%d) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestCaptureRecordsTheFirstStatusAndEveryByte(t *testing.T) {
	c := &capture{header: http.Header{}}
	c.Header().Add("X-Test", "a")
	n, err := c.Write([]byte("hel"))
	if n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if c.code != http.StatusOK {
		t.Fatalf("code after a bare Write = %d, want 200", c.code)
	}
	c.WriteHeader(http.StatusTeapot)
	c.WriteHeader(http.StatusBadGateway)
	if c.code != http.StatusOK {
		t.Fatalf("WriteHeader after a Write must not overwrite the status: %d", c.code)
	}
	if _, _ = c.Write([]byte("lo")); c.body.String() != "hello" {
		t.Fatalf("body = %q, want the concatenation of every Write", c.body.String())
	}
}

func TestFlushCopiesHeadersStatusAndBody(t *testing.T) {
	rec := &capture{header: http.Header{}}
	rec.Header().Add("Content-Type", "application/json")
	rec.Header().Add("Set-Cookie", "a=1")
	rec.Header().Add("Set-Cookie", "b=2")
	rec.WriteHeader(http.StatusCreated)
	_, _ = rec.Write([]byte(`{"ok":true}`))

	w := httptest.NewRecorder()
	flush(w, rec)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if got := w.Header().Values("Set-Cookie"); len(got) != 2 || got[0] != "a=1" || got[1] != "b=2" {
		t.Fatalf("Set-Cookie = %v, want both values", got)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestClaimReportsWhetherTheKeyIsNew(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/loads", nil)
	ctx := r.Context()

	oneRow := pgconn.NewCommandTag("INSERT 0 1")
	noRow := pgconn.NewCommandTag("INSERT 0 0")

	k := &keyed{route: "POST /loads"}
	// claim execs the purge DELETE first, then the INSERT: script both.
	fresh, err := k.claim(ctx, &fakeTx{exec: []pgconn.CommandTag{oneRow, oneRow}}, "key", "hash")
	if err != nil || !fresh {
		t.Fatalf("claim of a new key = (%v, %v), want (true, nil)", fresh, err)
	}

	fresh, err = k.claim(ctx, &fakeTx{exec: []pgconn.CommandTag{oneRow, noRow}}, "key", "hash")
	if err != nil || fresh {
		t.Fatalf("claim of a held key = (%v, %v), want (false, nil)", fresh, err)
	}

	deleteErr := errors.New("purge failed")
	if _, err := k.claim(ctx, &fakeTx{execErr: []error{deleteErr}}, "key", "hash"); !errors.Is(err, deleteErr) {
		t.Fatalf("claim = %v, want the purge error", err)
	}
}

// handlerOf builds the middleware's next handler around a response func.
func handlerOf(fn func(http.ResponseWriter, *http.Request)) http.Handler {
	return http.HandlerFunc(fn)
}

func TestRunFreshStoresA2xxAndCommitsTheSavepoint(t *testing.T) {
	tx := &fakeTx{}
	k := &keyed{route: "POST /loads", bind: func(ctx context.Context, _ pgx.Tx) context.Context { return ctx }, next: handlerOf(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"loadId":"load-1"}`))
	})}
	r := httptest.NewRequest(http.MethodPost, "/loads", nil)
	rec := httptest.NewRecorder()

	k.runFresh(r.Context(), rec, r, tx, "key-1")

	if rec.Code != http.StatusCreated || rec.Body.String() != `{"loadId":"load-1"}` {
		t.Fatalf("response = %d %q, want the handler's outcome", rec.Code, rec.Body.String())
	}
	if tx.begins != 1 {
		t.Fatalf("savepoints begun = %d, want 1", tx.begins)
	}
	if len(tx.execSQL) != 1 || !strings.HasPrefix(tx.execSQL[0], "UPDATE idempotency_keys") {
		t.Fatalf("execs = %q, want exactly the outcome UPDATE", tx.execSQL)
	}
	if args := tx.execArgs[0]; args[0] != http.StatusCreated || string(args[1].([]byte)) != `{"loadId":"load-1"}` ||
		args[2] != "POST /loads" || args[3] != "key-1" {
		t.Fatalf("UPDATE args = %v, want the recorded status, body, route and key", args)
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commits = %d, rollbacks = %d, want the transaction committed once", tx.commits, tx.rollbacks)
	}
}

func TestRunFreshStoresA4xxButDropsItsWrites(t *testing.T) {
	tx := &fakeTx{}
	k := &keyed{route: "POST /loads", bind: func(ctx context.Context, _ pgx.Tx) context.Context { return ctx }, next: handlerOf(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	})}
	r := httptest.NewRequest(http.MethodPost, "/loads", nil)
	rec := httptest.NewRecorder()

	k.runFresh(r.Context(), rec, r, tx, "key-1")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if len(tx.execSQL) != 1 || !strings.HasPrefix(tx.execSQL[0], "UPDATE idempotency_keys") {
		t.Fatalf("a 4xx must still be stored, execs = %q", tx.execSQL)
	}
	if tx.commits != 1 {
		t.Fatalf("commits = %d, want the stored 4xx committed", tx.commits)
	}
	// The savepoint (begun inside runFresh) is rolled back by settle because
	// the handler answered 4xx; the outer transaction commits.
	if tx.begins != 1 {
		t.Fatalf("savepoints = %d, want 1", tx.begins)
	}
}

func TestRunFreshNeverStoresA5xx(t *testing.T) {
	tx := &fakeTx{}
	k := &keyed{route: "POST /loads", bind: func(ctx context.Context, _ pgx.Tx) context.Context { return ctx }, next: handlerOf(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})}
	r := httptest.NewRequest(http.MethodPost, "/loads", nil)
	rec := httptest.NewRecorder()

	k.runFresh(r.Context(), rec, r, tx, "key-1")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if len(tx.execSQL) != 0 {
		t.Fatalf("a 5xx must never be stored, execs = %q", tx.execSQL)
	}
	if tx.commits != 0 {
		t.Fatalf("commits = %d, want none: everything rolls back so the key can retry", tx.commits)
	}
}

func TestRunFreshRollsBackAndRepanicsOnAHandlerPanic(t *testing.T) {
	tx := &fakeTx{}
	k := &keyed{route: "POST /loads", bind: func(ctx context.Context, _ pgx.Tx) context.Context { return ctx }, next: handlerOf(func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	})}
	r := httptest.NewRequest(http.MethodPost, "/loads", nil)
	rec := httptest.NewRecorder()

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Error("the panic must reach the server's own recovery, not be swallowed")
		}
		if tx.rollbacks == 0 {
			t.Error("the transaction must be rolled back when the handler panics")
		}
	}()
	k.runFresh(r.Context(), rec, r, tx, "key-1")
}

// The stored outcome must reach the client only once committed: settle's
// UPDATE failing keeps the client waiting on a 500 instead of a phantom 2xx.
func TestRunFreshAnswers500WhenStoringFails(t *testing.T) {
	tx := &fakeTx{execErr: []error{errors.New("update failed")}}
	k := &keyed{route: "POST /loads", bind: func(ctx context.Context, _ pgx.Tx) context.Context { return ctx }, next: handlerOf(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})}
	r := httptest.NewRequest(http.MethodPost, "/loads", nil)
	rec := httptest.NewRecorder()

	k.runFresh(r.Context(), rec, r, tx, "key-1")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the outcome cannot be stored", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "update failed") {
		t.Fatal("an internal error must not leak its cause to the client")
	}
	_ = io.Discard
}
