package vm

import (
	"math"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/tdsl"
)

func TestNonFiniteAndHugeNumbersNeverReachTheWorld(t *testing.T) {
	pre := "var v = 10\nrepeat 400 times\n  v *= 10\nvar z = v - v\n"
	cases := []string{
		"x = v", "y = z", "move right by v", "move left by z", "move to v, 0", "move to 0, z",
		"width = 2000000000", "x = -2000000000", "turn by v", "x += 1000000001",
	}
	for _, body := range cases {
		h := newHost(60)
		e, in := setup(t, "when start\n"+indent(pre+body), h)
		errs := step(e, in)
		if len(errs) != 1 || errs[0].Code != "TD205" || !strings.Contains(errs[0].Message, "вне допустимых пределов") {
			t.Errorf("%q: %v", body, errs)
		}
		if h.x() != 0 || h.y() != 0 || h.props[PropWidth].N != 16 || h.props[PropAngle].N != 0 {
			t.Errorf("%q changed the world: %v %v", body, h.x(), h.y())
		}
	}
	// large but allowed values pass
	h := newHost(60)
	e, in := setup(t, "when start\n  x = 1000000000\n  y = -999999999.5\n", h)
	if errs := step(e, in); len(errs) != 0 || h.x() != 1e9 {
		t.Fatalf("%v", errs)
	}
	// and arithmetic itself may overflow without a crash
	logs, errs := runStart(t, pre+"print v\nprint z\nprint v > 5")
	if len(errs) != 0 || strings.Join(logs, ",") != "inf,nan,yes" {
		t.Fatalf("%v %v", logs, errs)
	}
}

func TestBudgetExhaustedDuringStartupIsDeferredNotLost(t *testing.T) {
	h := newHost(60)
	e := NewEngine(h)
	e.Limits.HandlerSteps = 1000
	e.Limits.FrameSteps = 1000
	hog := e.NewInstance(compile(t, "when update\n  while yes\n    x += 1\n"), 1)
	src := "var a = 7\nwhen start\n  print \"s1\"\nwhen start\n  print \"s2\"\nwhen every 1 seconds\n  y += 1\n"
	late := e.NewInstance(compile(t, src), 1)

	e.BeginFrame()
	hog.RunPhase(PhaseUpdate) // eats the whole frame
	if errs := late.RunPhase(PhaseStart); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
	if len(h.logs) != 0 {
		t.Fatalf("started with an empty budget: %v", h.logs)
	}
	if v, _ := late.Global("a"); v.K != KNothing {
		t.Fatal("initialized with an empty budget")
	}
	e.BeginFrame()
	if errs := late.RunPhase(PhaseStart); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
	if v, _ := late.Global("a"); v.N != 7 || strings.Join(h.logs, ",") != "s1,s2" {
		t.Fatalf("a=%v logs=%v", v, h.logs)
	}
	e.BeginFrame()
	late.RunPhase(PhaseStart)
	if len(h.logs) != 2 {
		t.Fatal("start ran twice")
	}
}

func TestBudgetExhaustedInTheMiddleOfInitializers(t *testing.T) {
	h := newHost(60)
	e := NewEngine(h)
	e.Limits.FrameSteps = 400
	src := `var a = tally(20)
var b = tally(20)
var c = tally(20)
to tally(n)
  var i = 0
  while i < n
    i += 1
  return i
when start
  print "{a} {b} {c}"
`
	in := e.NewInstance(compile(t, src), 1)
	var errs []*Error
	frames := 0
	for ; len(h.logs) == 0 && frames < 10; frames++ {
		e.BeginFrame()
		errs = append(errs, in.RunPhase(PhaseStart)...)
	}
	if len(errs) != 0 || frames < 2 || strings.Join(h.logs, ",") != "20 20 20" {
		t.Fatalf("frames=%d errs=%v logs=%v", frames, errs, h.logs)
	}
}

