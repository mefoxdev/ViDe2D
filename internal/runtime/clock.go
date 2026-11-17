package runtime

import "time"

// maxCatchUpDiv: one Advance returns at most rate/maxCatchUpDiv steps (a quarter
// of a second of simulation). Longer stalls are dropped
// instead of simulated, so a hiccup cannot freeze the game catching up.
const maxCatchUpDiv = 4

// Clock is a fixed-timestep accumulator. It never reads the wall clock: the
// caller feeds it elapsed time, so tests use fake time and the result is
// exact. Time is counted in nanoseconds*rate, which keeps 90 Hz exact too.
type Clock struct {
	rate int64
	acc  int64 // nanoseconds * rate not yet turned into steps
}

const nsPerSecond = int64(time.Second)

// NewClock returns a clock for a simulation rate in Hz.
func NewClock(rate int) *Clock { return &Clock{rate: int64(rate)} }

// Advance adds dt of elapsed time and returns how many whole steps are due.
// Negative dt is ignored.
func (c *Clock) Advance(dt time.Duration) int {
	if dt > 0 {
		c.acc += int64(dt) * c.rate
	}
	steps := c.acc / nsPerSecond
	c.acc %= nsPerSecond
	if limit := c.rate / maxCatchUpDiv; steps > limit {
		steps = limit
	}
	return int(steps)
}

// Alpha is the progress into the next step, 0 <= Alpha < 1, for interpolation.
func (c *Clock) Alpha() float64 { return float64(c.acc) / float64(nsPerSecond) }

// Sim couples a World with a Clock: the simulation half of the game loop.
type Sim struct {
	World *World
	Clock *Clock
}

// NewSim returns a simulation that runs w at its own tick rate.
func NewSim(w *World) *Sim { return &Sim{World: w, Clock: NewClock(w.TickRate)} }

// Update feeds elapsed time and runs the due steps. It returns how many ran.
func (s *Sim) Update(dt time.Duration) (int, error) {
	n := s.Clock.Advance(dt)
	for i := 0; i < n; i++ {
		if err := s.World.Step(); err != nil {
			return i, err
		}
	}
	return n, nil
}
