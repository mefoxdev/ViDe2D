package vm

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Services of the fake host.

func (h *fakeHost) PlaySound(n string) error {
	if strings.HasPrefix(n, "bad") {
		return &HostError{Code: "TD304", Message: "Не нашёл звук «" + n + "»."}
	}
	h.sounds = append(h.sounds, n)
	return nil
}
func (h *fakeHost) PlayMusic(n string) error {
	if strings.HasPrefix(n, "bad") {
		return &HostError{Code: "TD304", Message: "Не нашёл музыку «" + n + "»."}
	}
	h.music = append(h.music, n)
	return nil
}
func (h *fakeHost) StopMusic() { h.stops++ }
func (h *fakeHost) GoToScene(n string) error {
	if n == "nowhere" {
		return &HostError{Code: "TD305", Message: "Нет сцены «nowhere»."}
	}
	h.scenes = append(h.scenes, n)
	return nil
}
func (h *fakeHost) RestartScene() { h.restarts++ }
func (h *fakeHost) Remember(k string, v Value) (int, error) {
	if h.mem == nil {
		h.mem = map[string]Value{}
	}
	h.mem[k] = copyValue(v, map[*List]*List{})
	return 1, nil
}
func (h *fakeHost) Recall(k string) (Value, bool, int) {
	v, ok := h.mem[k]
	return copyValue(v, map[*List]*List{}), ok, 1
}

// frames runs n frames and returns all errors.
func frames(e *Engine, in *Instance, n int) (errs []*Error) {
	for i := 0; i < n; i++ {
		errs = append(errs, step(e, in)...)
	}
	return errs
}

func TestWaitResumesAtTheRightStepAtEveryRate(t *testing.T) {
	for _, rate := range []int{60, 90, 120} {
		h := newHost(rate)
		e, in := setup(t, "when start\n  print \"a\"\n  wait 2 seconds\n  print \"b\"\n", h)
		// the start handler runs in step 1; it goes on 2 seconds (2*rate steps) later
		if errs := frames(e, in, 2*rate); len(errs) != 0 || strings.Join(h.logs, ",") != "a" {
			t.Fatalf("%d Hz: before: %v %v", rate, h.logs, errs)
		}
		if in.Waiting() != 1 || e.Waiting() != 1 {
			t.Fatalf("%d Hz: waiting %d %d", rate, in.Waiting(), e.Waiting())
		}
		frames(e, in, 1)
		if strings.Join(h.logs, ",") != "a,b" || in.Waiting() != 0 || e.Waiting() != 0 {
			t.Fatalf("%d Hz: after: %v waiting %d %d", rate, h.logs, in.Waiting(), e.Waiting())
		}
	}
}

func TestWaitZeroMeansNextStepAndLoopsDoNotHangTheFrame(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var n = 0\nwhen start\n  repeat forever\n    n += 1\n    wait 0 seconds\n", h)
	if errs := frames(e, in, 10); len(errs) != 0 {
		t.Fatal(errs[0].Format())
	}
	if v, _ := in.Global("n"); v.N != 10 {
		t.Fatalf("n = %v, want one iteration per step", v)
	}
}

func TestStepLimitCountsBetweenWaits(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var n = 0\nwhen start\n  repeat forever\n    n += 1\n    wait 1 second\n", h)
	e.Limits.HandlerSteps = 50
	if errs := frames(e, in, 60*40); len(errs) != 0 {
		t.Fatal(errs[0].Format())
	}
	if v, _ := in.Global("n"); v.N != 40 {
		t.Fatalf("n = %v", v)
	}
	// but a single stretch between waits is still limited
	h2 := newHost(60)
	e2, in2 := setup(t, "when start\n  wait 1 second\n  while yes\n    x += 1\n", h2)
	e2.Limits.HandlerSteps = 50
	errs := frames(e2, in2, 70)
	if len(errs) != 1 || errs[0].Code != CodeSteps {
		t.Fatalf("%v", errs)
	}
	if in2.Waiting() != 0 || e2.Waiting() != 0 {
		t.Fatal("a failed run still counts as waiting")
	}
}

func TestHandlerNeverRunsInsideItselfWhileWaiting(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var n = 0\nwhen key space pressed\n  n += 1\n  wait 1 second\n  n += 100\n", h)
	h.pressed["space"] = true
	frames(e, in, 30) // pressed every step; only the first one starts a run
	if v, _ := in.Global("n"); v.N != 1 {
		t.Fatalf("n = %v", v)
	}
	frames(e, in, 31) // the run ends at step 61 and the very same step starts a new one
	if v, _ := in.Global("n"); v.N != 102 {
		t.Fatalf("n = %v", v)
	}
}

