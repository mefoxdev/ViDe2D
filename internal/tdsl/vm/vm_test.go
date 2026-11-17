package vm

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestExpressions(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"1 + 2 * 3", "7"},
		{"(1 + 2) * 3", "9"},
		{"10 / 4", "2.5"},
		{"7 % 3", "1"},
		{"-7 % 3", "2"}, // floored remainder
		{"7 % -3", "-2"},
		{"-(2 + 3)", "-5"},
		{"2 - 3 - 4", "-5"},
		{"0.1 * 3 + 0.2", "0.5"},
		{"1 / 3", "0.333333333333"},
		{`"a" + "b"`, "ab"},
		{`"Счёт: {1 + 2}"`, "Счёт: 3"},
		{`"{yes} {no} {nothing} {left}"`, "yes no nothing left"},
		{`"x\{y\}"`, "x{y}"},
		{"1 < 2", "yes"},
		{"2 <= 1", "no"},
		{`"a" < "b"`, "yes"},
		{"3 is 3", "yes"},
		{"3 is not 3", "no"},
		{`"a" is "a"`, "yes"},
		{"left is left", "yes"},
		{"left is opposite(right)", "yes"},
		{"nothing is nothing", "yes"},
		{"1 is nothing", "no"},
		{"[1, 2] is [1, 2]", "yes"},
		{"[1, 2] is [1, 3]", "no"},
		{"yes and no", "no"},
		{"yes or no", "yes"},
		{"not yes", "no"},
		{"not 1 is 2", "yes"},
		{"no and 1 / 0 is 1", "no"}, // short circuit: no division
		{"yes or 1 / 0 is 1", "yes"},
		{"2 in [1, 2, 3]", "yes"},
		{`"x" not in ["a"]`, "yes"},
		{`"ab" in "cabd"`, "yes"},
		{"[1, 2, 3][2]", "2"},
		{"[1, 2, 3].length", "3"},
		{`[10, 20, 30].first`, "10"},
		{`[10, 20, 30].last`, "30"},
		{`"привет".length`, "6"},
		{"[1, [2, 3]]", "[1, [2, 3]]"},
		{`["a", "b"]`, `["a", "b"]`},
		{"min(3, 2)", "2"},
		{"max(3, 2)", "3"},
		{"abs(-4)", "4"},
		{"round(2.5)", "3"},
		{"round(-2.5)", "-3"},
		{"round(2.4)", "2"},
		{"floor(-1.5)", "-2"},
		{"ceil(1.2)", "2"},
		{"sqrt(16)", "4"},
		{`upper("abc")`, "ABC"},
		{`number("12.5") + 1`, "13.5"},
		{"opposite(up)", "down"},
		{"60 per second", "1"},
		{"123456789012", "123456789012"},
	}
	for _, c := range cases {
		logs, errs := runStart(t, "print "+c.expr)
		if len(errs) > 0 {
			t.Errorf("%s: %v", c.expr, errs[0])
			continue
		}
		if len(logs) != 1 || logs[0] != c.want {
			t.Errorf("%s = %q, want %q", c.expr, logs, c.want)
		}
	}
}

func TestPerSecondUsesTickRate(t *testing.T) {
	for _, rate := range []int{60, 90, 120} {
		h := newHost(rate)
		e, in := setup(t, "when start\n  print 180 per second\n", h)
		step(e, in)
		want := FormatNum(float64(180 / float64(rate)))
		if h.logs[0] != want {
			t.Errorf("rate %d: %q, want %q", rate, h.logs[0], want)
		}
	}
}

// FMA pin: results that differ if the Go compiler fuses a multiply and an add.
func TestNoFusedMultiplyAdd(t *testing.T) {
	h := newHost(60)
	src := `
var a = 134217729
var b = 0.1
var c = 3
when start
  x = a * a - 18014398777917440
  y = b * c + 0.2
  width = b * c
  height = width + 0.2
`
	e, in := setup(t, src, h)
	if errs := step(e, in); len(errs) > 0 {
		t.Fatal(errs[0])
	}
	// a*a is not representable: rounded to 2^54+2^28, so the difference is 0.
	// A fused multiply-subtract would give 1.
	if h.x() != 0 {
		t.Errorf("a*a - c = %v, want 0 (fused multiply-add?)", h.x())
	}
	// Pinned bit patterns of 0.1*3 and 0.1*3+0.2 computed without fusion.
	if got := math.Float64bits(h.props[PropWidth].N); got != 0x3fd3333333333334 {
		t.Errorf("0.1*3 = %#x", got)
	}
	if got := math.Float64bits(h.y()); got != 0x3fe0000000000000 {
		t.Errorf("0.1*3+0.2 = %#x, want 0.5 (%#x)", got, uint64(0x3fe0000000000000))
	}
	if got := math.Float64bits(h.props[PropHeight].N); got != math.Float64bits(h.y()) {
		t.Errorf("split and combined differ: %#x vs %#x", got, math.Float64bits(h.y()))
	}
}

