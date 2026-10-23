package shared

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidID(t *testing.T) {
	long := strings.Repeat("a", MaxID)
	cases := []struct {
		in   string
		want bool
	}{
		{"", false}, {"a", true}, {"A9", true}, {"dep-1.x_y:z", true}, {"-a", false}, {".a", false}, {"a b", false},
		{"a/b", false}, {"é", false}, {long, true}, {long + "a", false}, {"0", true}, {"a-", true}, {"Z", true}, {"z", true},
		{"{", false}, {"`", false}, {"@", false}, {"[", false}, {":", false}, {"/", false},
	}
	for _, c := range cases {
		if got := ValidID(c.in); got != c.want {
			t.Errorf("ValidID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestValidText(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  bool
	}{
		{"", 5, false}, {"ok", 5, true}, {" ok", 5, false}, {"ok ", 5, false}, {"abcde", 5, true}, {"abcdef", 5, false},
		{"a\tb", 5, false}, {"a\x7fb", 5, false}, {"a\x1fb", 5, false}, {"a b", 5, true}, {"ééééé", 5, true}, {"éééééé", 5, false},
	}
	for _, c := range cases {
		if got := ValidTextN(c.in, c.limit); got != c.want {
			t.Errorf("ValidTextN(%q,%d) = %v, want %v", c.in, c.limit, got, c.want)
		}
	}
	if !ValidText(strings.Repeat("a", MaxText)) || ValidText(strings.Repeat("a", MaxText+1)) {
		t.Fatal("ValidText must bound at MaxText")
	}
}

func TestErrorsWrapTheirSentinels(t *testing.T) {
	if err := Invalid("x %d", 1); !errors.Is(err, ErrValidation) || err.Error() != "validation failed: x 1" {
		t.Fatalf("Invalid = %v", err)
	}
	if err := Corrupt("y"); !errors.Is(err, ErrInvalidSnapshot) || err.Error() != "invalid snapshot: y" {
		t.Fatalf("Corrupt = %v", err)
	}
}

func TestHeader(t *testing.T) {
	at := time.Date(2026, 10, 23, 1, 2, 3, 0, time.UTC)
	h := Header{Version: 4, At: at}
	if h.AggregateVersion() != 4 || !h.OccurredAt().Equal(at) {
		t.Fatalf("header = %+v", h)
	}
}

func TestUTC(t *testing.T) {
	if !UTC(time.Time{}).IsZero() {
		t.Fatal("zero stays zero")
	}
	loc := time.FixedZone("x", 3600)
	got := UTC(time.Date(2026, 1, 1, 1, 0, 0, 0, loc))
	if got.Location() != time.UTC || got.Hour() != 0 {
		t.Fatalf("UTC = %v", got)
	}
}

func TestDeterministicID(t *testing.T) {
	a := DeterministicID("dep-", "lane", "PICKUP", "2026-10-23T21:00:00Z", "0")
	b := DeterministicID("dep-", "lane", "PICKUP", "2026-10-23T21:00:00Z", "0")
	c := DeterministicID("dep-", "lane", "PICKUP", "2026-10-23T21:00:00Z", "1")
	if a != b || a == c || !strings.HasPrefix(a, "dep-") || len(a) != 4+32 || !ValidID(a) {
		t.Fatalf("ids %q %q %q", a, b, c)
	}
	if DeterministicID("x", "a", "b") == DeterministicID("x", "ab") {
		t.Fatal("parts must be separated")
	}
}
