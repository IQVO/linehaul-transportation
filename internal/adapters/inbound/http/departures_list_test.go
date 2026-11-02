package http_test

import (
	"fmt"
	nethttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestListDeparturesWalksEveryPageWithoutDuplicateOrSkip pages 7 departures, 3
// of which share a cutoff, by 3: every id appears exactly once, in order.
func TestListDeparturesWalksEveryPageWithoutDuplicateOrSkip(t *testing.T) {
	f := newFixture(t)
	want := []string{}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("dep-%d", i)
		in := time.Duration(i/3+1) * time.Hour // 3 + 3 + 1: ties on the cutoff
		f.departure(id, in, 5)
		want = append(want, id)
	}
	var got []string
	path := "/departures?limit=3"
	for pages := 0; path != ""; pages++ {
		if pages > 5 {
			t.Fatal("paging does not terminate")
		}
		status, _, body := f.call("GET", path, "")
		if status != nethttp.StatusOK {
			t.Fatalf("status = %d: %v", status, body)
		}
		items := body["items"].([]any)
		if len(items) > 3 {
			t.Fatalf("page has %d items, limit 3", len(items))
		}
		for _, it := range items {
			got = append(got, it.(map[string]any)["departureId"].(string))
		}
		path = ""
		if next, ok := body["nextCursor"].(string); ok {
			path = "/departures?limit=3&cursor=" + url.QueryEscape(next)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("walked %v, want %v", got, want)
	}
}

func TestListDeparturesLastPageHasNoNextCursor(t *testing.T) {
	f := newFixture(t)
	f.departure("dep-1", time.Hour, 5)
	_, _, body := f.call("GET", "/departures", "")
	if _, ok := body["nextCursor"]; ok {
		t.Fatalf("a single page carries nextCursor: %v", body)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("items = %v", body["items"])
	}
}

func TestRetiredRoutesAreGone(t *testing.T) {
	f := newFixture(t)
	for _, c := range [][2]string{
		{"POST", "/lane-schedules/l:retire"},
		{"POST", "/departures/d:cancel"},
		{"POST", "/departures/d:change-capacity"},
		{"POST", "/loads/l:close"},
		{"POST", "/loads/l:dispatch"},
		{"POST", "/loads/l:receive"},
		{"POST", "/departures/d/capacity"}, // the capacity is replaced with PUT
	} {
		req, err := nethttp.NewRequest(c[0], f.srv.URL+c[1], strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := nethttp.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		status := resp.StatusCode
		if status != nethttp.StatusNotFound && status != nethttp.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 404 or 405", c[0], c[1], status)
		}
	}
}