func TestVariablesAndAssignment(t *testing.T) {
	logs, errs := runStart(t, `var n = 10
n += 5
n -= 3
n *= 2
n /= 4
print n
var s = "a"
s += "b"
print s
var items = [1, 2, 3]
items[2] = 20
items[3] += 1
print items
x = 5
x += 2
print x
self.y = 9
print self.y
print width + 1
flip = yes
print flip
`)
	if len(errs) > 0 {
		t.Fatal(errs[0])
	}
	want := "6|ab|[1, 20, 4]|7|9|17|yes"
	if got := strings.Join(logs, "|"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestObjectVariablesPersistAcrossHandlers(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, `var hp = 3
var names = ["a"]
when start
  hp = hp + 1
  add "b" to names
when update
  hp += 1
`, h)
	step(e, in)
	step(e, in)
	if v, _ := in.Global("hp"); v.N != 6 {
		t.Fatalf("hp = %v", v.N)
	}
	if v, _ := in.Global("names"); v.String() != `["a", "b"]` {
		t.Fatalf("names = %v", v)
	}
}

func TestControlFlow(t *testing.T) {
	logs, errs := runStart(t, `var n = 5
if n > 10
  print "big"
else if n > 3
  print "mid"
else
  print "small"
var i = 0
while i < 3
  i += 1
print i
repeat 3 times
  print "r"
repeat 2.5 times
  print "f"
repeat 0 times
  print "never"
for each item in ["a", "b"]
  print item
for each k in 2 to 4
  print k
for each k in 5 to 1
  print "never"
var total = 0
repeat forever
  total += 1
  if total is 4
    stop repeating
print total
var odd = 0
for each j in 1 to 6
  if j % 2 is 0
    next round
  odd += j
print odd
var m = 0
for each a in 1 to 3
  for each b in 1 to 3
    if b is 2
      stop repeating
    m += 1
print m
`)
	if len(errs) > 0 {
		t.Fatal(errs[0])
	}
	want := "mid|3|r|r|r|f|f|a|b|2|3|4|4|9|3"
	if got := strings.Join(logs, "|"); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestLoopVariableIsACopy(t *testing.T) {
	logs, _ := runStart(t, `var c = 0
for each i in 1 to 3
  i = 100
  c += 1
print c
var xs = [1, 2]
for each v in xs
  if v is 1
    add 9 to xs
print xs.length
`)
	if got := strings.Join(logs, "|"); got != "3|3" {
		t.Fatalf("got %q", got)
	}
}

func TestToCommands(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, `var hp = 50
to heal(amount)
  hp = min(hp + amount, 100)
to double(n)
  return n * 2
to fact(n)
  if n <= 1
    return 1
  return n * fact(n - 1)
to noreturn()
  hp += 0
to even(n)
  if n is 0
    return yes
  return odd(n - 1)
to odd(n)
  if n is 0
    return no
  return even(n - 1)
to local_scope(n)
  var t = n + 1
  return t
when start
  heal(30)
  print hp
  heal(30)
  print hp
  print "стало {double(hp)}"
  print fact(10)
  print noreturn()
  print even(10)
  print local_scope(1) + local_scope(2)
`, h)
	if errs := step(e, in); len(errs) > 0 {
		t.Fatal(errs[0])
	}
	want := "80|100|стало 200|3628800|nothing|yes|5"
	if got := strings.Join(h.logs, "|"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	v, err := in.Call("double", Num(21))
	if err != nil || v.N != 42 {
		t.Fatalf("Call: %v %v", v, err)
	}
}

func TestCallDepthLimit(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, `to spin(n)
  spin(n + 1)
to deep(n)
  if n > 0
    deep(n - 1)
when start
  deep(63)
  print "ok63"
when update
  spin(0)
when every 1 seconds
  print "still alive"
`, h)
	errs := step(e, in)
	if len(errs) != 1 {
		t.Fatalf("errors: %v", errs)
	}
	if errs[0].Code != "TD404" || !strings.Contains(errs[0].Message, "spin") || errs[0].Handler != "when update" {
		t.Fatalf("bad error: %+v", errs[0])
	}
	if len(h.logs) != 1 || h.logs[0] != "ok63" { // 64 nested calls: allowed
		t.Fatalf("logs %v", h.logs)
	}
	// deep(63) makes 64 nested calls: fine; deep(64) makes the 65th: too deep.
	h2 := newHost(60)
	e2, in2 := setup(t, "to deep(n)\n  if n > 0\n    deep(n - 1)\nwhen start\n  deep(63)\n  print \"ok\"\n", h2)
	if errs := step(e2, in2); len(errs) != 0 || len(h2.logs) != 1 {
		t.Fatalf("depth 64 failed: %v %v", errs, h2.logs)
	}
	h3 := newHost(60)
	e3, in3 := setup(t, "to deep(n)\n  if n > 0\n    deep(n - 1)\nwhen start\n  deep(64)\n  print \"ok\"\n", h3)
	if errs := step(e3, in3); len(errs) != 1 || errs[0].Code != "TD404" {
		t.Fatalf("depth 65 should fail: %v", errs)
	}
}

func TestStepLimitStopsOnlyTheHandler(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, `when update
  while yes
    x += 1
when update
  print "second"
when start
  print "start"
`, h)
	errs := step(e, in)
	if len(errs) != 1 || errs[0].Code != "TD401" {
		t.Fatalf("errors: %v", errs)
	}
	if !strings.Contains(errs[0].Message, "бесконечный цикл") {
		t.Fatal(errs[0].Message)
	}
	if strings.Join(h.logs, ",") != "start,second" {
		t.Fatalf("logs %v", h.logs)
	}
	// the game goes on in the next frame
	errs = step(e, in)
	if len(errs) != 1 || strings.Join(h.logs, ",") != "start,second,second" {
		t.Fatalf("frame 2: %v %v", errs, h.logs)
	}
	if e.StepsLeft() < 0 {
		t.Fatal("negative budget")
	}
}

