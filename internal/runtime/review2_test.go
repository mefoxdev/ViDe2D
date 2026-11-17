package runtime

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// ---- tag changes keep the index (finding 1) ----

func tagLists(w *World) map[string][]int {
	out := map[string][]int{}
	for t, l := range w.coll.tags {
		for _, e := range l {
			out[t] = append(out[t], e.ID)
		}
	}
	return out
}

func TestRetagKeepsIndexAndMatchesRebuild(t *testing.T) {
	var specs []spec
	for i := 0; i < 80; i++ {
		tag := []string{"a", "b", ""}[i%3]
		specs = append(specs, spec{name: fmt.Sprintf("o%d", i), x: (i % 9) * 30, y: (i / 9) * 30, w: 20, h: 20, tag: tag})
	}
	w, _, _ := (&proj{specs: specs}).load(t)
	w.collEnsure()
	w.namesEnsure()
	rng := rand.New(rand.NewSource(5))
	var all []*Entity
	w.Each(func(e *Entity) { all = append(all, e) })
	for round := 0; round < 300; round++ {
		e := all[rng.Intn(len(all))]
		tag := []string{"a", "b", "c", ""}[rng.Intn(4)]
		var tags []string
		if tag != "" {
			tags = []string{tag}
		}
		w.retag(e, tags)
		if !w.coll.valid || !w.coll.namesOK {
			t.Fatalf("round %d: a tag change dropped the index", round)
		}
		if round%7 == 0 {
			w.Place(all[rng.Intn(len(all))], float64(rng.Intn(300)), float64(rng.Intn(300)), false)
		}
		if round%5 == 0 {
			w.Tick++ // the sweep runs too
		}
		// the queries must agree with a scan of every object
		for _, tg := range []string{"a", "b", "c"} {
			probe := all[rng.Intn(len(all))]
			got, _ := w.overlapping(probe, vmTarget(tg))
			var want []int
			sb := w.boxOf(probe)
			for _, o := range all {
				if o.HasTag(tg) && !related(probe, o) && sb.overlaps(w.boxOf(o)) {
					want = append(want, o.ID)
				}
			}
			var ids []int
			for _, o := range got {
				ids = append(ids, o.ID)
			}
			if !slices.Equal(ids, want) {
				t.Fatalf("round %d tag %s: index %v, scan %v", round, tg, ids, want)
			}
		}
	}
	// the lists equal what a fresh rebuild gives
	before := tagLists(w)
	w.collChanged()
	w.collEnsure()
	after := tagLists(w)
	for tg, l := range after {
		if !slices.Equal(before[tg], l) {
			t.Fatalf("tag %s: kept %v, rebuilt %v", tg, before[tg], l)
		}
	}
	if len(before) != len(after) {
		t.Fatalf("tags differ: %v vs %v", before, after)
	}
}

func TestTagLoopIsCheap(t *testing.T) {
	var specs []spec
	specs = append(specs, spec{name: "me", x: 0, y: 0, w: 5, h: 5, script: "m", tag: "me"})
	for i := 0; i < 2000; i++ {
		specs = append(specs, spec{name: fmt.Sprintf("b%d", i), x: 100 + i, y: 100, w: 5, h: 5, tag: "b"})
	}
	w, _, sink := (&proj{
		scripts: map[string]string{"m": "when update\n  repeat 200 times\n    tag = \"a\"\n    if touching(\"b\")\n      print \"x\"\n    tag = \"me\"\n"},
		specs:   specs,
	}).load(t)
	steps(t, w, 1)
	mustNoErrors(t, sink)
	if sink.count("TD401") != 0 {
		t.Fatalf("log: %s", sink.all())
	}
	if !w.coll.valid {
		t.Fatal("the index was dropped by the tag changes")
	}
	// the whole loop must stay far below the frame budget: no rebuild per turn
	if used := w.scripts.eng.Limits.FrameSteps - w.scripts.eng.StepsLeft(); used > 20000 {
		t.Fatalf("the loop used %d steps", used)
	}
}

