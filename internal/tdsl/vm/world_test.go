package vm

import (
	"fmt"
	"strings"
	"testing"
)

// worldHost is a fakeHost with a tiny scripted world: the objects another one
// overlaps are whatever the test says.
type worldHost struct {
	*fakeHost
	overlap map[Target][]ObjID
	alive   map[ObjID]bool
	names   map[string]ObjID
	spawned int
}

func newWorldHost() *worldHost {
	return &worldHost{fakeHost: newHost(60), overlap: map[Target][]ObjID{}, alive: map[ObjID]bool{1: true, 2: true, 3: true, 4: true}, names: map[string]ObjID{"Door": 4}}
}

func (h *worldHost) Exists(id ObjID) bool { return h.alive[id] }
func (h *worldHost) Find(n string) (ObjID, bool) {
	id, ok := h.names[n]
	return id, ok
}
func (h *worldHost) Overlapping(self ObjID, t Target, buf []ObjID) ([]ObjID, int, error) {
	return append(buf, h.overlap[t]...), len(h.overlap[t]), nil
}
func (h *worldHost) Spawn(tpl string, x, y float64) (ObjID, int, error) {
	h.spawned++
	return ObjID(100 + h.spawned), 1, nil
}

func (h *worldHost) engine(t *testing.T, src string) (*Engine, *Instance) {
	e := NewEngine(h)
	return e, e.NewInstance(compile(t, src), 1)
}

func TestTouchEnterExitAndOtherInActions(t *testing.T) {
	h := newWorldHost()
	e, in := h.engine(t, `to report(word)
  print "{word} {other.x}"
when touch "coin"
  report("in")
when stop touching "coin"
  report("out")
`)
	tag := Target{Tag: "coin"}
	h.overlap[tag] = []ObjID{2, 3}
	step(e, in)
	if strings.Join(h.logs, "|") != "in 0|in 0" {
		t.Fatalf("logs %v", h.logs)
	}
	h.logs = nil
	step(e, in) // still touching both: nothing
	if len(h.logs) != 0 {
		t.Fatalf("logs %v", h.logs)
	}
	h.overlap[tag] = []ObjID{3}
	step(e, in)
	if strings.Join(h.logs, "|") != "out 0" {
		t.Fatalf("logs %v", h.logs)
	}
	h.logs = nil
	// an object that is gone is not reported as "stop touching"
	h.alive[3] = false
	h.overlap[tag] = nil
	step(e, in)
	if len(h.logs) != 0 {
		t.Fatalf("logs %v", h.logs)
	}
}

func TestTouchTargetsFromExpressions(t *testing.T) {
	h := newWorldHost()
	e, in := h.engine(t, "var what = \"a\"\nwhen touch what\n  print \"got\"\nwhen touch Door\n  print \"door\"\n")
	h.overlap[Target{Tag: "a"}] = []ObjID{2}
	h.overlap[Target{Obj: 4, HasObj: true}] = []ObjID{4}
	step(e, in)
	if strings.Join(h.logs, "|") != "got|door" {
		t.Fatalf("logs %v", h.logs)
	}
	// a number is not a target
	e2, in2 := h.engine(t, "var what = pick([5])\nwhen touch what\n  print \"x\"\n")
	if errs := step(e2, in2); len(errs) != 1 || errs[0].Code != CodeWrongType {
		t.Fatalf("errors %v", errs)
	}
}

