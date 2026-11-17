package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// spec describes one object of a test scene.
type spec struct {
	name     string
	x, y     int
	w, h     int
	tag      string
	script   string // file name inside scripts/ (the file is written by the test)
	template bool
	node     bool // a plain Node (a group) instead of a Sprite
	kids     []spec
}

func (s spec) json() string {
	kind := "Sprite"
	if s.node {
		kind = "Node"
	}
	extra := ""
	if s.tag != "" {
		extra += fmt.Sprintf(`,"tag":%q`, s.tag)
	}
	if s.script != "" {
		extra += fmt.Sprintf(`,"script":"scripts/%s.tdsl"`, s.script)
	}
	if s.template {
		extra += `,"template":true`
	}
	var kids []string
	for _, k := range s.kids {
		kids = append(kids, k.json())
	}
	return fmt.Sprintf(`{"name":%q,"kind":%q,"x":%d,"y":%d,"w":%d,"h":%d%s,"children":[%s]}`,
		s.name, kind, s.x, s.y, s.w, s.h, extra, strings.Join(kids, ","))
}

// proj is a throw-away project on disk.
type proj struct {
	rate    int
	scripts map[string]string
	files   map[string]string // other project files, by project-relative path
	specs   []spec
	extra   string // extra scene JSON fields
}

func (p *proj) load(t *testing.T) (*World, *InputState, *logSink) {
	t.Helper()
	root := t.TempDir()
	for n, src := range p.scripts {
		write(t, root, "scripts/"+n+".tdsl", src)
	}
	for rel, c := range p.files {
		write(t, root, rel, c)
	}
	rate := p.rate
	if rate == 0 {
		rate = 60
	}
	var kids []string
	for _, s := range p.specs {
		kids = append(kids, s.json())
	}
	write(t, root, "project.vide.json", doc("scene", fmt.Sprintf(`"tick_rate":%d,%s`, rate, p.extra), strings.Join(kids, ",")))
	in, sink := NewInputState(), &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: in, Log: sink.log, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	return w, in, sink
}

func (s *logSink) lines(prefix string) []string {
	var out []string
	for _, m := range s.msgs {
		if strings.HasPrefix(m, prefix) {
			out = append(out, m)
		}
	}
	return out
}

func (s *logSink) join(prefix string) string { return strings.Join(s.lines(prefix), "|") }

func mustNoErrors(t *testing.T, s *logSink) {
	t.Helper()
	if s.count("Во время игры") != 0 || s.count("не запущен") != 0 {
		t.Fatalf("unexpected errors:\n%s", s.all())
	}
}

// ---- touches ----

func TestTouchEnterAndExitOrder(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"mover": `when touch "coin"
  print "in {other.name}"
when stop touching "coin"
  print "out {other.name}"
when update
  x += 4
`},
		specs: []spec{
			{name: "mover", x: 0, y: 0, w: 10, h: 10, script: "mover"},
			{name: "c1", x: 20, y: 0, w: 10, h: 10, tag: "coin"},
			{name: "c2", x: 24, y: 0, w: 10, h: 10, tag: "coin"},
			{name: "far", x: 24, y: 100, w: 10, h: 10, tag: "coin"},
		},
	}).load(t)
	steps(t, w, 20)
	mustNoErrors(t, sink)
	// the mover (x = 4 per step, 10 wide) reaches c1 at x=12 (overlap 2) and c2 at x=16;
	// it leaves c1 when x >= 30 and c2 when x >= 34. Enters and exits are in scene order.
	got := strings.Join(sink.msgs, "|")
	want := "in c1|in c2|out c1|out c2"
	if got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
}

func TestTouchBothEnterInTheSameStepInSceneOrder(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": "when touch \"t\"\n  print \"{other.name}\"\n"},
		specs: []spec{
			{name: "a", x: 0, y: 0, w: 50, h: 50, script: "a"},
			{name: "z", x: 5, y: 5, w: 5, h: 5, tag: "t"},
			{name: "m", x: 10, y: 10, w: 5, h: 5, tag: "t"},
			{name: "b", x: 20, y: 20, w: 5, h: 5, tag: "t"},
		},
	}).load(t)
	steps(t, w, 3)
	if got := sink.join(""); got != "z|m|b" {
		t.Fatalf("got %q", got)
	}
}

