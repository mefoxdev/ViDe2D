package vm

import (
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/tdsl"
)

// fakeHost is a one-object world for tests.
type fakeHost struct {
	NoWorld
	rate      int
	seed      uint64
	props     map[Prop]Value
	logs      []string
	held      map[string]bool
	pressed   map[string]bool
	released  map[string]bool
	destroyed []ObjID
	gone      bool
}

func newHost(rate int) *fakeHost {
	return &fakeHost{rate: rate, seed: 7, props: map[Prop]Value{
		PropX: Num(0), PropY: Num(0), PropWidth: Num(16), PropHeight: Num(16), PropAngle: Num(0),
		PropVisible: Bool(true), PropFlip: Bool(false), PropText: Text(""), PropName: Text("hero"),
		PropTag: Text(""), PropImage: Text(""), PropTint: Text(""), PropLayer: Num(0), PropFontSize: Num(10),
	}, held: map[string]bool{}, pressed: map[string]bool{}, released: map[string]bool{}}
}

func (h *fakeHost) TickRate() int { return h.rate }
func (h *fakeHost) Seed() uint64  { return h.seed }
func (h *fakeHost) GetProp(_ ObjID, p Prop) (Value, error) {
	if h.gone {
		return Value{}, ErrGone
	}
	return h.props[p], nil
}
func (h *fakeHost) SetProp(_ ObjID, p Prop, v Value) error {
	if h.gone {
		return ErrGone
	}
	h.props[p] = v
	return nil
}
func (h *fakeHost) MoveBy(_ ObjID, dx, dy float64) error {
	h.props[PropX] = Num(float64(h.props[PropX].N + dx))
	h.props[PropY] = Num(float64(h.props[PropY].N + dy))
	return nil
}
func (h *fakeHost) MoveTo(_ ObjID, x, y float64) error {
	h.props[PropX], h.props[PropY] = Num(x), Num(y)
	return nil
}
func (h *fakeHost) Exists(ObjID) bool      { return !h.gone }
func (h *fakeHost) Destroy(id ObjID) error { h.destroyed = append(h.destroyed, id); return nil }
func (h *fakeHost) Log(s string)           { h.logs = append(h.logs, s) }
func (h *fakeHost) Key(name string, st tdsl.KeyState) bool {
	switch st {
	case tdsl.KeyHeld:
		return h.held[name]
	case tdsl.KeyPressed:
		return h.pressed[name]
	}
	return h.released[name]
}

func (h *fakeHost) x() float64 { return h.props[PropX].N }
func (h *fakeHost) y() float64 { return h.props[PropY].N }

func compile(t testing.TB, src string) *Program {
	t.Helper()
	p, errs := CompileSource("t.tdsl", src, nil)
	if len(errs) > 0 {
		t.Fatalf("compile: %v\n%s", errs[0], src)
	}
	return p
}

// setup compiles src and creates an instance of it on object 1.
func setup(t testing.TB, src string, h *fakeHost) (*Engine, *Instance) {
	t.Helper()
	e := NewEngine(h)
	return e, e.NewInstance(compile(t, src), 1)
}

// frame runs one full frame in the order of the reference.
func step(e *Engine, in *Instance) []*Error {
	e.BeginFrame()
	var errs []*Error
	for _, ph := range []Phase{PhaseInit, PhaseStart, PhaseInput, PhaseUpdate, PhaseTimers, PhaseTouches} {
		errs = append(errs, in.RunPhase(ph)...)
	}
	errs = append(errs, e.Deliver([]*Instance{in})...)
	return errs
}

// runStart runs a script's "when start" and returns the log and errors.
func runStart(t testing.TB, body string) ([]string, []*Error) {
	t.Helper()
	h := newHost(60)
	e, in := setup(t, "when start\n"+indent(body), h)
	errs := step(e, in)
	return h.logs, errs
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}
