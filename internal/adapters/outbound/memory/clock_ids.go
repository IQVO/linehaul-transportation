package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/domain/load"
)

// Clock is a settable ports.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current reading.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// SequentialIDs is a ports.IDGenerator minting load-1, load-2, ...
type SequentialIDs struct {
	mu    sync.Mutex
	loads int
}

// NewLoadID returns the next load id.
func (g *SequentialIDs) NewLoadID() load.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loads++
	return load.ID(fmt.Sprintf("load-%d", g.loads))
}