func TestEdgeToEdgeIsNotTouching(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when update
  if touching("t")
    print "touch"
  if touching("t", right)
    print "flush"
  if touching("t", left)
    print "left"
`},
		specs: []spec{
			{name: "a", x: 0, y: 0, w: 10, h: 10, script: "a"},
			{name: "b", x: 10, y: 0, w: 10, h: 10, tag: "t"},
		},
	}).load(t)
	steps(t, w, 1)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "flush" {
		t.Fatalf("got %q", got)
	}
}

func TestTouchByNameAndByTag(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when touch Door
  print "door {other.tag}"
when touch "wall"
  print "wall {other.name}"
`},
		specs: []spec{
			{name: "a", x: 0, y: 0, w: 10, h: 10, script: "a"},
			{name: "Door", x: 5, y: 0, w: 10, h: 10, tag: "exit"},
			{name: "Other", x: 5, y: 0, w: 10, h: 10, tag: "wall"},
			{name: "wall", x: 5, y: 0, w: 10, h: 10, tag: "none"}, // a name is not a tag
		},
	}).load(t)
	steps(t, w, 2)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "door exit|wall Other" {
		t.Fatalf("got %q", got)
	}
}

func TestTouchGroupBoxIsUnionOfMembers(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"hero": "when touch \"coin\"\n  print \"got {other.name}\"\n"},
		specs: []spec{
			{name: "hero", node: true, x: 0, y: 0, script: "hero", kids: []spec{
				{name: "body", x: 0, y: 0, w: 8, h: 8},
				{name: "arm", x: 20, y: 0, w: 8, h: 8},
			}},
			{name: "coin", x: 22, y: 2, w: 4, h: 4, tag: "coin"},  // only the arm reaches it
			{name: "coin2", x: 12, y: 2, w: 4, h: 4, tag: "coin"}, // in the gap between: the union box covers it
		},
	}).load(t)
	steps(t, w, 2)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "got coin|got coin2" {
		t.Fatalf("got %q", got)
	}
}

func TestDestroyedObjectDoesNotSendStopTouching(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when touch "coin"
  destroy other
when stop touching "coin"
  print "out"
`},
		specs: []spec{
			{name: "a", x: 0, y: 0, w: 10, h: 10, script: "a"},
			{name: "c", x: 5, y: 0, w: 10, h: 10, tag: "coin"},
		},
	}).load(t)
	steps(t, w, 5)
	mustNoErrors(t, sink)
	if find(w, "c") != nil || len(sink.msgs) != 0 {
		t.Fatalf("c alive=%v log=%v", find(w, "c") != nil, sink.msgs)
	}
}

func TestDestroyOtherAppliesAtEndOfFrame(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when update
  destroy Victim
  print "exists {exists(Victim)} {count("v")}"
  Victim.x += 1
`, "b": "when update\n  print \"b sees {exists(Victim)}\"\n"},
		specs: []spec{
			{name: "a", script: "a", node: true},
			{name: "Victim", x: 1, w: 4, h: 4, tag: "v"},
			{name: "b", script: "b", node: true},
		},
	}).load(t)
	steps(t, w, 2)
	want := "exists yes 1|b sees yes"
	if got := sink.join(""); !strings.HasPrefix(got, want) {
		t.Fatalf("got %q", got)
	}
	if find(w, "Victim") != nil {
		t.Fatal("not destroyed at end of frame")
	}
	// the second frame: Victim is gone, find gives nothing, reading it is TD303
	if sink.count("TD303") < 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestAvoidingStopsFlush(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when update
  move right by 0.3 avoiding "wall"
  move down by 100 avoiding "floor"
`},
		specs: []spec{
			{name: "a", x: 0, y: 0, w: 10, h: 10, script: "a"},
			{name: "wall", x: 12, y: -50, w: 4, h: 200, tag: "wall"},
			{name: "floor", x: -50, y: 40, w: 200, h: 4, tag: "floor"},
		},
	}).load(t)
	steps(t, w, 100)
	mustNoErrors(t, sink)
	a := find(w, "a")
	if a.X != 2 { // 12 - 10, exactly; 0.3 steps accumulate and the last one is shortened
		if d := a.X - 2; d > 1e-9 || d < -1e-9 {
			t.Fatalf("x = %v, want 2", a.X)
		}
	}
	if a.Y != 30 {
		t.Fatalf("y = %v, want 30 (floor at 40, height 10)", a.Y)
	}
}

func TestAvoidingDoesNotTunnelAndAllowsLeavingAWall(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when start
  move right by 1000 avoiding "wall"
  print "x {x}"
  move left by 5 avoiding "wall"
  print "x {x}"
`},
		specs: []spec{
			{name: "a", x: 0, y: 0, w: 10, h: 10, script: "a"},
			{name: "w1", x: 100, y: 0, w: 1, h: 10, tag: "wall"},
			{name: "w2", x: 50, y: 0, w: 1, h: 10, tag: "wall"},
			{name: "w3", x: 3, y: 5, w: 20, h: 20, tag: "wall"}, // overlaps the start: does not block
		},
	}).load(t)
	steps(t, w, 1)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "x 40|x 35" {
		t.Fatalf("got %q", got)
	}
}