func TestMessagesOrderLimitsAndBacklog(t *testing.T) {
	h := newWorldHost()
	e, in := h.engine(t, `var seen = ""
when message "a"
  seen = seen + "a{value}"
when message "b"
  seen = seen + "b"
  send "a" with 9
when start
  send "a" with 1
  send "b"
  send "a" with 2
when every 1 seconds
  print seen
`)
	errs := step(e, in)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if v, _ := in.Global("seen"); v.S != "a1ba2" {
		t.Fatalf("seen %q", v.S)
	}
	// the "a" sent while delivering waits for the next frame
	if e.Pending() != 1 {
		t.Fatalf("pending %d", e.Pending())
	}
	step(e, in)
	if v, _ := in.Global("seen"); v.S != "a1ba2a9" {
		t.Fatalf("seen %q", v.S)
	}

	// the per-frame limit
	h = newWorldHost()
	e, in = h.engine(t, "when update\n  repeat 1500 times\n    send \"x\"\n")
	errs = step(e, in)
	if len(errs) != 1 || errs[0].Code != CodeTooBig || !strings.Contains(errs[0].Message, "1000") {
		t.Fatalf("errors %v", errs)
	}
	if e.Pending() != 0 { // delivered (nobody listens) at the end of that frame
		t.Fatalf("pending %d", e.Pending())
	}

	// the budget runs out in the middle of delivery: the rest stays queued, in order
	h = newWorldHost()
	e, in = h.engine(t, "var seen = \"\"\nwhen message \"m\"\n  seen = seen + \"{value}\"\nwhen start\n  for each i in 1 to 5\n    send \"m\" with i\n")
	e.BeginFrame()
	in.RunPhase(PhaseInit)
	in.RunPhase(PhaseStart)
	e.frameLeft = 0
	e.Deliver([]*Instance{in})
	if e.Pending() != 5 {
		t.Fatalf("pending %d", e.Pending())
	}
	e.BeginFrame()
	e.Deliver([]*Instance{in})
	if v, _ := in.Global("seen"); v.S != "12345" {
		t.Fatalf("seen %q", v.S)
	}

	// the backlog limit
	h = newWorldHost()
	e, in = h.engine(t, "when update\n  send \"x\"\n")
	e.Limits.Backlog = 3
	e.BeginFrame()
	e.queue = append(e.queue, Message{Text: "old"}, Message{Text: "old"}) // nobody delivers these
	if errs := in.RunPhase(PhaseUpdate); len(errs) != 0 || e.Pending() != 3 {
		t.Fatalf("errors %v pending %d", errs, e.Pending())
	}
	e.BeginFrame()
	if errs := in.RunPhase(PhaseUpdate); len(errs) != 1 || errs[0].Code != CodeTooBig || e.Pending() != 3 {
		t.Fatalf("errors %v pending %d", errs, e.Pending())
	}
}

func TestSendToAnObject(t *testing.T) {
	h := newWorldHost()
	e, in := h.engine(t, "when start\n  send \"hi\" to Door\n  send \"hi\" to nothing\n")
	e2in := e.NewInstance(compile(t, "when message \"hi\"\n  print \"door got hi\"\n"), 4)
	_ = e2in
	errs := step(e, in)
	if len(errs) != 1 || errs[0].Code != CodeGone {
		t.Fatalf("errors %v", errs)
	}
	e.Deliver(nil)
	if strings.Join(h.logs, "|") != "door got hi" {
		t.Fatalf("logs %v", h.logs)
	}
}

func TestSharedIsCreatedOnceByTheFirstDeclarer(t *testing.T) {
	h := newWorldHost()
	e := NewEngine(h)
	p1 := compile(t, "shared total = 5 + 5\nshared list = [1]\n")
	p2 := compile(t, "shared total = 99\nwhen start\n  total += 1\n  add 2 to list\n  print \"{total} {list}\"\n")
	if errs := e.RegisterProgram(p1); len(errs) != 0 {
		t.Fatal(errs)
	}
	e.RegisterProgram(p2)
	if v, _ := e.Shared("total"); v.N != 10 {
		t.Fatalf("total %v", v)
	}
	in := e.NewInstance(p2, 1)
	step(e, in)
	if strings.Join(h.logs, "|") != "11 [1, 2]" {
		t.Fatalf("logs %v", h.logs)
	}
	// assigning to a name that is neither a shared variable nor a variable fails
	in2 := e.NewInstance(compile(t, "when start\n  nope = 1\n"), 2)
	if errs := step(e, in2); len(errs) != 1 || errs[0].Code != CodeUnknownName {
		t.Fatalf("errors %v", errs)
	}
	// a failing initializer leaves nothing and reports
	p3 := compile(t, "shared bad = 1 / 0\n")
	if errs := e.RegisterProgram(p3); len(errs) != 1 || errs[0].Code != CodeDivZero {
		t.Fatalf("errors %v", errs)
	}
	if v, ok := e.Shared("bad"); !ok || v.K != KNothing {
		t.Fatalf("bad %v %v", v, ok)
	}
}

func TestObjectVariablesOfOtherInstances(t *testing.T) {
	h := newWorldHost()
	e := NewEngine(h)
	hero := e.NewInstance(compile(t, "var hp = 100\nwhen update\n  print hp\n"), 4) // Door is object 4
	e.NewInstance(compile(t, "when update\n  Door.hp -= 30\n  print Door.hp\n  print Door.zip\n"), 1)
	step(e, hero)
	if v, _ := hero.Global("hp"); v.N != 100 { // the hero's own start-up: initialised in its first frame
		t.Fatalf("hp %v", v)
	}
	in := e.InstanceOf(1)
	errs := step(e, in)
	if len(errs) != 1 || errs[0].Code != CodeNoProp {
		t.Fatalf("errors %v", errs)
	}
	if v, _ := hero.Global("hp"); v.N != 70 {
		t.Fatalf("hp %v", v)
	}
	e.Detach(4)
	h.alive[4] = false
	if errs := step(e, in); len(errs) != 1 || errs[0].Code != CodeGone {
		t.Fatalf("errors %v", errs)
	}
}

