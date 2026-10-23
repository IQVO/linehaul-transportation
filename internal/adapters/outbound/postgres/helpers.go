package postgres

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
)

// uniqueViolation is the SQLSTATE of a unique index or constraint violation.
const uniqueViolation = "23505"

// violatedConstraint returns the name of the unique constraint err violates, or "".
func violatedConstraint(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == uniqueViolation {
		return pg.ConstraintName
	}
	return ""
}

// nullTime stores the zero instant as NULL and everything else in UTC.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// fromNull is the inverse of nullTime.
func fromNull(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// nullText stores the empty string as NULL.
func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func fromNullText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// concurrent is the answer of a write that matched no row.
func concurrent(rows int64) error {
	if rows == 0 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}