func TestFindAllCountDistance(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when start
  print count("t")
  print all("t").length
  print all("t")[2].name
  print find("zed").name
  print find("nobody")
  print exists(find("nobody"))
  print distance(find("zed"), find("two"))
  print distance(self, find("zed"))
  print touching("t")
`},
		specs: []spec{
			{name: "a", script: "a", x: 0, y: 0, w: 10, h: 10},
			{name: "zed", x: 30, y: 0, w: 10, h: 10, tag: "t"},
			{name: "two", x: 30, y: 40, w: 10, h: 10, tag: "t"},
		},
	}).load(t)
	steps(t, w, 1)
	mustNoErrors(t, sink)
	want := "2|2|two|zed|nothing|no|40|30|no"
	if got := sink.join(""); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestReadingAnObjectThatIsGoneIsTD303(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": "when start\n  print find(\"Ghost\").x\n"},
		specs:   []spec{{name: "a", script: "a", node: true}},
	}).load(t)
	steps(t, w, 1)
	if sink.count("TD303") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestOtherVariablesOfObjects(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"a": "when update\n  Hero.hp -= 10\n  print Hero.hp\n  Hero.nope = 1\n",
			"h": "var hp = 100\n",
		},
		specs: []spec{{name: "a", script: "a", node: true}, {name: "Hero", script: "h", node: true}, {name: "Plain", node: true}},
	}).load(t)
	steps(t, w, 1)
	if len(sink.msgs) != 2 || sink.msgs[0] != "90" {
		t.Fatalf("log %q", sink.all())
	}
	if sink.count("TD106") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

// ---- spawn ----

func TestSpawnTemplateWithChildrenScriptAndTag(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"boss": `when key space pressed
  var s = spawn "Bullet" at 40, 50
  print "spawned {s.name} {s.x} {s.y} {s.tag} {s.hp} {exists(s)} {count("bullet")}"
  s.hp = 7
`,
			"bullet": "var hp = 3\nwhen start\n  print \"start {name} {hp}\"\nwhen update\n  x += 1\n",
		},
		specs: []spec{
			{name: "boss", node: true, script: "boss"},
			{name: "Bullet", node: true, template: true, x: 500, y: 500, tag: "bullet", script: "bullet", kids: []spec{
				{name: "tip", x: 5, y: 0, w: 2, h: 2, tag: "tip"},
			}},
		},
	}).load(t)
	if find(w, "Bullet") != nil || w.Len() != 2 {
		t.Fatalf("template is in the world: %v", names(w))
	}
	_, in := w, w.Input.(*InputState)
	in.Press("space")
	steps(t, w, 1)
	mustNoErrors(t, sink)
	// the new object lives for scripts at once but is not in the world's lists until the step ends
	if got := sink.join("spawned"); got != "spawned Bullet 40 50 bullet 3 yes 0" {
		t.Fatalf("got %q\n%s", got, sink.all())
	}
	b := find(w, "Bullet")
	tip := find(w, "tip")
	if b == nil || tip == nil || b.X != 40 || b.Y != 50 || tip.X != 45 || tip.Parent != b || !b.HasTag("bullet") {
		t.Fatalf("bullet %+v tip %+v", b, tip)
	}
	steps(t, w, 2)
	mustNoErrors(t, sink)
	if got := sink.join("start"); got != "start Bullet 7" { // hp was changed right after spawn
		t.Fatalf("got %q", got)
	}
	if b.X != 42 || tip.X != 47 {
		t.Fatalf("bullet x %v tip x %v", b.X, tip.X)
	}
	if find(w, "Bullet") != b { // the template node itself never joined
		t.Fatal("two bullets")
	}
}

func TestSpawnUnknownTemplateAndBareName(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": "when start\n  spawn Star at 1, 2\n  spawn \"Nope\" at 0, 0\n"},
		specs:   []spec{{name: "a", node: true, script: "a"}, {name: "Star", w: 3, h: 3, template: true}},
	}).load(t)
	steps(t, w, 2)
	// "Nope" fails the check (unknown template) so the script does not even start
	if sink.count("Nope") == 0 {
		t.Fatalf("log: %s", sink.all())
	}
	w, _, sink = (&proj{
		scripts: map[string]string{"a": "when start\n  spawn Star at 1, 2\n  var n = \"Ne\" + \"ver\"\n  spawn n at 0, 0\n"},
		specs:   []spec{{name: "a", node: true, script: "a"}, {name: "Star", w: 3, h: 3, template: true}},
	}).load(t)
	steps(t, w, 2)
	if find(w, "Star") == nil || sink.count("TD304") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestSpawnLimit(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when update
  repeat 3000 times
    spawn "S" at 0, 0
`},
		specs: []spec{{name: "a", node: true, script: "a"}, {name: "S", w: 3, h: 3, template: true}},
	}).load(t)
	steps(t, w, 1)
	// 3000 fit, the next frame hits 5000
	if w.spawnLive != 3000 || sink.count("TD403") != 0 {
		t.Fatalf("live %d log %s", w.spawnLive, sink.all())
	}
	steps(t, w, 1)
	if w.spawnLive != 5000 || sink.count("TD403") != 1 {
		t.Fatalf("live %d log %s", w.spawnLive, sink.all())
	}
	// destroying frees room
	n := 0
	w.Each(func(e *Entity) {
		if e.Name == "S" && n < 10 {
			w.Destroy(e)
			n++
		}
	})
	steps(t, w, 1) // the limit is still hit in this step; the ten are freed at its end
	if w.spawnLive != 4990 {
		t.Fatalf("live %d", w.spawnLive)
	}
	steps(t, w, 1)
	if w.spawnLive != 5000 || w.Len() != 5002 { // ten new ones fit, then the limit is hit again
		t.Fatalf("live %d len %d", w.spawnLive, w.Len())
	}
}

