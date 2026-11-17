package vm

import (
	"os"
	"testing"

	"github.com/vide2d/vide-studio/internal/tdsl"
)

// FuzzRun feeds arbitrary text through the parser, the checker, the compiler
// and a few frames of the VM with small limits: whatever the script says,
// nothing may panic or hang.
func FuzzRun(f *testing.F) {
	for _, s := range []string{
		"when start\n  print 1 + 2\n",
		"var a = [1, 2, 3]\nwhen update\n  for each i in a\n    x += i\n",
		"to f(n)\n  return f(n + 1)\nwhen start\n  print f(0)\n",
		"when start\n  var s = \"a\"\n  repeat 40 times\n    s = s + s\n",
		"when start\n  var l = []\n  while yes\n    add l to l\n",
		"when every 0.1 seconds\n  x += 1\nwhen key space pressed\n  destroy\n",
		"when start\n  print [1, 2][3] / 0\n",
		"when start\n  for each i in 1 to 1e9\n    next round\n",
	} {
		f.Add(s)
	}
	for _, p := range []string{"../../../examples/demo/scripts/hero.tdsl", "../../../examples/demo/scripts/score.tdsl"} {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(string(b))
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		s, errs := tdsl.Parse(src)
		if len(errs) > 0 {
			return
		}
		if len(tdsl.Check(s, nil)) > 0 {
			return
		}
		h := newHost(60)
		h.held["space"] = true
		h.pressed["space"] = true
		e := NewEngine(h)
		e.Limits = Limits{FrameSteps: 3000, HandlerSteps: 1000, CallDepth: 16, ListItems: 200, TextChars: 200}
		in := e.NewInstance(Compile("fuzz.tdsl", src, s), 1)
		for i := 0; i < 8; i++ {
			for _, er := range step(e, in) {
				_ = er.Format()
			}
		}
	})
}

// BenchmarkLoop measures raw instruction throughput: a counting loop.
func BenchmarkLoop(b *testing.B) {
	h := newHost(60)
	e := NewEngine(h)
	e.Limits.HandlerSteps = 1 << 30
	e.Limits.FrameSteps = 1 << 30
	in := e.NewInstance(compile(b, "var n = 0\nwhen update\n  var i = 0\n  while i < 1000\n    i += 1\n    n += i % 3\n"), 1)
	step(e, in)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.BeginFrame()
		in.RunPhase(PhaseUpdate)
	}
}