func TestHandlersWakeInFileOrderAndTimeOrder(t *testing.T) {
	h := newHost(60)
	src := "when start\n  wait 2 seconds\n  print \"A\"\nwhen start\n  wait 1 second\n  print \"B\"\nwhen start\n  wait 2 seconds\n  print \"C\"\n"
	e, in := setup(t, src, h)
	frames(e, in, 125)
	if got := strings.Join(h.logs, ""); got != "BAC" {
		t.Fatalf("%q", got)
	}
}

func TestWaitInsideActionAndExpression(t *testing.T) {
	h := newHost(60)
	src := "to slow(n)\n  wait 1 second\n  return n * 2\nwhen start\n  var a = [1, slow(4) + 1, 3]\n  print a\n"
	e, in := setup(t, src, h)
	frames(e, in, 60)
	if len(h.logs) != 0 {
		t.Fatalf("%v", h.logs)
	}
	if errs := frames(e, in, 1); len(errs) != 0 || strings.Join(h.logs, "") != "[1, 9, 3]" {
		t.Fatalf("%v %v", h.logs, errs)
	}
}

func TestWaitWhereItCannotSuspendIsAnError(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "to f()\n  wait 1 second\n", h)
	_, err := in.Call("f")
	if err == nil || err.Code != CodeWaitFrame {
		t.Fatalf("%v", err)
	}
	// a variable initializer cannot wait either
	h2 := newHost(60)
	e2, in2 := setup(t, "to f()\n  wait 1 second\n  return 1\nvar a = f()\n", h2)
	errs := step(e2, in2)
	if len(errs) != 1 || errs[0].Code != CodeWaitFrame {
		t.Fatalf("%v", errs)
	}
	_ = e
}

func TestWaitArgumentErrors(t *testing.T) {
	for src, code := range map[string]string{
		"wait -1 seconds": CodeWrongType, "var q = recall(\"z\", \"a\")\nwait q seconds": CodeWrongType, "wait 1 / 0 seconds": CodeDivZero,
		"slide to 1, 2 in -1 seconds": CodeWrongType, "var q = recall(\"z\", \"a\")\nslide to q, 2 in 1 seconds": CodeWrongType,
	} {
		_, errs := runStart(t, src)
		if len(errs) != 1 || errs[0].Code != code {
			t.Errorf("%s: %v", src, errs)
		}
	}
}

func TestSlideMovesStepByStepAndEndsExactly(t *testing.T) {
	for _, rate := range []int{60, 90, 120} {
		h := newHost(rate)
		h.props[PropX], h.props[PropY] = Num(10), Num(20)
		e, in := setup(t, "when start\n  slide to 110, 70 in 1 second\n  print \"done\"\n", h)
		for k := 1; k <= rate; k++ {
			if errs := step(e, in); len(errs) != 0 {
				t.Fatal(errs[0].Format())
			}
			if k == 1 {
				// started this step: still at the start
				if h.x() != 10 {
					t.Fatalf("%d Hz: moved at once: %v", rate, h.x())
				}
				continue
			}
			j := float64(k - 1) // steps since the start
			want := 10 + 100*j/float64(rate)
			if k < rate && math.Abs(h.x()-want) > 1e-9 {
				t.Fatalf("%d Hz step %d: x = %v, want %v", rate, k, h.x(), want)
			}
		}
		if len(h.logs) != 0 || h.x() == 110 {
			t.Fatalf("%d Hz: finished too early: x=%v %v", rate, h.x(), h.logs)
		}
		step(e, in)
		if h.x() != 110 || h.y() != 70 || strings.Join(h.logs, "") != "done" || in.Waiting() != 0 {
			t.Fatalf("%d Hz: x=%v y=%v %v", rate, h.x(), h.y(), h.logs)
		}
	}
}

func TestSlideDeterministicBits(t *testing.T) {
	run := func() []uint64 {
		h := newHost(90)
		e, in := setup(t, "when start\n  slide to 333.3, -17.7 in 1.7 seconds\n", h)
		var out []uint64
		for i := 0; i < 200; i++ {
			step(e, in)
			out = append(out, math.Float64bits(h.x()), math.Float64bits(h.y()))
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("not reproducible")
		}
	}
}

func TestSlideCatchesUpAfterAStarvedFrame(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when start\n  slide to 60, 0 in 1 second\n", h)
	frames(e, in, 11) // 10 steps after the start
	// a frame whose budget is empty: the slide does not move
	e.BeginFrame()
	e.Charge(e.Limits.FrameSteps)
	in.RunPhase(PhaseWake)
	if math.Abs(h.x()-10) > 1e-9 {
		t.Fatalf("x = %v", h.x())
	}
	// next frame it is at the place of its own step, not one step behind
	step(e, in)
	if math.Abs(h.x()-12) > 1e-9 {
		t.Fatalf("x = %v", h.x())
	}
}