func TestBigOperationsArePaidForInSteps(t *testing.T) {
	big := &List{}
	for i := 0; i < 100_000; i++ {
		big.Items = append(big.Items, Num(float64(100_000-i)))
	}
	text := strings.Repeat("a", 9000)
	cases := map[string]string{
		"in":     "to probe(l)\n  repeat 100 times\n    if 0 in l\n      return 1\n",
		"sort":   "to probe(l)\n  sort l\n  sort l\n  sort l\n  sort l\n  sort l\n  sort l\n",
		"remove": "to probe(l)\n  repeat 100 times\n    remove 0 from l\n",
		"text":   "to probe(l)\n  var t = \"" + text + "\"\n  repeat 100 times\n    if \"b\" in t\n      return 1\n",
		"concat": "to probe(l)\n  var t = \"" + text + "\"\n  repeat 100 times\n    var u = t + \"b\"\n",
		"length": "to probe(l)\n  var t = \"" + text + "\"\n  repeat 100 times\n    var n = t.length\n",
	}
	for name, src := range cases {
		h := newHost(60)
		_, in := setup(t, src, h)
		_, err := in.Call("probe", ListValue(big))
		if err == nil || err.Code != "TD401" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// comparing two long, equal, distinct lists is paid for as well
	big2 := &List{Items: append([]Value(nil), big.Items...)}
	_, in := setup(t, "to probe(a, b)\n  repeat 100 times\n    var r = a is b\n", newHost(60))
	if _, err := in.Call("probe", ListValue(big), ListValue(big2)); err == nil || err.Code != "TD401" {
		t.Errorf("equality: %v", err)
	}
	// a small list is cheap: the same loops pass
	small := NewList(Num(1), Num(2))
	_, in = setup(t, cases["in"], newHost(60))
	if _, err := in.Call("probe", small); err != nil {
		t.Fatal(err)
	}
}

func TestCycleCheckIsALimitErrorNotAFalseAccusation(t *testing.T) {
	deep := NewList()
	for i := 0; i < 100; i++ {
		deep = NewList(deep)
	}
	h := newHost(60)
	_, in := setup(t, "to probe(target, item)\n  add item to target\n", h)
	_, err := in.Call("probe", NewList(), deep)
	if err == nil || err.Code != "TD402" || strings.Contains(err.Message, "в самого себя") || !strings.Contains(err.Message, "вложенность") {
		t.Fatalf("%v", err)
	}
	// real self-inclusion is still named as such
	l := NewList()
	if _, err := in.Call("probe", l, l); err == nil || !strings.Contains(err.Message, "в самого себя") {
		t.Fatalf("%v", err)
	}
	// a shallow nested list is fine
	if _, err := in.Call("probe", NewList(), NewList(NewList(Num(1)))); err != nil {
		t.Fatal(err)
	}
}

func TestPanicInsideTheVMBecomesAnErrorAndTheGameGoesOn(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when update\n  x += 1\nwhen update\n  y += 1\nwhen start\n  print \"s\"\n", h)
	step(e, in)
	// corrupt the first handler: a local slot that does not exist
	hd := &in.prog.Handlers[0]
	hd.fn = &Func{Name: "bad", Code: []Instr{{Op: OpLoadLocal, A: 999}}, Spans: []Span{{Line: 2, Col: 3, EndCol: 4}}}
	errs := step(e, in)
	if len(errs) != 1 || errs[0].Code != CodeInternal || !strings.Contains(errs[0].Message, "Внутренняя ошибка") || errs[0].Handler != "when update" {
		t.Fatalf("%v", errs)
	}
	if h.y() != 2 {
		t.Fatalf("second handler stopped: y=%v", h.y())
	}
	// unknown opcode, bad jump, stack underflow: all caught
	for _, code := range [][]Instr{{{Op: OpJump, A: 77}}, {{Op: OpAdd}}, {{Op: Op(200)}}, {{Op: OpCall, A: 5, B: 0}}, {{Op: OpConst, A: -1}}} {
		hd.fn = &Func{Name: "bad", Code: code, Spans: make([]Span, len(code))}
		errs := step(e, in)
		if len(errs) != 1 || (errs[0].Code != CodeInternal && errs[0].Code != CodeUnsupported) {
			t.Errorf("%v: %v", code, errs)
		}
	}
	// and the step budget stays sane after panics
	if e.StepsLeft() < 0 || e.StepsLeft() > e.Limits.FrameSteps {
		t.Fatalf("budget %d", e.StepsLeft())
	}
	// a panic while starting up
	h2 := newHost(60)
	e2, in2 := setup(t, "var a = 1\n", h2)
	in2.prog.Inits[0] = &Func{Name: "bad", Code: []Instr{{Op: OpStoreGlobal, A: 99}}, Spans: make([]Span, 1)}
	if errs := step(e2, in2); len(errs) != 1 || errs[0].Code != CodeInternal {
		t.Fatalf("%v", errs)
	}
}

func TestPrintIsLimitedAndTruncated(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when update\n  repeat 1001 times\n    print \"m\"\n", h)
	errs := step(e, in)
	if len(h.logs) != 1000 || len(errs) != 1 || errs[0].Code != "TD402" || !strings.Contains(errs[0].Message, "print") {
		t.Fatalf("logs=%d errs=%v", len(h.logs), errs)
	}
	h.logs = nil
	step(e, in) // a new frame: the allowance is back
	if len(h.logs) != 1000 {
		t.Fatalf("second frame logs=%d", len(h.logs))
	}
	h2 := newHost(60)
	e2, in2 := setup(t, "when start\n  var s = \"ж\"\n  repeat 12 times\n    s = s + s\n  print s\n", h2)
	step(e2, in2)
	if n := len([]rune(h2.logs[0])); n != 1001 || !strings.HasSuffix(h2.logs[0], "…") {
		t.Fatalf("len=%d", n)
	}
}

func TestSnapshotRestore(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var items = [1, [2]]\nvar n = 3\nwhen update\n  n += 1\n  add n to items\n", h)
	step(e, in)
	snap := in.Snapshot()
	step(e, in)
	in2 := e.NewInstance(in.prog, 1)
	if !in2.Restore(snap) {
		t.Fatal("restore refused")
	}
	n, _ := in2.Global("n")
	items, _ := in2.Global("items")
	if n.N != 4 || items.String() != "[1, [2], 4]" {
		t.Fatalf("n=%v items=%v", n, items)
	}
	// the snapshot is a copy: later changes do not leak into it
	step(e, in2)
	again := e.NewInstance(in.prog, 1)
	again.Restore(snap)
	if v, _ := again.Global("items"); v.String() != "[1, [2], 4]" {
		t.Fatalf("snapshot mutated: %v", v)
	}
	// started state came along: no second "when start"
	other := e.NewInstance(compile(t, "when start\n  print 1\n"), 1)
	if other.Restore(snap) {
		t.Fatal("restored a snapshot of another program")
	}
	_ = tdsl.KeyHeld
	_ = math.Pi
}