func TestFrameBudgetIsSharedByTheWholeGame(t *testing.T) {
	h := newHost(60)
	e := NewEngine(h)
	e.Limits.HandlerSteps = 1000
	e.Limits.FrameSteps = 2500
	prog := compile(t, "when update\n  while yes\n    x += 1\n")
	var ins []*Instance
	for i := 0; i < 5; i++ {
		ins = append(ins, e.NewInstance(prog, 1))
	}
	e.BeginFrame()
	var errs []*Error
	for _, in := range ins {
		in.RunPhase(PhaseStart)
	}
	e.BeginFrame()
	for _, in := range ins {
		errs = append(errs, in.RunPhase(PhaseUpdate)...)
	}
	// two handlers use 1000 steps each, the third is cut by the frame budget,
	// the remaining two are skipped
	if len(errs) != 3 {
		t.Fatalf("errors: %d", len(errs))
	}
	if !strings.Contains(errs[2].Message, "за кадр") {
		t.Fatalf("third: %v", errs[2].Message)
	}
	if e.Skipped() != 2 {
		t.Fatalf("skipped %d", e.Skipped())
	}
	e.BeginFrame()
	if e.Skipped() != 0 || e.StepsLeft() != 2500 {
		t.Fatal("budget not restored")
	}
}

func TestListIndexErrors(t *testing.T) {
	_, errs := runStart(t, "var items = [\"a\", \"b\", \"c\"]\nprint items[0]\n")
	if len(errs) != 1 {
		t.Fatalf("errs %v", errs)
	}
	e := errs[0]
	if e.Code != "TD302" || e.Line != 3 || e.Handler != "when start" || e.File != "t.tdsl" {
		t.Fatalf("%+v", e)
	}
	for _, want := range []string{"«items»", "номером 0", "Нумерация идёт с единицы", "items[1]", "сейчас 3 элемента"} {
		if !strings.Contains(e.Message+" "+e.Hint, want) {
			t.Errorf("message lacks %q: %s %s", want, e.Message, e.Hint)
		}
	}
	f := e.Format()
	for _, want := range []string{"Во время игры, t.tdsl, строка 3 (TD302):", "print items[0]", "^^^^^^^^", "Обработчик «when start» остановлен. Игра продолжается."} {
		if !strings.Contains(f, want) {
			t.Errorf("Format lacks %q:\n%s", want, f)
		}
	}
	for _, src := range []string{"print [1][2]", "print [][1]", "print [1][-1]", "print [1][1.5]", "var l = [1]\nl[5] = 1", "print pick([])", "print [].first"} {
		_, errs := runStart(t, src)
		if len(errs) != 1 || errs[0].Code != "TD302" {
			t.Errorf("%q: %v", src, errs)
		}
	}
}