func TestSpawnedObjectsTouchFromTheNextFrame(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"a": "when start\n  spawn \"Coin\" at 0, 0\n  print count(\"coin\")\n",
			"b": "when touch \"coin\"\n  print \"b touches {other.name}\"\n",
		},
		specs: []spec{
			{name: "a", node: true, script: "a"},
			{name: "b", x: 0, y: 0, w: 5, h: 5, script: "b"},
			{name: "Coin", w: 3, h: 3, tag: "coin", template: true},
		},
	}).load(t)
	steps(t, w, 1)
	if got := sink.join(""); got != "0" {
		t.Fatalf("frame 1: %q", got)
	}
	steps(t, w, 1)
	mustNoErrors(t, sink)
	if got := sink.join("b"); got != "b touches Coin" {
		t.Fatalf("frame 2: %q", sink.all())
	}
}

func TestDestroyASpawnedObjectInTheSameFrame(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": "when start\n  var s = spawn \"S\" at 0, 0\n  destroy s\n"},
		specs:   []spec{{name: "a", node: true, script: "a"}, {name: "S", w: 3, h: 3, template: true}},
	}).load(t)
	steps(t, w, 2)
	mustNoErrors(t, sink)
	if find(w, "S") != nil || w.spawnLive != 0 || w.Len() != 2 {
		t.Fatalf("spawnLive %d len %d", w.spawnLive, w.Len())
	}
}

