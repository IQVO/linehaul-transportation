package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
)

// The repository functions need a container (postgres_integration_test.go);
// these are the pure helpers around them.

func TestViolatedConstraint(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"a unique violation names its constraint", &pgconn.PgError{Code: uniqueViolation, ConstraintName: "departures_pkey"}, "departures_pkey"},
		{"another SQLSTATE is not a unique violation", &pgconn.PgError{Code: "23503"}, ""},
		{"a plain error", errors.New("boom"), ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := violatedConstraint(tc.err); got != tc.want {
				t.Errorf("violatedConstraint(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestNullTimeRoundTrip(t *testing.T) {
	if nullTime(time.Time{}) != nil {
		t.Error("the zero instant must be stored as NULL")
	}
	local := time.Date(2026, 10, 23, 9, 0, 0, 0, time.FixedZone("local", -3*3600))
	p := nullTime(local)
	if p == nil {
		t.Fatal("a real instant must be stored")
	}
	if _, offset := p.Zone(); offset != 0 {
		t.Errorf("stored offset = %ds, want UTC", offset)
	}
	if got := fromNull(p); !got.Equal(local) {
		t.Errorf("fromNull(nullTime(x)) = %v, want %v", got, local)
	}
	if got := fromNull(nil); !got.IsZero() {
		t.Errorf("fromNull(nil) = %v, want the zero instant", got)
	}
}

func TestNullTextRoundTrip(t *testing.T) {
	if nullText("") != nil {
		t.Error("the empty string must be stored as NULL")
	}
	if got := nullText("lane-1"); got == nil || *got != "lane-1" {
		t.Errorf("nullText(\"lane-1\") = %v, want a pointer to it", got)
	}
	if got := fromNullText(nil); got != "" {
		t.Errorf("fromNullText(nil) = %q, want \"\"", got)
	}
	if got := fromNullText(ptr("x")); got != "x" {
		t.Errorf("fromNullText = %q, want x", got)
	}
}

func ptr(s string) *string { return &s }

func TestConcurrent(t *testing.T) {
	if err := concurrent(0); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Errorf("concurrent(0) = %v, want ErrConcurrentUpdate", err)
	}
	if err := concurrent(1); err != nil {
		t.Errorf("concurrent(1) = %v, want nil", err)
	}
}