func TestDivisionByZero(t *testing.T) {
	for _, src := range []string{"var z = 0\nprint 1 / z", "var z = 0\nprint 1 % z", "var z = 0\nz /= z"} {
		logs, errs := runStart(t, src)
		if len(errs) != 1 || errs[0].Code != "TD301" || len(logs) != 0 {
			t.Errorf("%q: %v %v", src, errs, logs)
			continue
		}
		if errs[0].Line != 3 {
			t.Errorf("line %d", errs[0].Line)
		}
	}
}

func TestDynamicTypeErrors(t *testing.T) {
	cases := []struct{ src, code string }{
		{"var l = [1, \"a\"]\nprint l[1] + l[2]", "TD204"},
		{"var l = [1, \"a\"]\nprint l[2] - 1", "TD205"},
		{"var l = [1, \"a\"]\nprint l[1] is l[2]", "TD201"},
		{"var l = [1, \"a\"]\nprint l[1] < l[2]", "TD201"},
		{"var l = [1, \"a\"]\nif l[1]\n  print 1", "TD202"},
		{"var l = [1, \"a\"]\nprint not l[1]", "TD202"},
		{"var l = [1, \"a\"]\nprint l[1] and yes", "TD202"},
		{"var l = [1, \"a\"]\nvar d = l[1]\nmove d by 1", "TD205"},
		{"var l = [1, \"a\"]\nmove left by l[2]", "TD205"},
		{"var l = [1, \"a\"]\nx = l[2]", "TD205"},
		{"var l = [1, \"a\"]\nrepeat l[2] times\n  print 1", "TD205"},
		{"var l = [1, \"a\"]\nprint number(l[2])", "TD205"},
		{"var l = [1, \"a\"]\nprint -l[2]", "TD205"},
		{"var l = [1, \"a\"]\nprint l[1] per second\nprint l[2] per second", "TD205"},
	}
	for _, c := range cases {
		_, errs := runStart(t, c.src)
		if len(errs) != 1 || errs[0].Code != c.code {
			t.Errorf("%q: %v", c.src, errs)
		}
	}
}

func TestSortShuffleAndListCommands(t *testing.T) {
	logs, errs := runStart(t, `var l = [3, 1, 2]
sort l
print l
add 0 to l
remove 1 from l
print l
var w = ["б", "а"]
sort w
print w
clear l
print l.length
var s = [1, 2, 3, 4, 5, 6]
shuffle s
sort s
print s
`)
	if len(errs) > 0 {
		t.Fatal(errs[0])
	}
	if got := strings.Join(logs, "|"); got != `[1, 2, 3]|[2, 3, 0]|["а", "б"]|0|[1, 2, 3, 4, 5, 6]` {
		t.Fatalf("got %q", got)
	}
	_, errs = runStart(t, "var l = [1, \"a\"]\nsort l")
	if len(errs) != 1 || errs[0].Code != "TD205" {
		t.Fatalf("%v", errs)
	}
}