func TestIndexRebuildIsCharged(t *testing.T) {
	var specs []spec
	for i := 0; i < 3200; i++ {
		specs = append(specs, spec{name: fmt.Sprintf("b%d", i), x: i, y: 0, w: 5, h: 5, tag: "b"})
	}
	w, _, _ := (&proj{specs: specs}).load(t)
	w.collChanged()
	w.collEnsure()
	if got := w.takeWork(); got < 3200/16 {
		t.Fatalf("a rebuild of 3200 objects charged only %d", got)
	}
}

// ---- group boxes: per-object dirty flags (finding 2) ----

func TestGroupBoxCacheIsPerGroup(t *testing.T) {
	w, _, _ := (&proj{specs: []spec{
		{name: "g1", node: true, tag: "g", kids: []spec{{name: "a", x: 0, y: 0, w: 10, h: 10}, {name: "b", x: 20, y: 0, w: 10, h: 10}}},
		{name: "g2", node: true, tag: "g", kids: []spec{{name: "c", x: 100, y: 0, w: 10, h: 10}}},
	}}).load(t)
	var g1, g2, a *Entity
	w.Each(func(e *Entity) {
		switch e.Name {
		case "g1":
			g1 = e
		case "g2":
			g2 = e
		case "a":
			a = e
		}
	})
	w.boxOf(g1)
	w.boxOf(g2)
	w.takeWork()
	w.Place(a, 5, 0, false) // moves a member of g1 only
	if g2.bbOK == false {
		t.Fatal("moving a member of g1 dropped the cache of g2")
	}
	if g1.bbOK {
		t.Fatal("the cache of g1 survived the move of its member")
	}
	if b := w.boxOf(g1); b.x0 != 5 || b.x1 != 30 {
		t.Fatalf("box of g1 = %+v", b)
	}
	if w.bbWork == 0 {
		t.Fatal("recomputing a group box was not counted")
	}
	// untouched groups cost nothing on the next look
	w.takeWork()
	w.boxOf(g1)
	w.boxOf(g2)
	if n := w.takeWork(); n != 0 {
		t.Fatalf("cached boxes cost %d", n)
	}
}

func TestGroupBoxMatchesFreshComputation(t *testing.T) {
	var kids []spec
	for i := 0; i < 12; i++ {
		kids = append(kids, spec{name: fmt.Sprintf("k%d", i), x: i * 7, y: i * 3, w: 4, h: 4})
	}
	w, _, _ := (&proj{specs: []spec{
		{name: "outer", node: true, kids: []spec{{name: "inner", node: true, kids: kids}, {name: "z", x: 500, y: 500, w: 3, h: 3}}},
	}}).load(t)
	var all []*Entity
	w.Each(func(e *Entity) { all = append(all, e) })
	rng := rand.New(rand.NewSource(9))
	fresh := func(e *Entity) box {
		cp := *e
		cp.bbOK = false
		return w.boxOf(&cp)
	}
	for i := 0; i < 400; i++ {
		e := all[rng.Intn(len(all))]
		switch rng.Intn(3) {
		case 0:
			w.Place(e, float64(rng.Intn(600)), float64(rng.Intn(600)), false)
		case 1:
			e.W, e.H = float64(rng.Intn(10)), float64(rng.Intn(10))
			w.geoChanged(e, false)
		default:
			w.Tick++
		}
		for _, g := range all[:2] {
			if got, want := w.boxOf(g), fresh(g); got != want {
				t.Fatalf("step %d %s: cached %+v, fresh %+v", i, g.Name, got, want)
			}
		}
	}
}

// ---- pending objects (finding 6) ----

