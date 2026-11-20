package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryProblemTypeDeclaresAReplayClass: an entry with no class, a 5xx that
// is not RETRYABLE and one slug declared with two classes fail. There is no
// runtime default: a new problem type is classified by the PR that adds it.
func TestEveryProblemTypeDeclaresAReplayClass(t *testing.T) {
	if len(problemCatalogue) == 0 {
		t.Fatal("no problem types: the test would pass on nothing")
	}
	seen := map[string]replayClass{}
	for _, e := range problemCatalogue {
		if !e.replay.declared() {
			t.Errorf("problem type %q (%d) declares no replay class: set FINAL, OUTCOME or RETRYABLE on its catalogue entry (ADR 0015)", e.p.slug, e.p.status)
		}
		if e.p.status >= http.StatusInternalServerError && e.replay != replayRetryable {
			t.Errorf("problem type %q answers %d: a 5xx is never stored, so its class is RETRYABLE, got %s", e.p.slug, e.p.status, e.replay)
		}
		if prev, dup := seen[e.p.slug]; dup && prev != e.replay {
			t.Errorf("slug %q is declared %s and %s: one slug, one class", e.p.slug, prev, e.replay)
		}
		seen[e.p.slug] = e.replay
	}
}

func TestReplayClassDeclaredAndString(t *testing.T) {
	for c, want := range map[replayClass]string{replayFinal: "FINAL", replayOutcome: "OUTCOME", replayRetryable: "RETRYABLE", replayUnset: "UNSET"} {
		if c.String() != want || c.declared() != (c != replayUnset) {
			t.Errorf("class %d: String %q declared %v, want %q and %v", c, c.String(), c.declared(), want, c != replayUnset)
		}
	}
	if replayClass(9).declared() || replayClass(9).String() != "UNSET" {
		t.Error("an out-of-range class must be undeclared")
	}
}

// TestTheMiddlewareDecidesByClassAndTwoStatusRanges: the whole verdict table.
func TestTheMiddlewareDecidesByClassAndTwoStatusRanges(t *testing.T) {
	for _, tc := range []struct {
		code  int
		class replayClass
		want  verdict
	}{
		{http.StatusCreated, replayUnset, storeAndKeep},
		{http.StatusOK, replayFinal, storeAndKeep},
		{http.StatusConflict, replayFinal, storeRefusal},
		{http.StatusConflict, replayOutcome, storeAndKeep},
		{http.StatusConflict, replayRetryable, releaseKey},
		{http.StatusConflict, replayUnset, releaseKey},
		{http.StatusUnprocessableEntity, replayUnset, releaseKey},
		{http.StatusFound, replayFinal, storeRefusal},
		{http.StatusInternalServerError, replayFinal, releaseKey},
		{http.StatusServiceUnavailable, replayOutcome, releaseKey},
	} {
		c := &capture{code: tc.code, class: tc.class}
		if got := c.verdict(); got != tc.want {
			t.Errorf("%d %s: verdict %d, want %d", tc.code, tc.class, got, tc.want)
		}
	}
}

// TestWriteErrorMarksTheCaptureWithTheClassOfTheEntry: the production error
// writer tells the capture, and an error the catalogue does not know leaves it
// unmarked.
func TestWriteErrorMarksTheCaptureWithTheClassOfTheEntry(t *testing.T) {
	for _, e := range problemCatalogue {
		rec := &capture{header: http.Header{}}
		writeError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), e.err)
		if rec.class != e.replay {
			t.Errorf("%s marked %s, want %s", e.p.slug, rec.class, e.replay)
		}
	}
	rec := &capture{header: http.Header{}}
	writeError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), errNULBody)
	if rec.class != replayFinal {
		t.Errorf("a NUL body marked %s, want FINAL", rec.class)
	}
	rec = &capture{header: http.Header{}}
	writeError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), errors.New("pq: down"))
	if rec.class != replayUnset || rec.code != http.StatusInternalServerError {
		t.Errorf("an unmapped error marked %s with %d, want an unmarked 500", rec.class, rec.code)
	}
	// A writer that is not the capture (a plain recorder) is simply written to.
	plain := httptest.NewRecorder()
	writeError(plain, httptest.NewRequest(http.MethodPost, "/x", nil), errMalformedRequest)
	if plain.Code != http.StatusBadRequest {
		t.Errorf("plain writer = %d, want 400", plain.Code)
	}
}

var adrRow = regexp.MustCompile("^\\| `([a-z-]+)` \\| (FINAL|OUTCOME|RETRYABLE) \\| (\\d{3}) \\|")

// TestTheADRDeclaresTheClassOfEveryProblem holds the class table of ADR 0015
// equal to the catalogue: slug, class and status.
func TestTheADRDeclaresTheClassOfEveryProblem(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/adr/0015-every-problem-type-has-a-replay-class.md")
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for _, line := range strings.Split(string(raw), "\n") {
		if m := adrRow.FindStringSubmatch(line); m != nil {
			got = append(got, m[1]+" "+m[2]+" "+m[3])
		}
	}
	for _, e := range problemCatalogue {
		row := e.p.slug + " " + e.replay.String() + " " + strconv.Itoa(e.p.status)
		if !slices.Contains(want, row) {
			want = append(want, row)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("the ADR 0015 table and the catalogue differ.\nADR:       %v\ncatalogue: %v", got, want)
	}
}

var specList = regexp.MustCompile(`are not stored and release the key: ([a-z, -]+)\.`)

// TestTheSpecListsTheRetryableProblemTypes: the Idempotency-Key text of
// apis/openapi.yaml names exactly the RETRYABLE problem types.
func TestTheSpecListsTheRetryableProblemTypes(t *testing.T) {
	raw, err := os.ReadFile("../../../../apis/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := specList.FindStringSubmatch(strings.Join(strings.Fields(string(raw)), " "))
	if m == nil {
		t.Fatal("apis/openapi.yaml does not list the problem types that are not stored (\"are not stored and release the key: ...\")")
	}
	var got, want []string
	for _, s := range strings.Split(m[1], ",") {
		got = append(got, strings.TrimSpace(s))
	}
	for _, e := range problemCatalogue {
		if e.replay == replayRetryable && !slices.Contains(want, e.p.slug) {
			want = append(want, e.p.slug)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("spec list %v, RETRYABLE catalogue entries %v", got, want)
	}
}