func TestListAndTextLimits(t *testing.T) {
	h := newHost(60)
	e := NewEngine(h)
	e.Limits.ListItems = 10
	e.Limits.TextChars = 20
	run := func(body string) []*Error {
		in := e.NewInstance(compile(t, "when start\n"+indent(body)), 1)
		return step(e, in)
	}
	cases := []string{
		"var l = []\nrepeat 11 times\n  add 1 to l",
		"var l = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]",
		"var s = \"abcdefghij\"\ns = s + s + s",
		"var s = \"abcdefghij\"\nprint \"{s}{s}{s}\"",
		"var l = []\nadd l to l",
		"var a = []\nvar b = [a]\nadd b to a",
		"var s = \"x\"\nrepeat 10 times\n  s = s + s",
	}
	for _, src := range cases {
		if errs := run(src); len(errs) != 1 || (errs[0].Code != "TD402" && errs[0].Code != "TD205") {
			t.Errorf("%q: %v", src, errs)
		}
	}
	if errs := run("var l = []\nrepeat 10 times\n  add 1 to l\nvar s = \"abcdefghij\" + \"abcdefghij\""); len(errs) != 0 {
		t.Errorf("at the limit: %v", errs[0])
	}
}

func TestRandomIsSeededAndDeterministic(t *testing.T) {
	src := `when start
  repeat 20 times
    print random(1, 6)
  print pick(["a", "b", "c"])
  print random(5, 5)
  print random(3, 1) >= 1
`
	runWith := func(seed uint64) []string {
		h := newHost(60)
		h.seed = seed
		e, in := setup(t, src, h)
		if errs := step(e, in); len(errs) > 0 {
			t.Fatal(errs[0])
		}
		return h.logs
	}
	a, b, c := runWith(1), runWith(1), runWith(2)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatal("same seed, different numbers")
	}
	if strings.Join(a, ",") == strings.Join(c, ",") {
		t.Fatal("different seeds gave the same numbers")
	}
	seen := map[string]bool{}
	for _, s := range a[:20] {
		if s < "1" || s > "6" {
			t.Fatalf("out of range: %s", s)
		}
		seen[s] = true
	}
	if len(seen) < 3 {
		t.Fatalf("suspiciously few distinct values: %v", seen)
	}
	if a[21] != "5" || a[22] != "yes" {
		t.Fatalf("tail %v", a[20:])
	}
}

func TestPCGReferenceSequence(t *testing.T) {
	// pcg32_srandom_r(&rng, 42u, 54u) from the PCG reference demo.
	r := NewPCG(42)
	want := []uint32{0xa15c02b7, 0x7b47f409, 0xba1d3330, 0x83d2f293, 0xbfa4784b, 0xcbed606e}
	for i, w := range want {
		if got := r.Uint32(); got != w {
			t.Fatalf("output %d = %#x, want %#x", i, got, w)
		}
	}
}

func TestPropertiesAndCommands(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, `when start
  move right by 5
  move down by 2.5
  move left by 1
  move up by 1
  show
  hide
  turn by 90
  turn by 45
  text = "hi {x}"
  tint = "red"
  width = 32
  visible = yes
  move to 100, 50
  move right by 1
  print name
`, h)
	if errs := step(e, in); len(errs) > 0 {
		t.Fatal(errs[0])
	}
	if h.x() != 101 || h.y() != 50 {
		t.Fatalf("pos %v,%v", h.x(), h.y())
	}
	if h.props[PropVisible].IsTrue() != true || h.props[PropAngle].N != 135 || h.props[PropText].S != "hi 4" ||
		h.props[PropTint].S != "red" || h.props[PropWidth].N != 32 {
		t.Fatalf("props %+v", h.props)
	}
	if h.logs[0] != "hero" {
		t.Fatal(h.logs)
	}
	// hide really hides
	h2 := newHost(60)
	e2, in2 := setup(t, "when start\n  hide\n", h2)
	step(e2, in2)
	if h2.props[PropVisible].IsTrue() {
		t.Fatal("not hidden")
	}
}

func TestPropertyTypeAndReadOnlyErrors(t *testing.T) {
	cases := []struct{ src, code string }{
		{"var l = [1, \"a\"]\ntext = l[1]", "TD205"},
		{"var l = [1, \"a\"]\nflip = l[1]", "TD205"},
		{"var l = [1, \"a\"]\nprint self.nope", "TD106"},
	}
	for _, c := range cases {
		_, errs := runStart(t, c.src)
		if len(errs) != 1 || errs[0].Code != c.code {
			t.Errorf("%q: %v", c.src, errs)
		}
	}
}