// ---- messages ----

func TestMessagesAreDeliveredAtTheEndOfTheFrameInSendOrder(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"s": `when update
  send "one"
  send "two" with 5
  send "three" with [1, 2]
  print "sent"
`,
			"r1": `when message "one"
  print "r1 one"
when message "two"
  print "r1 two {value}"
`,
			"r2": `when message "three"
  print "r2 three {value.length}"
when message "one"
  print "r2 one {value}"
`,
		},
		specs: []spec{
			{name: "r1", node: true, script: "r1"},
			{name: "s", node: true, script: "s"},
			{name: "r2", node: true, script: "r2"},
		},
	}).load(t)
	steps(t, w, 1)
	mustNoErrors(t, sink)
	want := "sent|r1 one|r2 one nothing|r1 two 5|r2 three 2"
	if got := sink.join(""); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestMessageSentWhileDeliveringArrivesNextFrame(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when start
  send "ping"
when message "ping"
  print "ping at {n}"
  n += 1
  send "ping"
var n = 0
`},
		specs: []spec{{name: "a", node: true, script: "a"}},
	}).load(t)
	steps(t, w, 3)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "ping at 0|ping at 1|ping at 2" {
		t.Fatalf("got %q", got)
	}
}

func TestSendTo(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"s": "when start\n  send \"open\" to Door\n  send \"open\" with 9 to Door\n",
			"d": "when message \"open\"\n  print \"{name} opens {value}\"\n",
		},
		specs: []spec{
			{name: "s", node: true, script: "s"},
			{name: "Door", node: true, script: "d"},
			{name: "Other", node: true, script: "d"},
		},
	}).load(t)
	steps(t, w, 2)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "Door opens nothing|Door opens 9" {
		t.Fatalf("got %q", got)
	}
}

func TestSendLimitPerFrame(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"s": "when update\n  repeat 1500 times\n    send \"x\"\n",
			"r": "var n = 0\nwhen message \"x\"\n  n += 1\nwhen update\n  if n > 0\n    print n\n    n = 0\n",
		},
		specs: []spec{{name: "s", node: true, script: "s"}, {name: "r", node: true, script: "r"}},
	}).load(t)
	steps(t, w, 2)
	if sink.count("TD402") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
	// 1000 were sent in frame 1 and delivered at its end; frame 2's update prints them
	if got := sink.join("1000"); got != "1000" {
		t.Fatalf("log: %s", sink.all())
	}
}

// ---- mouse ----

func TestClickAndMouse(t *testing.T) {
	w, in, sink := (&proj{
		scripts: map[string]string{
			"b": "when click\n  print \"clicked {name}\"\n",
			"m": "when update\n  if mouse.held\n    print \"mouse {mouse.x} {mouse.y}\"\n",
		},
		specs: []spec{
			{name: "b1", x: 10, y: 10, w: 20, h: 20, script: "b"},
			{name: "b2", x: 100, y: 10, w: 20, h: 20, script: "b"},
			{name: "m", node: true, script: "m"},
		},
	}).load(t)
	w.CamX, w.CamY = 50, 0
	in.SetMouse(-30, 15) // view coordinates: world (20, 15) is inside b1
	in.PressMouse()
	steps(t, w, 1)
	in.ReleaseMouse()
	steps(t, w, 1)
	in.SetMouse(69, 15) // world (119, 15): inside b2
	in.PressMouse()
	steps(t, w, 3) // held for 3 steps: one click
	mustNoErrors(t, sink)
	got := sink.join("")
	want := "clicked b1|mouse 20 15|clicked b2|mouse 119 15|mouse 119 15|mouse 119 15"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestToView(t *testing.T) {
	p := Fit(1000, 600, 320, 180, "pixel") // scale 3, bars on the sides
	if p.Scale != 3 {
		t.Fatalf("%+v", p)
	}
	x, y := p.ToView(p.X+30, p.Y+90)
	if x != 10 || y != 30 {
		t.Fatalf("%v %v", x, y)
	}
	if x, _ := p.ToView(0, 0); x >= 0 {
		t.Fatal("bar should be outside")
	}
	if x, y := (Placement{}).ToView(5, 5); x != 0 || y != 0 {
		t.Fatal("no scale")
	}
}

// ---- shared and use ----

func TestSharedVariablesAcrossScripts(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			// "late" is far down the scene but declares the variable; "early" runs first and still sees it
			"early": "when update\n  coins += 1\n  print \"early {coins} {names}\"\n",
			"late":  "shared coins = 10\nshared names = [\"a\"]\nwhen update\n  add \"b\" to names\n",
		},
		specs: []spec{{name: "early", node: true, script: "early"}, {name: "late", node: true, script: "late"}},
	}).load(t)
	steps(t, w, 2)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != `early 11 ["a"]|early 12 ["a", "b"]` {
		t.Fatalf("got %q", got)
	}
	if v, ok := w.scripts.eng.Shared("coins"); !ok || v.N != 12 {
		t.Fatalf("shared coins = %v", v)
	}
}

func TestSharedDeclaredTwiceRefusesBothScripts(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"a": "shared score = 1\nwhen start\n  print \"a\"\n",
			"b": "shared score = 2\nwhen start\n  print \"b\"\n",
		},
		specs: []spec{{name: "a", node: true, script: "a"}, {name: "b", node: true, script: "b"}},
	}).load(t)
	steps(t, w, 1)
	if sink.count("TD105") != 2 || sink.count("не запущен") != 2 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestSharedKeepsValueAcrossChunkUnload(t *testing.T) {
	w, _, sink, _ := chunkScripted(t, map[ChunkKey]string{
		{0, 0}: sprite("g00", 0, 0, scr("c")),
		{4, 0}: sprite("g40", 0, 0, ""),
	}, map[string]string{"c": "shared hits = 0\nwhen update\n  hits += 1\n"})
	steps(t, w, 3)
	mustNoErrors(t, sink)
	if v, _ := w.scripts.eng.Shared("hits"); v.N != 3 {
		t.Fatalf("hits %v", v)
	}
	w.CamX = 5 * 1024
	steps(t, w, 2)
	w.CamX = 0
	steps(t, w, 3)
	if !w.ChunkLoaded(0, 0) {
		t.Fatal("chunk not back")
	}
	v, _ := w.scripts.eng.Shared("hits")
	if v.N < 6 { // the shared value is not part of any chunk: it kept its value
		t.Fatalf("hits %v", v)
	}
}

func TestUseLinksActions(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"helpers": "to heal(n)\n  hp = min(hp + n, 100)\n  return hp\n\nto boom()\n  print 1 / zero\n",
			"a":       "use \"helpers.tdsl\"\nvar hp = 50\nvar zero = 0\nwhen start\n  print heal(20)\n  print heal(70)\n  boom()\n",
		},
		specs: []spec{{name: "a", node: true, script: "a"}},
	}).load(t)
	steps(t, w, 1)
	if len(sink.msgs) < 3 || sink.msgs[0] != "70" || sink.msgs[1] != "100" {
		t.Fatalf("log: %s", sink.all())
	}
	// the error in the library is quoted from the library file
	if sink.count("scripts/helpers.tdsl, строка 6 (TD301)") != 1 || sink.count("print 1 / zero") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestUseIsSafeAndOnlyActions(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"mixed": "var secret = 1\nto ok()\n  print \"ok\"\nwhen start\n  print \"lib handler\"\n",
			"a":     "use \"mixed.tdsl\"\nuse \"../outside.tdsl\"\nuse \"/etc/passwd\"\nuse \"nothing.tdsl\"\nuse \"notscript.txt\"\nwhen start\n  ok()\n",
		},
		specs: []spec{{name: "a", node: true, script: "a"}},
	}).load(t)
	steps(t, w, 2)
	mustNoErrors(t, sink)
	if got := sink.join("ok"); got != "ok" {
		t.Fatalf("log: %s", sink.all())
	}
	if sink.count("lib handler") != 0 {
		t.Fatal("a handler of a library ran")
	}
	all := sink.all()
	for _, want := range []string{"допустимы только команды", "../outside.tdsl", "/etc/passwd", "nothing.tdsl", "notscript.txt"} {
		if !strings.Contains(all, want) {
			t.Errorf("log lacks %q:\n%s", want, all)
		}
	}
}

// ---- chunks ----

func chunkProject(t *testing.T, sceneKids string, chunks map[ChunkKey]string, scripts map[string]string) (*World, *InputState, *logSink) {
	t.Helper()
	root := t.TempDir()
	for name, src := range scripts {
		write(t, root, "scripts/"+name+".tdsl", src)
	}
	var cs []string
	for _, k := range []ChunkKey{{0, 0}, {1, 0}, {5, 0}} {
		if kids, ok := chunks[k]; ok {
			cs = append(cs, chunk(k.X, k.Y, kids))
		}
	}
	write(t, root, "game/maps/m.vide.json", doc("map", "", strings.Join(cs, ",")))
	write(t, root, "project.vide.json", doc("scene", `"map":"game/maps/m.vide.json","view":{"w":320,"h":180},`, sceneKids))
	in, sink := NewInputState(), &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: in, Log: sink.log, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	return w, in, sink
}

func TestQueriesAndTouchesOnlySeeLoadedChunks(t *testing.T) {
	w, _, sink := chunkProject(t,
		sprite("hero", 1030, 10, scr("hero"))+","+sprite("counter", 0, 0, scr("counter")),
		map[ChunkKey]string{
			{1, 0}: sprite("c1", 6, 10, `,"tag":"coin"`),
			{5, 0}: sprite("c5", 10, 10, `,"tag":"coin"`),
		},
		map[string]string{
			"hero":    "when touch \"coin\"\n  print \"in {other.name}\"\nwhen stop touching \"coin\"\n  print \"out {other.name}\"\n",
			"counter": "when key space pressed\n  print \"count {count(\"coin\")} {exists(find(\"c5\"))}\"\n",
		})
	_ = sink
	in := w.Input.(*InputState)
	say := func() {
		in.Press("space")
		steps(t, w, 1)
		in.Release("space")
	}
	say()
	steps(t, w, 1)
	if !w.ChunkLoaded(1, 0) || w.ChunkLoaded(5, 0) {
		t.Fatal("chunk setup")
	}
	// only the loaded chunk exists: one coin, c5 is nothing
	if got := sink.join(""); got != "count 1 no|in c1" {
		t.Fatalf("got %q\n%s", got, sink.all())
	}
	// the chunk of c1 unloads: the coin simply vanishes, no "stop touching"
	w.CamX = 4 * 1024
	steps(t, w, 2)
	if w.ChunkLoaded(1, 0) || !w.ChunkLoaded(5, 0) {
		t.Fatal("camera move did not swap chunks")
	}
	say()
	steps(t, w, 1)
	if got := sink.join(""); got != "count 1 no|in c1|count 1 yes" {
		t.Fatalf("got %q\n%s", got, sink.all())
	}
	// coming back: the coin loads again under the hero: a new "touch"
	w.CamX = 0
	steps(t, w, 3)
	if got := sink.join("in"); got != "in c1|in c1" {
		t.Fatalf("got %q\n%s", got, sink.all())
	}
	mustNoErrors(t, sink)
}

// ---- determinism ----

func detScenario(rate int) *proj {
	return &proj{
		rate: rate,
		scripts: map[string]string{
			"hero": `var vy = 0
when update
  move right by 60 per second avoiding "wall"
  vy += 900 per second
  move down by vy per second avoiding "floor"
  if touching("floor", down)
    vy = 0
when touch "coin"
  print "coin {other.name} {x}"
  destroy other
  send "got" with other.name
when stop touching "gate"
  print "left gate {other.name}"
`,
			"score": "shared total = 0\nwhen message \"got\"\n  total += 1\n  print \"score {total} {value}\"\n",
		},
		specs: []spec{
			{name: "hero", x: 0, y: 0, w: 8, h: 8, script: "hero"},
			{name: "score", node: true, script: "score"},
			{name: "floor", x: -100, y: 40, w: 1000, h: 4, tag: "floor"},
			{name: "g1", x: 20, y: 36, w: 8, h: 4, tag: "gate"},
			{name: "k1", x: 30, y: 30, w: 6, h: 6, tag: "coin"},
			{name: "k2", x: 90, y: 30, w: 6, h: 6, tag: "coin"},
			{name: "k3", x: 150, y: 30, w: 6, h: 6, tag: "coin"},
			{name: "wall", x: 200, y: 0, w: 4, h: 60, tag: "wall"},
		},
	}
}

func runDet(t *testing.T, rate int, seconds float64) (events string, x, y float64) {
	w, _, sink := detScenario(rate).load(t)
	steps(t, w, int(seconds*float64(rate)))
	mustNoErrors(t, sink)
	h := find(w, "hero")
	return strings.Join(sink.msgs, "|"), h.X, h.Y
}

func TestDeterminismAndRateIndependenceOfEvents(t *testing.T) {
	var first string
	for _, rate := range []int{60, 90, 120} {
		ev1, x1, y1 := runDet(t, rate, 4)
		ev2, x2, y2 := runDet(t, rate, 4)
		if ev1 != ev2 || x1 != x2 || y1 != y2 {
			t.Fatalf("%d Hz is not reproducible", rate)
		}
		if x1 != 192 || y1 != 32 { // stopped by the wall (200 - 8 wide = 192) and standing on the floor (40 - 8)
			t.Fatalf("%d Hz: hero at %v,%v", rate, x1, y1)
		}
		// the same story at every rate: coins in order, then the gate, the score in step
		strip := func(s string) string { // positions differ a little between rates, the order must not
			var out []string
			for _, l := range strings.Split(s, "|") {
				if i := strings.Index(l, "coin k"); i == 0 {
					l = l[:7]
				}
				out = append(out, l)
			}
			return strings.Join(out, "|")
		}
		got := strip(ev1)
		if first == "" {
			first = got
			want := "coin k1|score 1 k1|left gate g1|coin k2|score 2 k2|coin k3|score 3 k3"
			if got != want {
				t.Fatalf("events %q\nwant   %q", got, want)
			}
		} else if got != first {
			t.Fatalf("%d Hz: events %q differ from %q", rate, got, first)
		}
	}
}

func TestSharedFromAScriptAttachedToNothing(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"globals": "shared lives = 3\n",
			"a":       "when start\n  lives -= 1\n  print lives\n",
		},
		specs: []spec{{name: "a", node: true, script: "a"}},
	}).load(t)
	steps(t, w, 1)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "2" {
		t.Fatalf("got %q", got)
	}
}

func FuzzWorldScripts(f *testing.F) {
	for _, s := range []string{
		"when touch \"t\"\n  destroy other\n  send \"x\" with other.x\nwhen message \"x\"\n  spawn \"S\" at value, 0\n",
		"when update\n  move right by 5 avoiding \"t\"\n  if touching(\"t\", right)\n    x -= 1\n",
		"shared n = 0\nwhen stop touching \"t\"\n  n += 1\n  send \"x\"\nwhen click\n  print mouse.x\n",
		"when start\n  for each o in all(\"t\")\n    o.x += 1\n    print distance(self, o)\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 2000 {
			return
		}
		p := &proj{scripts: map[string]string{"a": src}, specs: []spec{
			{name: "a", x: 0, y: 0, w: 10, h: 10, script: "a", tag: "me"},
			{name: "b", x: 5, y: 5, w: 10, h: 10, tag: "t"},
			{name: "c", x: 50, y: 5, w: 10, h: 10, tag: "t"},
			{name: "S", x: 0, y: 0, w: 4, h: 4, template: true, script: "a"},
		}}
		w, in, _ := p.load(t)
		in.Press("space")
		in.SetMouse(3, 3)
		in.PressMouse()
		for i := 0; i < 6; i++ {
			if err := w.Step(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