func TestSpawnAndFindThroughHost(t *testing.T) {
	h := newWorldHost()
	e, in := h.engine(t, "when start\n  var s = spawn \"Star\" at 1, 2\n  print exists(s)\n  var t = spawn Star at 100000000000, 0\n")
	errs := step(e, in)
	if len(errs) != 1 || errs[0].Code != CodeWrongType { // 1e10 is out of range; the first spawn worked
		t.Fatalf("errors %v", errs)
	}
	if h.spawned != 1 {
		t.Fatalf("spawned %d", h.spawned)
	}
}

func TestLibrariesAreLinkedAndTheirErrorsQuoteTheLibrary(t *testing.T) {
	lib := "to ping()\n  print \"lib ping\"\n\nto boom()\n  print 1 / 0\n\nto own()\n  print \"from lib\"\n"
	load := func(path string) (string, string, error) {
		if path == "lib.tdsl" {
			return "scripts/lib.tdsl", lib, nil
		}
		return "", "", fmt.Errorf("no such file")
	}
	src := "use \"lib.tdsl\"\nuse \"gone.tdsl\"\nto own()\n  print \"mine\"\nwhen start\n  ping()\n  own()\n  boom()\n"
	p, errs := CompileSourceLibs("a.tdsl", src, nil, load)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "gone.tdsl") {
		t.Fatalf("warnings %v", p.Warnings)
	}
	h := newHost(60)
	e := NewEngine(h)
	in := e.NewInstance(p, 1)
	errs2 := step(e, in)
	if strings.Join(h.logs, "|") != "lib ping|mine" {
		t.Fatalf("logs %v", h.logs)
	}
	if len(errs2) != 1 || errs2[0].File != "scripts/lib.tdsl" || errs2[0].Line != 5 || !strings.Contains(errs2[0].Format(), "print 1 / 0") {
		t.Fatalf("errors %+v", errs2)
	}
	// a command nobody defines is an error when reached, not a crash
	p2, _ := CompileSourceLibs("b.tdsl", "use \"lib.tdsl\"\nwhen start\n  nothing_here()\n", nil, load)
	in2 := NewEngine(newHost(60)).NewInstance(p2, 1)
	e2 := in2.eng
	if errs := step(e2, in2); len(errs) != 1 || errs[0].Code != CodeUnknownName {
		t.Fatalf("errors %v", errs)
	}
}

func TestMouseAndClick(t *testing.T) {
	h := newWorldHost()
	e, in := h.engine(t, "when click\n  print \"click\"\nwhen update\n  print mouse.x + mouse.y\n  print mouse.held\n")
	step(e, in)
	if strings.Join(h.logs, "|") != "0|no" {
		t.Fatalf("logs %v", h.logs)
	}
	_, errs := runStart(t, "print mouse.speed\n")
	if len(errs) != 1 || errs[0].Code != CodeNoProp {
		t.Fatalf("errors %v", errs)
	}
}

// A frame that runs out of steps in the middle of a broadcast must not lose
// the rest of that message: it goes on from the next recipient, in order.
func TestBroadcastResumesAfterBudgetRunsOut(t *testing.T) {
	h := newHost(60)
	e := NewEngine(h)
	prog := compile(t, "var hits = 0\nvar last = 0\nwhen message \"m\"\n  hits += 1\n  last = value\n")
	var order []*Instance
	for id := 1; id <= 20; id++ {
		in := e.NewInstance(prog, ObjID(id))
		in.Initialize()
		order = append(order, in)
	}
	e.queue = []Message{{Text: "m", Value: Num(1)}, {Text: "m", Value: Num(2)}}
	frames := 0
	for e.Pending() > 0 && frames < 1000 {
		e.BeginFrame()
		e.frameLeft = 40 // far too little for 2 x 20 deliveries
		e.Deliver(order)
		frames++
	}
	if e.Pending() != 0 {
		t.Fatalf("messages still queued after %d frames", frames)
	}
	if frames < 3 {
		t.Fatalf("the budget never ran out in the middle (%d frames)", frames)
	}
	for i, in := range order {
		if v, _ := in.Global("hits"); v.N != 2 {
			t.Fatalf("object %d got %v messages, want exactly 2", i+1, v.N)
		}
	}
	// order is kept: the second message came last to everybody
	for i, in := range order {
		if v, _ := in.Global("last"); v.N != 2 {
			t.Fatalf("object %d saw message %v last, want 2", i+1, v.N)
		}
	}
}

// The charge for a broadcast must not depend on the frames before it.
func TestPairChargeResetsEachFrame(t *testing.T) {
	e := NewEngine(newHost(60))
	e.pairs = 3
	e.BeginFrame()
	if e.pairs != 0 {
		t.Fatalf("pairs = %d after BeginFrame", e.pairs)
	}
}