func TestDestroySelf(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when start\n  destroy\n  print \"still runs\"\n", h)
	step(e, in)
	if len(h.destroyed) != 1 || h.destroyed[0] != 1 || len(h.logs) != 1 {
		t.Fatalf("destroyed %v logs %v", h.destroyed, h.logs)
	}
	// a destroyed object: host answers ErrGone -> TD303
	h2 := newHost(60)
	h2.gone = true
	e2, in2 := setup(t, "when start\n  print x\n", h2)
	if errs := step(e2, in2); len(errs) != 1 || errs[0].Code != "TD303" {
		t.Fatalf("%v", errs)
	}
}

func TestEventsOrderAndKeys(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, `when update
  print "update1"
when key space pressed
  print "pressed"
when key a held
  print "held a"
when key "1" released
  print "released 1"
when start
  print "start"
when update
  print "update2"
when key space held
  print "held space"
`, h)
	// frame 1: start, then nothing pressed
	step(e, in)
	if got := strings.Join(h.logs, ","); got != "start,update1,update2" {
		t.Fatalf("frame 1: %s", got)
	}
	h.logs = nil
	h.held["space"], h.pressed["space"], h.held["a"], h.released["1"] = true, true, true, true
	step(e, in)
	// phases: input (in file order: pressed, held a, released 1, held space), then update
	if got := strings.Join(h.logs, ","); got != "pressed,held a,released 1,held space,update1,update2" {
		t.Fatalf("frame 2: %s", got)
	}
	h.logs = nil
	h.pressed["space"] = false
	step(e, in)
	if got := strings.Join(h.logs, ","); got != "held a,released 1,held space,update1,update2" {
		t.Fatalf("frame 3: %s", got)
	}
}

func TestKeyHeldExpression(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when update\n  if key space is held\n    x += 1\n", h)
	step(e, in)
	h.held["space"] = true
	step(e, in)
	step(e, in)
	if h.x() != 2 {
		t.Fatalf("x = %v", h.x())
	}
}

func TestEveryTimersAreTickBased(t *testing.T) {
	for _, rate := range []int{60, 90, 120} {
		h := newHost(rate)
		e, in := setup(t, "var period = 2\nwhen every period seconds\n  x += 1\nwhen every 0.5 seconds\n  y += 1\n", h)
		for i := 0; i < 10*rate; i++ { // ten seconds
			if errs := step(e, in); len(errs) > 0 {
				t.Fatal(errs[0])
			}
		}
		if h.x() != 5 || h.y() != 20 {
			t.Errorf("rate %d: every 2s fired %v times (want 5), every 0.5s %v (want 20)", rate, h.x(), h.y())
		}
	}
}

func TestEveryBadPeriodDisablesOnlyTheTimer(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var p = 0\nwhen every p seconds\n  x += 1\nwhen update\n  y += 1\n", h)
	errs := step(e, in)
	if len(errs) != 1 || errs[0].Code != "TD205" {
		t.Fatalf("%v", errs)
	}
	for i := 0; i < 130; i++ {
		step(e, in)
	}
	if h.x() != 0 || h.y() != 131 {
		t.Fatalf("x=%v y=%v", h.x(), h.y())
	}
}

// Same script and inputs at 60/90/120 Hz: per-second movement over N seconds
// ends at the same place within float rounding.
func TestPerSecondMovementAcrossTickRates(t *testing.T) {
	src := "var speed = 75\nwhen update\n  move right by speed per second\n  y += 30 per second\n"
	for _, rate := range []int{60, 90, 120} {
		h := newHost(rate)
		e, in := setup(t, src, h)
		for i := 0; i < 8*rate; i++ {
			if errs := step(e, in); len(errs) > 0 {
				t.Fatal(errs[0])
			}
		}
		if math.Abs(h.x()-600) > 1e-9 || math.Abs(h.y()-240) > 1e-9 {
			t.Errorf("rate %d: ended at %.12f, %.12f; want 600, 240", rate, h.x(), h.y())
		}
	}
	// and exactly reproducible: two runs give identical bits
	var bits [2]uint64
	for i := range bits {
		h := newHost(90)
		e, in := setup(t, src, h)
		for j := 0; j < 900; j++ {
			step(e, in)
		}
		bits[i] = math.Float64bits(h.x())
	}
	if bits[0] != bits[1] {
		t.Fatal("not reproducible")
	}
}

