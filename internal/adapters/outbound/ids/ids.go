// Package ids is the UUID-backed ports.IDGenerator.
package ids

import (
	"github.com/google/uuid"

	"github.com/claudioed/linehaul-transportation/internal/domain/load"
)

// UUID mints load ids of the form load-<uuid>.
type UUID struct{}

// NewLoadID implements ports.IDGenerator.
func (UUID) NewLoadID() load.ID { return load.ID("load-" + uuid.NewString()) }