func TestPendingObjectIsInvisibleToQueries(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"gun": `var b = nothing
when start
  b = spawn "Bullet" at 3, 3
  print "exists {exists(b)}"
  if touching(b)
    print "touch-sees"
  if touching("bullet")
    print "tag-sees"
  b.hp = 9
  print "hp {b.hp} {b.x}"
when update
  if touching(b)
    print "touch2"
  print "dist {distance(self, b)}"
`,
			"bullet": "var hp = 1\n",
		},
		specs: []spec{
			{name: "gun", x: 0, y: 0, w: 10, h: 10, script: "gun"},
			{name: "Bullet", x: 0, y: 0, w: 10, h: 10, script: "bullet", tag: "bullet", template: true},
		},
	}).load(t)
	steps(t, w, 2)
	got := sink.all()
	for _, bad := range []string{"touch-sees", "tag-sees"} {
		if strings.Contains(got, bad) {
			t.Fatalf("a pending object was seen (%s):\n%s", bad, got)
		}
	}
	for _, want := range []string{"exists yes", "hp 9 3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	// the distance is refused in frame 1 only if update ran with the copy pending;
	// frame 2 sees it (and touches it)
	if !strings.Contains(got, "touch2") {
		t.Fatalf("the copy did not enter the game in frame 2:\n%s", got)
	}
}

func TestDistanceToPendingObjectIsRefused(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"gun": "when start\n  var b = spawn \"Bullet\" at 3, 3\n  print distance(self, b)\n"},
		specs: []spec{
			{name: "gun", x: 0, y: 0, w: 10, h: 10, script: "gun"},
			{name: "Bullet", w: 10, h: 10, tag: "bullet", template: true},
		},
	}).load(t)
	steps(t, w, 1)
	if sink.count("TD303") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestCountLimitedSpawnDoesNotOvershoot(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"gun": "when update\n  if count(\"bullet\") < 5\n    spawn \"Bullet\" at 0, 0\n"},
		specs: []spec{
			{name: "gun", node: true, script: "gun"},
			{name: "Bullet", w: 4, h: 4, tag: "bullet", template: true},
		},
	}).load(t)
	for i := 1; i <= 12; i++ {
		steps(t, w, 1)
		w.collEnsure()
		if n := len(w.coll.tags["bullet"]); n != min(i, 5) {
			t.Fatalf("frame %d: %d bullets in the world", i, n)
		}
		if w.spawnLive != min(i, 5) {
			t.Fatalf("frame %d: %d spawned", i, w.spawnLive)
		}
	}
	mustNoErrors(t, sink)
}

// ---- mouse at launch (finding 9) ----

func TestButtonHeldAtLaunchIsNoClick(t *testing.T) {
	in := NewInputState()
	in.SetMouseHeld(true) // first poll: the Play click is still down
	if in.MousePressed() {
		t.Fatal("a button that was already down at launch counts as pressed")
	}
	if _, _, held := in.Mouse(); !held {
		t.Fatal("the held state is lost")
	}
	in.SetMouseHeld(true)
	if in.MousePressed() {
		t.Fatal("still held: no edge")
	}
	in.SetMouseHeld(false)
	if !in.MouseReleased() {
		t.Fatal("release not seen")
	}
	in.Advance()
	in.SetMouseHeld(true)
	if !in.MousePressed() {
		t.Fatal("a later press must be an edge")
	}
	fresh := NewInputState()
	fresh.SetMouseHeld(false)
	fresh.SetMouseHeld(true)
	if !fresh.MousePressed() {
		t.Fatal("press after a first idle poll must be an edge")
	}
}

// ---- smaller findings ----

func TestSelfSpawningTemplateStopsCleanly(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"r": "var x2 = spawn \"R\" at 0, 0\n", "g": "when start\n  spawn \"R\" at 0, 0\n"},
		specs: []spec{
			{name: "g", node: true, script: "g"},
			{name: "R", w: 4, h: 4, script: "r", template: true},
		},
	}).load(t)
	steps(t, w, 1)
	if sink.count("TD404") == 0 {
		t.Fatalf("log: %s", sink.all())
	}
	if w.spawnLive > maxSpawnNesting+2 {
		t.Fatalf("%d objects were made before the cut", w.spawnLive)
	}
}