func TestUnsupportedFeaturesFailCleanly(t *testing.T) {
	src := `when start
  print "before"
  wait 1 second
  print "after"
when update
  play sound "a.wav"
when every 1 seconds
  go to scene "x"
`
	h := newHost(60)
	prog, errs := CompileSource("t.tdsl", src, nil)
	if len(errs) > 0 {
		t.Fatal(errs[0])
	}
	if len(prog.Warnings) != 3 {
		t.Fatalf("warnings: %v", prog.Warnings)
	}
	e := NewEngine(h)
	in := e.NewInstance(prog, 1)
	errs2 := step(e, in)
	if len(errs2) != 2 { // wait in start, play in update (the timer has not fired yet)
		t.Fatalf("errors: %v", errs2)
	}
	for _, er := range errs2 {
		if er.Code != CodeUnsupported || !strings.Contains(er.Message, "не поддерживается") {
			t.Errorf("%+v", er)
		}
	}
	if strings.Join(h.logs, ",") != "before" {
		t.Fatalf("logs %v", h.logs)
	}
	for _, expr := range []string{"print sin(1)", "print recall(\"a\", 0)", "remember \"a\" as 1", "restart scene", "stop music"} {
		p, errs := CompileSource("t.tdsl", "when start\n  "+expr+"\n", nil)
		_ = errs
		if p == nil {
			continue
		}
		h := newHost(60)
		e := NewEngine(h)
		in := e.NewInstance(p, 1)
		if errs := step(e, in); len(errs) != 1 || errs[0].Code != CodeUnsupported {
			t.Errorf("%s: %v", expr, errs)
		}
	}
}

func TestShadowErrorFormatAndMultiline(t *testing.T) {
	_, errs := runStart(t, "var z = 0\nvar q = 1 / z")
	if len(errs) != 1 {
		t.Fatal(errs)
	}
	if !strings.Contains(errs[0].Error(), "TD301") || !strings.Contains(errs[0].Format(), "var q = 1 / z") {
		t.Fatalf("%s\n%s", errs[0].Error(), errs[0].Format())
	}
}

func TestInitErrorStopsOnlyThatInitializer(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "var a = 1 / 0\nvar b = 5\nwhen update\n  x += 1\n", h)
	errs := step(e, in)
	if len(errs) != 1 || errs[0].Code != "TD301" {
		t.Fatalf("%v", errs)
	}
	if a, _ := in.Global("a"); a.K != KNothing {
		t.Fatalf("a = %v, want nothing", a)
	}
	if b, _ := in.Global("b"); b.N != 5 {
		t.Fatalf("b = %v", b)
	}
	if h.x() != 1 {
		t.Fatal("handlers did not run")
	}
	if !strings.Contains(errs[0].Format(), "Запуск скрипта остановлен") {
		t.Fatal(errs[0].Format())
	}
}

func TestHandlerIsNeverRunInsideItself(t *testing.T) {
	h := newHost(60)
	e, in := setup(t, "when update\n  x += 1\n", h)
	step(e, in)
	in.busy[0] = true // as if suspended in "wait"
	step(e, in)
	if h.x() != 1 {
		t.Fatalf("x = %v", h.x())
	}
}

func TestEmptyAndOddPrograms(t *testing.T) {
	for _, src := range []string{"", "# only a comment\n", "var a = 1\n", "to f()\n  return 1\n"} {
		h := newHost(60)
		e, in := setup(t, src, h)
		if errs := step(e, in); len(errs) != 0 {
			t.Errorf("%q: %v", src, errs)
		}
	}
}

func TestFormatNum(t *testing.T) {
	for in, want := range map[float64]string{
		0: "0", math.Copysign(0, -1): "0", 1: "1", -3: "-3", 2.5: "2.5", 0.1 + 0.2: "0.3",
		1e15: "1e+15", 123456789012345: "123456789012345", math.Inf(1): "inf", math.NaN(): "nan", 1.0 / 3: "0.333333333333",
	} {
		if got := FormatNum(in); got != want {
			t.Errorf("FormatNum(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestOpcodeStringsCoverAllOps(t *testing.T) {
	for op := OpNothing; op <= OpFail; op++ {
		if strings.HasPrefix(op.String(), "OP") {
			t.Errorf("op %d has no name", op)
		}
	}
	if fmt.Sprint(Op(250)) == "" {
		t.Fatal()
	}
}
