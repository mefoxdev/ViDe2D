package clip

import "testing"

func TestRoundTripLocalFallback(t *testing.T) {
	once.Do(func() {}) // force "no system tool"
	sys = nil
	Set("привет\nмир")
	if got := Get(); got != "привет\nмир" {
		t.Fatalf("got %q", got)
	}
}