func TestDestroyedObjectCancelsItsTasks(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when start\n  wait 1 second\n  print \"never\"\nwhen click\n  slide to 5, 5 in 1 second\n", h)
	frames(e, in, 3)
	if e.Waiting() != 1 {
		t.Fatal(e.Waiting())
	}
	e.Detach(1)
	if e.Waiting() != 0 || in.Waiting() != 0 {
		t.Fatalf("still waiting %d %d", e.Waiting(), in.Waiting())
	}
	frames(e, in, 100)
	if len(h.logs) != 0 {
		t.Fatalf("a cancelled run went on: %v", h.logs)
	}
}

type goneMover struct{ *fakeHost }

func (g goneMover) MoveTo(ObjID, float64, float64) error { return ErrGone }

func TestSlideStopsWhenTheObjectDisappears(t *testing.T) {
	h := newHost(60)
	g := goneMover{h}
	e := NewEngine(g)
	in := e.NewInstance(compile(t, "when start\n  slide to 5, 5 in 1 second\n  print \"x\"\n"), 1)
	errs := frames(e, in, 5)
	if len(errs) != 0 || e.Waiting() != 0 || len(h.logs) != 0 {
		t.Fatalf("%v %d %v", errs, e.Waiting(), h.logs)
	}
}

func TestWaitingLimits(t *testing.T) {
	h := newHost(60)
	src := "when start\n  wait 5 seconds\nwhen start\n  wait 5 seconds\nwhen start\n  wait 5 seconds\n"
	e, in := setup(t, src, h)
	e.Limits.WaitingPerObj = 2
	errs := frames(e, in, 3)
	if len(errs) != 1 || errs[0].Code != CodeTooBig || !strings.Contains(errs[0].Message, "ждущих") {
		t.Fatalf("%v", errs)
	}
	if in.Waiting() != 2 {
		t.Fatal(in.Waiting())
	}
	// the world limit
	h2 := newHost(60)
	e2 := NewEngine(h2)
	e2.Limits.Waiting = 3
	p := compile(t, "when start\n  wait 5 seconds\n")
	var bad int
	for id := 1; id <= 5; id++ {
		in := e2.NewInstance(p, ObjID(id))
		e2.BeginFrame()
		for _, ph := range []Phase{PhaseInit, PhaseStart} {
			for _, er := range in.RunPhase(ph) {
				if er.Code == CodeTooBig {
					bad++
				}
			}
		}
	}
	if bad != 2 || e2.Waiting() != 3 {
		t.Fatalf("bad %d waiting %d", bad, e2.Waiting())
	}
}

func TestSuspendedTaskSurvivesSnapshotAndRestore(t *testing.T) {
	src := "var items = [1, 2]\nwhen start\n  var mine = items\n  add 3 to mine\n  wait 1 second\n  add 4 to mine\n  print items\n  print mine is items\n"
	h := newHost(60)
	e, in := setup(t, src, h)
	frames(e, in, 21) // 20 steps after the start
	st := in.Snapshot()
	e.Detach(1) // the chunk is unloaded
	frames(e, in, 500)
	if len(h.logs) != 0 {
		t.Fatalf("ran while unloaded: %v", h.logs)
	}
	e.BeginFrame()
	e.BeginFrame() // time passes while unloaded
	in2 := e.NewInstance(in.Program(), 1)
	if !in2.Restore(st) {
		t.Fatal("restore")
	}
	if in2.Waiting() != 1 || e.Waiting() != 1 {
		t.Fatalf("%d %d", in2.Waiting(), e.Waiting())
	}
	// 40 steps were left of the second
	for i := 0; i < 39; i++ {
		step(e, in2)
	}
	if len(h.logs) != 0 {
		t.Fatalf("too early: %v", h.logs)
	}
	step(e, in2)
	if strings.Join(h.logs, ";") != "[1, 2, 3, 4];yes" {
		t.Fatalf("%v", h.logs)
	}
	if v, _ := in2.Global("items"); len(v.L.Items) != 4 {
		t.Fatal("the list lost its sharing with the task")
	}
}

func TestSnapshotOfSlideKeepsProgress(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when start\n  slide to 60, 0 in 1 second\n", h)
	frames(e, in, 21)
	st := in.Snapshot()
	e.Detach(1)
	frames(e, in, 50)
	in2 := e.NewInstance(in.Program(), 1)
	in2.Restore(st)
	step(e, in2)
	if math.Abs(h.x()-21) > 1e-9 {
		t.Fatalf("x = %v", h.x())
	}
}