func TestSetVarOfNotYetStartedObject(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"a": "when start\n  Late.hp = 5\n  print \"read {Late.hp}\"\n",
			"l": "var hp = 1\nwhen start\n  print \"late start {hp}\"\n",
		},
		specs: []spec{
			{name: "A", node: true, script: "a"},
			{name: "Late", node: true, script: "l"},
		},
	}).load(t)
	steps(t, w, 1)
	if sink.count("TD106") != 0 {
		t.Fatalf("log: %s", sink.all())
	}
	if got := sink.join("read"); got != "read 5" {
		t.Fatalf("log: %s", sink.all())
	}
	if got := sink.join("late"); got != "late start 5" {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestBrokenSharedFileIsReported(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{
			"globals": "shared lives = 3\nwhen start\n  print nope_name_xyz\n",
			"a":       "when start\n  print lives\n",
		},
		specs: []spec{{name: "a", node: true, script: "a"}},
	}).load(t)
	steps(t, w, 1)
	if sink.count("Общей переменной «lives» нет") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestManyScriptsWarn(t *testing.T) {
	scripts := map[string]string{"a": "when start\n  print 1\n"}
	for i := 0; i < maxScanScripts+3; i++ {
		scripts[fmt.Sprintf("s%03d", i)] = "when start\n  print 1\n"
	}
	w, _, sink := (&proj{scripts: scripts, specs: []spec{{name: "a", node: true, script: "a"}}}).load(t)
	steps(t, w, 1)
	if sink.count("shared") == 0 || sink.count("больше") == 0 {
		t.Fatalf("no warning about the truncated scan: %s", sink.all())
	}
}

func TestNegativeSizeIsNoCollisionBox(t *testing.T) {
	w, _, _ := (&proj{specs: []spec{
		{name: "neg", x: 10, y: 10, w: 10, h: 10, tag: "n"},
		{name: "other", x: 2, y: 2, w: 3, h: 3, tag: "o"},
		{name: "wide", x: 10, y: 10, w: 10, h: 4, tag: "w"},
	}}).load(t)
	var neg, other, wide *Entity
	w.Each(func(e *Entity) {
		switch e.Name {
		case "neg":
			neg = e
		case "other":
			other = e
		case "wide":
			wide = e
		}
	})
	// scripts can set a negative size; the scene file cannot
	neg.W, neg.H, wide.H = -10, -10, -4
	w.geoChanged(neg, false)
	w.geoChanged(wide, false)
	if _, ok := ownBox(neg); ok {
		t.Fatal("negative size is a box")
	}
	if _, ok := ownBox(wide); ok {
		t.Fatal("negative height is a box")
	}
	if b := w.boxOf(neg); b != (box{10, 10, 10, 10}) {
		t.Fatalf("box %+v, want the point", b)
	}
	if hit, _ := w.touchesAny(neg, vmTarget("o"), 0, false); hit {
		t.Fatal("a negative-size object touches")
	}
	_ = other
}

func TestClickEdgesAreHalfOpen(t *testing.T) {
	w, in, sink := (&proj{
		scripts: map[string]string{"b": "when click\n  print \"hit\"\n"},
		specs:   []spec{{name: "b", x: 10, y: 10, w: 20, h: 20, script: "b"}},
	}).load(t)
	for _, c := range []struct {
		x, y float64
		hit  bool
	}{{10, 10, true}, {29.5, 29.5, true}, {30, 15, false}, {15, 30, false}, {9.5, 15, false}, {10, 15, true}} {
		sink.msgs = nil
		in.SetMouse(c.x, c.y)
		in.PressMouse()
		steps(t, w, 1)
		in.ReleaseMouse()
		steps(t, w, 1)
		if got := sink.count("hit") == 1; got != c.hit {
			t.Fatalf("click at %v,%v: hit %v, want %v", c.x, c.y, got, c.hit)
		}
	}
}