func TestRestoreRespectsTheLimits(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when start\n  wait 5 seconds\n", h)
	frames(e, in, 2)
	st := in.Snapshot()
	e.Detach(1)
	e.Limits.Waiting = 0
	in2 := e.NewInstance(in.Program(), 1)
	in2.Restore(st)
	if in2.Waiting() != 0 || e.Waiting() != 0 {
		t.Fatal("limit ignored")
	}
}

func TestSoundsScenesAndMemoryReachTheHost(t *testing.T) {
	src := `when start
  play sound "a.wav"
  play music "theme"
  stop music
  remember "best" as [1, "two", yes]
  var b = recall("best", 0)
  print b
  print recall("none", 7)
  go to scene "level2"
  restart scene
`
	h := newHost(60)
	e, in := setup(t, src, h)
	if errs := step(e, in); len(errs) != 0 {
		t.Fatal(errs[0].Format())
	}
	if strings.Join(h.sounds, ",") != "a.wav" || strings.Join(h.music, ",") != "theme" || h.stops != 1 ||
		strings.Join(h.scenes, ",") != "level2" || h.restarts != 1 {
		t.Fatalf("%+v", h)
	}
	if strings.Join(h.logs, "|") != `[1, "two", yes]|7` {
		t.Fatalf("%q", h.logs)
	}
}

func TestServiceErrors(t *testing.T) {
	for src, code := range map[string]string{
		`play sound "bad.wav"`:                                               "TD304",
		`play music "bad"`:                                                   "TD304",
		"var q = recall(\"z\", 5)\nplay sound q":                             CodeWrongType,
		`go to scene "nowhere"`:                                              "TD305",
		"var q = recall(\"z\", 5)\ngo to scene q":                            CodeWrongType,
		"var q = recall(\"z\", 5)\nremember q as 2":                          CodeWrongType,
		`remember "a" as nothing`:                                            CodeWrongType,
		`remember "a" as left`:                                               CodeWrongType,
		`remember "a" as hero`:                                               CodeWrongType,
		`remember "a" as [1, [2, left]]`:                                     CodeWrongType,
		"var b = 1000000\nrepeat 60 times\n  b = b * b\nremember \"a\" as b": CodeWrongType,
		"var q = recall(\"z\", 5)\nprint recall(q, 1)":                       CodeWrongType,
		"var q = recall(\"z\", \"a\")\nprint sin(q)":                         CodeWrongType,
	} {
		_, errs := runStart(t, src)
		if len(errs) != 1 || errs[0].Code != code {
			t.Errorf("%s: %v", src, errs)
		}
	}
}

func TestRememberNestedListDepthAndCycles(t *testing.T) {
	_, errs := runStart(t, "var a = []\nadd a to a\nremember \"c\" as a")
	if len(errs) != 1 || errs[0].Code != CodeWrongType {
		t.Fatalf("%v", errs)
	}
	_, errs = runStart(t, "remember \"c\" as [[[[1]]]]")
	if len(errs) != 0 {
		t.Fatal(errs[0].Format())
	}
}

func TestSinCosInScripts(t *testing.T) {
	logs, errs := runStart(t, "print sin(90)\nprint cos(180)\nprint sin(30)\nprint cos(0)\nprint sin(-90)\nprint round(sin(45) * 1000)")
	if len(errs) != 0 || strings.Join(logs, ",") != "1,-1,0.5,1,-1,707" {
		t.Fatalf("%v %v", logs, errs)
	}
	_, errs = runStart(t, "var b = 1000000\nrepeat 60 times\n  b = b * b\nprint sin(b)")
	if len(errs) != 1 {
		t.Fatalf("%v", errs)
	}
}

func TestSharedExportImportAndObjects(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "shared score = 5\nshared items = [1, [2]]\nshared who = nothing\nwhen start\n  who = self\n  score += 1\n", h)
	e.RegisterProgram(in.Program())
	step(e, in)
	out := e.ExportShared()
	if out["score"].N != 6 || out["who"].K != KNothing {
		t.Fatalf("%v", out)
	}
	out["items"].L.Items[0] = Num(99) // a copy: does not touch the engine
	if v, _ := e.Shared("items"); v.L.Items[0].N != 1 {
		t.Fatal("export shares lists")
	}
	e2 := NewEngine(newHost(60))
	e2.ImportShared(out)
	e2.RegisterProgram(in.Program()) // does not reset what was imported
	if v, _ := e2.Shared("score"); v.N != 6 {
		t.Fatalf("%v", v)
	}
	_ = fmt.Sprint()
}
