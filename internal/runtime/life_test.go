package runtime

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// lifeWorld builds a world whose map has the given chunk contents; the view is
// at the origin, so chunks (0,0) and (1,0) start loaded and (5,0) does not.
func lifeWorld(t *testing.T, rate int, chunks map[ChunkKey]string, scripts map[string]string, sceneKids string) (*World, *logSink) {
	t.Helper()
	root := t.TempDir()
	for name, src := range scripts {
		write(t, root, "scripts/"+name+".tdsl", src)
	}
	var cs []string
	if _, ok := chunks[ChunkKey{5, 0}]; !ok {
		chunks[ChunkKey{5, 0}] = sprite("far", 0, 0, "") // somewhere to go
	}
	for _, k := range []ChunkKey{{0, 0}, {1, 0}, {5, 0}} {
		if kids, ok := chunks[k]; ok {
			cs = append(cs, chunk(k.X, k.Y, kids))
		}
	}
	write(t, root, "game/maps/m.vide.json", doc("map", "", strings.Join(cs, ",")))
	write(t, root, "project.vide.json", doc("scene", fmt.Sprintf(`"map":"game/maps/m.vide.json","view":{"w":320,"h":180},"tick_rate":%d,`, rate), sceneKids))
	sink := &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: NewInputState(), Log: sink.log, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	return w, sink
}

func alive(name string, x, y int, extra string) string {
	return sprite(name, x, y, `,"alive":true`+extra)
}

// away moves the camera to chunk 4 (chunks 3..5 load, chunk 1 unloads) and
// returns the step at which chunk (1,0) was unloaded.
func away(t *testing.T, w *World) uint64 {
	t.Helper()
	w.CamX = 4 * 1024
	for i := 0; i < 5 && w.ChunkLoaded(1, 0); i++ {
		steps(t, w, 1)
	}
	if w.ChunkLoaded(1, 0) || !w.ChunkLoaded(5, 0) {
		t.Fatal("the camera move did not swap chunks")
	}
	return w.Tick
}

func back(t *testing.T, w *World) {
	t.Helper()
	w.CamX = 0
	steps(t, w, 1)
	if !w.ChunkLoaded(1, 0) {
		t.Fatal("chunk 1,0 did not load")
	}
}

// varOf reads a script variable of a live entity (its instance is created if
// the entity has not run yet).
func varOf(t *testing.T, e *Entity, name string) vm.Value {
	t.Helper()
	b := e.Behavior.(*scriptBehavior)
	b.ensure(e)
	if b.inst == nil {
		t.Fatalf("%s has no script instance", e.Name)
	}
	v, ok := b.inst.Global(name)
	if !ok {
		t.Fatalf("%s has no variable %s", e.Name, name)
	}
	return v
}

func sleeperVar(t *testing.T, w *World, k ChunkKey, name, v string) vm.Value {
	t.Helper()
	e := w.chunks[k].sleepByID[0]
	for _, s := range w.chunks[k].sleep {
		if s.Name == name {
			e = s
		}
	}
	if e == nil {
		t.Fatalf("no sleeper %s", name)
	}
	return varOf(t, e, v)
}

func TestLifeEquivalentMovementAtAllRates(t *testing.T) {
	for _, rate := range []int{60, 90, 120} {
		w, sink := lifeWorld(t, rate, map[ChunkKey]string{
			{1, 0}: alive("walker", 10, 10, scr("walk")) + "," + sprite("frozen", 10, 40, scr("walk")),
		}, map[string]string{"walk": "when update\n  move right by 60 per second\n"}, "")
		steps(t, w, 3)
		u := away(t, w) // the chunk was loaded for u steps
		if w.LifeObjects() != 1 {
			t.Fatalf("rate %d: %d sleepers", rate, w.LifeObjects())
		}
		x0 := 1024.0 + 10
		for w.Tick < u+uint64(5*rate)+uint64(rate/2) { // five life ticks, half a period left over (dropped)
			steps(t, w, 1)
		}
		back(t, w)                                // one more loaded step
		loaded := 60 * float64(u) / float64(rate) // the reload happens at the end of a step: no loaded step after it
		walker, frozen := find(w, "walker"), find(w, "frozen")
		if got, want := walker.X-x0, loaded+60*5; math.Abs(got-want) > 1e-6 {
			t.Fatalf("rate %d: walker moved %.9f, loaded simulation would give %.9f", rate, got, want)
		}
		if got := frozen.X - x0; math.Abs(got-loaded) > 1e-6 {
			t.Fatalf("rate %d: a plain object must freeze while unloaded, moved %v", rate, got)
		}
		if walker.X-walker.PrevX > 60.0/float64(rate)+1e-9 {
			t.Fatalf("rate %d: the glide covers the whole offline distance: %v -> %v", rate, walker.PrevX, walker.X)
		}
		mustNoErrors(t, sink)
	}
}

func TestLifeStateAfterReload(t *testing.T) {
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{
		{1, 0}: alive("cow", 10, 10, scr("cow")),
	}, map[string]string{"cow": "var n = 0\nvar log = []\nwhen every 1 seconds\n  n += 1\n  add n to log\nwhen update\n  y += 1\n"}, "")
	steps(t, w, 2)
	u := away(t, w)
	for w.Tick < u+3*60+5 {
		steps(t, w, 1)
	}
	back(t, w)
	cow := find(w, "cow")
	if got := varOf(t, cow, "n"); got.N != 3 {
		t.Fatalf("n after 3 s offline = %v, want 3", got.N)
	}
	// "update" ran once per life tick: a plain "y += 1" (not a rate) adds 1 each, 3 in all, besides the u loaded steps
	if cow.Y != 10+float64(u)+3 {
		t.Fatalf("y = %v", cow.Y)
	}
	// the partial period before the reload is dropped, nothing doubled afterwards
	if w.LifeObjects() != 0 || len(w.chunks[ChunkKey{1, 0}].sleep) != 0 {
		t.Fatal("sleepers must be gone after the chunk loads")
	}
	mustNoErrors(t, sink)
}

func TestLifeDeterministicAndRateIndependent(t *testing.T) {
	type result struct {
		n, ax, ay, bx, tt float64
		counts            string
	}
	run := func(rate int) result {
		w, sink := lifeWorld(t, rate, map[ChunkKey]string{
			{1, 0}: alive("a", 10, 10, scr("a")) + "," + alive("b", 60, 10, scr("a")),
		}, map[string]string{"a": "var n = 0\nvar t = 0\nwhen every 0.5 seconds\n  n += 1\nwhen update\n  t += 1 per second\n  move right by 10 per second\n  move down by 5 per second\n"}, "")
		steps(t, w, 1)
		u := away(t, w)
		for w.Tick < u+uint64(7*rate)+3 {
			steps(t, w, 1)
		}
		back(t, w)
		mustNoErrors(t, sink)
		a, b := find(w, "a"), find(w, "b")
		loaded := float64(u) / float64(rate) // seconds the chunk was loaded
		return result{
			counts: fmt.Sprintf("%v %v", varOf(t, a, "n").N, varOf(t, b, "n").N),
			tt:     varOf(t, a, "t").N - loaded, ax: a.X - 1034 - 10*loaded, ay: a.Y - 10 - 5*loaded, bx: b.X - 1084 - 10*loaded,
		}
	}
	ref := run(60)
	if again := run(60); again != ref {
		t.Fatalf("not repeatable: %+v vs %+v", ref, again)
	}
	if ref.counts != "14 14" { // "every 0.5 s": two per life second, 7 life seconds
		t.Fatalf("timer counts %s", ref.counts)
	}
	for _, rate := range []int{90, 120} {
		got := run(rate)
		if got.counts != ref.counts || math.Abs(got.tt-ref.tt) > 1e-6 || math.Abs(got.ax-ref.ax) > 1e-6 || math.Abs(got.ay-ref.ay) > 1e-6 || math.Abs(got.bx-ref.bx) > 1e-6 {
			t.Fatalf("rate %d: %+v differs from 60 Hz %+v", rate, got, ref)
		}
	}
	if math.Abs(ref.tt-7) > 1e-6 || math.Abs(ref.ax-70) > 1e-6 || math.Abs(ref.ay-35) > 1e-6 {
		t.Fatalf("seven offline seconds expected: %+v", ref)
	}
}

func TestLifeBudgetAndCursorFairness(t *testing.T) {
	var kids []string
	const N = 450
	for i := 0; i < N; i++ {
		kids = append(kids, alive(fmt.Sprintf("o%03d", i), 2+(i%60)*16, 2+(i/60)*16, scr("count")))
	}
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{{1, 0}: strings.Join(kids, ",")},
		map[string]string{"count": "var n = 0\nwhen update\n  n += 1\n"}, "")
	steps(t, w, 2)
	u := away(t, w)
	u1 := w.chunks[ChunkKey{1, 0}]
	counts := func() (sum, lo, hi int) {
		lo = 1 << 30
		for _, e := range u1.life {
			v := 0
			if b := e.Behavior.(*scriptBehavior); b.inst != nil {
				if g, ok := b.inst.Global("n"); ok {
					v = int(g.N)
				}
			}
			sum += v
			lo, hi = min(lo, v), max(hi, v)
		}
		return
	}
	if len(u1.life) != N {
		t.Fatalf("%d alive sleepers, want %d", len(u1.life), N)
	}
	base, _, _ := counts()
	prev := base
	maxStep := 0
	for w.Tick < u+60+3*2+2 { // two full rounds of the 450 objects
		steps(t, w, 1)
		sum, lo, hi := counts()
		maxStep = max(maxStep, sum-prev)
		prev = sum
		if hi-lo > 1 {
			t.Fatalf("step %d: an object was served twice before another once (n from %d to %d)", w.Tick, lo, hi)
		}
	}
	if maxStep != LifeMaxPerStep {
		t.Fatalf("at most %d ticks per step expected (and reached), got max %d", LifeMaxPerStep, maxStep)
	}
	if sum, lo, _ := counts(); sum-base != N || lo-base/N != 1 {
		t.Fatalf("after one period everybody was served exactly once: sum %d min %d", sum-base, lo-base/N)
	}
	// next period: served again, still in a fair round
	for w.Tick < u+120+6 {
		steps(t, w, 1)
		if _, lo, hi := counts(); hi-lo > 1 {
			t.Fatalf("unfair at %d: %d..%d", w.Tick, lo, hi)
		}
	}
	if sum, lo, _ := counts(); sum-base != 2*N || lo-base/N != 2 {
		t.Fatalf("second round: sum %d min %d", sum-base, lo-base/N)
	}
	mustNoErrors(t, sink)
}

func TestLifeStepBudgetIsShared(t *testing.T) {
	var kids []string
	for i := 0; i < 100; i++ {
		kids = append(kids, alive(fmt.Sprintf("h%03d", i), 2+i*8, 2, scr("heavy")))
	}
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{{1, 0}: strings.Join(kids, ",")},
		map[string]string{"heavy": "var n = 0\nwhen every 1 seconds\n  repeat 500 times\n    n += 1\n"}, "")
	steps(t, w, 1)
	u := away(t, w)
	eng := w.scripts.eng
	limit := eng.Limits.FrameSteps/lifeBudgetDiv + eng.Limits.HandlerSteps
	lazy := false
	for w.Tick < u+60+50 {
		steps(t, w, 1)
		if used := eng.Limits.FrameSteps - eng.StepsLeft(); used > limit {
			t.Fatalf("step %d used %d steps of the frame, more than a quarter plus one handler", w.Tick, used)
		}
		ticked := 0
		for _, e := range w.chunks[ChunkKey{1, 0}].life {
			if e.lifeLast == w.Tick {
				ticked++
			}
		}
		if ticked > 0 && ticked < LifeMaxPerStep {
			lazy = true
		}
	}
	if !lazy {
		t.Fatal("the step budget never limited the ticks: test is not testing anything")
	}
	for _, e := range w.chunks[ChunkKey{1, 0}].life {
		if int(varOf(t, e, "n").N) != 500 {
			t.Fatalf("%s not served within 50 steps: %v", e.Name, varOf(t, e, "n").N)
		}
	}
	_ = sink
}

func TestLifeDestroyIsPermanent(t *testing.T) {
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{
		{1, 0}: alive("fly", 10, 10, scr("fly")) + "," + alive("keep", 40, 10, scr("keep")),
	}, map[string]string{"fly": "when every 1 seconds\n  destroy\n", "keep": "var n = 0\nwhen every 1 seconds\n  n += 1\n"}, "")
	steps(t, w, 1)
	u := away(t, w)
	for w.Tick < u+62 {
		steps(t, w, 1)
	}
	if w.LifeObjects() != 1 {
		t.Fatalf("%d sleepers after one died", w.LifeObjects())
	}
	back(t, w)
	if find(w, "fly") != nil || find(w, "keep") == nil {
		t.Fatalf("destroyed object came back or survivor lost: %v", names(w))
	}
	mustNoErrors(t, sink)
}

func TestLifeWaitResumesByElapsedSteps(t *testing.T) {
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{
		{1, 0}: alive("w", 10, 10, scr("w")),
	}, map[string]string{"w": "var n = 0\nwhen every 1 seconds\n  wait 2 seconds\n  n += 1\n"}, "")
	steps(t, w, 1)
	u := away(t, w)
	for w.Tick < u+2*60+2 {
		steps(t, w, 1)
	}
	if sleeperVar(t, w, ChunkKey{1, 0}, "w", "n").N != 0 {
		t.Fatal("woke too early")
	}
	for w.Tick < u+3*60+2 {
		steps(t, w, 1)
	}
	if sleeperVar(t, w, ChunkKey{1, 0}, "w", "n").N != 1 {
		t.Fatal("did not resume after its wait")
	}
	mustNoErrors(t, sink)
}

func TestLifeWhatOfflineObjectsMayNotDo(t *testing.T) {
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{
		{1, 0}: alive("npc", 10, 10, scr("npc")),
	}, map[string]string{
		"npc":  "var seen = 0\nwhen every 1 seconds\n  seen = count(\"hero\")\n  Hero.score = 5\nwhen every 2 seconds\n  spawn \"Star\" at 1, 2\nwhen every 3 seconds\n  go to scene \"main\"\nwhen every 4 seconds\n  play sound \"nothing.wav\"\n  send \"ping\"\n  x += 0\n",
		"hero": "var score = 0\nvar pings = 0\nwhen message \"ping\"\n  pings += 1\n",
	}, sprite("Hero", 5, 5, `,"tag":"hero"`+scr("hero"))+","+sprite("Star", 0, 0, `,"template":true`))
	steps(t, w, 1)
	u := away(t, w)
	for w.Tick < u+4*60+2 {
		steps(t, w, 1)
	}
	hero := find(w, "Hero")
	if varOf(t, hero, "score").N != 0 {
		t.Fatal("an offline object wrote into a loaded object")
	}
	if got := sleeperVar(t, w, ChunkKey{1, 0}, "npc", "seen").N; got != 1 {
		t.Fatalf("offline objects should see loaded ones through queries, count=%v", got)
	}
	if varOf(t, hero, "pings").N != 1 {
		t.Fatal("send to loaded objects must work offline")
	}
	log := sink.all()
	for _, want := range []string{"TD406"} {
		if !strings.Contains(log, want) {
			t.Fatalf("expected %s in the log:\n%s", want, log)
		}
	}
	if sink.count("TD406") < 3 { // write, spawn, scene
		t.Fatalf("expected three TD406 errors:\n%s", log)
	}
	if strings.Contains(log, "nothing.wav") {
		t.Fatalf("sounds must be silent offline (not even a missing file is reported):\n%s", log)
	}
}

func TestLifeCapOnObjects(t *testing.T) {
	old := lifeMaxObjects
	lifeMaxObjects = 3
	defer func() { lifeMaxObjects = old }()
	var kids []string
	for i := 0; i < 5; i++ {
		kids = append(kids, alive(fmt.Sprintf("o%d", i), 10+i*20, 10, scr("c")))
	}
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{{1, 0}: strings.Join(kids, ",")},
		map[string]string{"c": "var n = 0\nwhen update\n  n += 1\n"}, "")
	steps(t, w, 1)
	u := away(t, w)
	if w.LifeObjects() != 3 {
		t.Fatalf("cap not applied: %d", w.LifeObjects())
	}
	for w.Tick < u+62 {
		steps(t, w, 1)
	}
	back(t, w)
	vals := map[float64]int{}
	for i := 0; i < 5; i++ {
		vals[varOf(t, find(w, fmt.Sprintf("o%d", i)), "n").N]++
	}
	if len(vals) != 2 || sink.count("20 000") != 1 {
		t.Fatalf("expected 3 objects that lived and 2 that froze: %v; log %q", vals, sink.all())
	}
	for n, c := range vals {
		if (c == 3) != (n > 0 && vals[n-1] == 2) {
			t.Fatalf("the three that lived must be one tick ahead of the two that froze: %v", vals)
		}
	}
}

func TestLifeGroupMovesWithItsMembers(t *testing.T) {
	grp := fmt.Sprintf(`{"name":"herd","kind":"Node","x":10,"y":10,"alive":true%s,"children":[%s,%s]}`, scr("walk"),
		sprite("calf1", 0, 0, ""), sprite("calf2", 20, 0, scr("static")))
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{{1, 0}: grp},
		map[string]string{"walk": "when update\n  move right by 30 per second\n", "static": "var k = 7\nwhen every 1 seconds\n  k += 1\n"}, "")
	steps(t, w, 1)
	u := away(t, w)
	for w.Tick < u+2*60+1 {
		steps(t, w, 1)
	}
	back(t, w)
	c1, c2, herd := find(w, "calf1"), find(w, "calf2"), find(w, "herd")
	if math.Abs(herd.X-(1024+10+60+0.5)) > 0.6 || math.Abs(c1.X-herd.X) > 1e-9 || math.Abs(c2.X-herd.X-20) > 1e-9 {
		t.Fatalf("group moved apart: herd %v calf1 %v calf2 %v", herd.X, c1.X, c2.X)
	}
	// a member with its own script that is not alive does not live
	if varOf(t, c2, "k").N != 7 {
		t.Fatalf("a non-alive member must freeze, k=%v", varOf(t, c2, "k").N)
	}
	mustNoErrors(t, sink)
}

func TestPlaceChargesTheStepBudgetPerDescendant(t *testing.T) {
	var kids []spec
	for i := 0; i < 160; i++ {
		kids = append(kids, spec{name: fmt.Sprintf("k%d", i), x: i % 20, y: i / 20, w: 2, h: 2})
	}
	p := &proj{
		scripts: map[string]string{"g": "when update\n  move right by 1\n"},
		specs:   []spec{{name: "G", node: true, script: "g", kids: kids}, {name: "Lone", w: 2, h: 2, script: "l"}},
	}
	p.scripts["l"] = "when update\n  move right by 1\n"
	w, _, sink := p.load(t)
	steps(t, w, 1) // start-up
	eng := w.scripts.eng
	used := func() int { steps(t, w, 1); return eng.Limits.FrameSteps - eng.StepsLeft() }
	both := used()
	// the group moves 160 descendants: 10 steps more than the lone object's whole frame cost
	p2 := &proj{scripts: map[string]string{"l": "when update\n  move right by 1\n"}, specs: []spec{{name: "Lone", w: 2, h: 2, script: "l"}}}
	w2, _, _ := p2.load(t)
	steps(t, w2, 1)
	eng2 := w2.scripts.eng
	steps(t, w2, 1)
	lone := eng2.Limits.FrameSteps - eng2.StepsLeft()
	groupCost := both - lone
	if groupCost < 10 {
		t.Fatalf("moving 160 descendants cost only %d steps beyond a lone mover, want at least 10", groupCost)
	}
	mustNoErrors(t, sink)
	// the remainder carries over: 15 descendants a time cost nothing at once but 1 step per 16 in total
	w3 := &World{}
	e := &Entity{}
	for i := 0; i < 15; i++ {
		e.Children = append(e.Children, &Entity{Parent: e})
	}
	for i := 0; i < 16; i++ {
		w3.Place(e, float64(i+1), 0, true)
	}
	if got := w3.takeWork(); got != 15 { // 16*15 = 240 descendants = 15 steps
		t.Fatalf("carry: charged %d, want 15", got)
	}
}

func TestLifeWaitSurvivesTheReload(t *testing.T) {
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{
		{1, 0}: alive("w", 10, 10, scr("w")),
	}, map[string]string{"w": "var n = 0\nwhen every 1 seconds\n  wait 2 seconds\n  n += 1\n"}, "")
	steps(t, w, 1)
	u := away(t, w)
	for w.Tick < u+62 { // the wait began at u+60 and ends at u+180
		steps(t, w, 1)
	}
	back(t, w)
	stop := u + 180
	for w.Tick < stop-1 {
		steps(t, w, 1)
	}
	if varOf(t, find(w, "w"), "n").N != 0 {
		t.Fatal("the wait ended early after the reload")
	}
	steps(t, w, 2)
	if varOf(t, find(w, "w"), "n").N != 1 {
		t.Fatal("the wait did not end on time after the reload")
	}
	mustNoErrors(t, sink)
}

func TestLifeCursorWrapsAcrossChunksAndSurvivesAWake(t *testing.T) {
	mk := func(prefix string) string {
		var kids []string
		for i := 0; i < 250; i++ {
			kids = append(kids, alive(fmt.Sprintf("%s%03d", prefix, i), 2+(i%60)*16, 2+(i/60)*16, scr("count")))
		}
		return strings.Join(kids, ",")
	}
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{{0, 0}: mk("a"), {1, 0}: mk("b")},
		map[string]string{"count": "var n = 0\nwhen update\n  n += 1\n"}, "")
	steps(t, w, 1)
	u := away(t, w)
	c0, c1 := w.chunks[ChunkKey{0, 0}], w.chunks[ChunkKey{1, 0}]
	if len(c0.life) != 250 || len(c1.life) != 250 {
		t.Fatalf("sleepers %d + %d", len(c0.life), len(c1.life))
	}
	nOf := func(e *Entity) int {
		if b := e.Behavior.(*scriptBehavior); b.inst != nil {
			if g, ok := b.inst.Global("n"); ok {
				return int(g.N)
			}
		}
		return 0
	}
	spread := func() (lo, hi int) {
		lo = 1 << 30
		for _, c := range []*chunkUnit{c0, c1} {
			for _, e := range c.life {
				lo, hi = min(lo, nOf(e)), max(hi, nOf(e))
			}
		}
		return
	}
	// 500 objects, 200 a step: the round goes through chunk (0,0), then (1,0),
	// and wraps around; over three rounds nobody gets ahead by more than one tick
	for w.Tick < u+3*60+8 {
		steps(t, w, 1)
		if lo, hi := spread(); hi-lo > 1 {
			t.Fatalf("step %d: round robin broken across chunks (%d..%d)", w.Tick, lo, hi)
		}
	}
	// chunk (0,0) loads and sleeps again in the middle of a round
	w.CamX = 2 * 1024 // chunks 1..3 load: (1,0) wakes, (0,0) stays asleep
	steps(t, w, 1)
	if w.ChunkLoaded(1, 0) != true {
		t.Fatal("chunk 1,0 should have woken")
	}
	w.CamX = 4 * 1024
	steps(t, w, 1)
	if w.ChunkLoaded(1, 0) || w.LifeObjects() != 500 {
		t.Fatalf("chunk 1,0 should sleep again: %d sleepers", w.LifeObjects())
	}
	c1 = w.chunks[ChunkKey{1, 0}]
	before := map[int]int{}
	for _, e := range c1.life {
		before[e.ID] = nOf(e)
	}
	b0 := map[int]int{}
	for _, e := range c0.life {
		b0[e.ID] = nOf(e)
	}
	for w.Tick < u+6*60 {
		steps(t, w, 1)
	}
	for _, e := range c1.life {
		if nOf(e) <= before[e.ID] {
			t.Fatalf("%s starved after the chunk slept again", e.Name)
		}
	}
	for _, e := range c0.life {
		if nOf(e) < b0[e.ID]+1 {
			t.Fatalf("%s starved", e.Name)
		}
	}
	mustNoErrors(t, sink)
}

func TestLifeLoopingScriptsDoNotEatTheFrame(t *testing.T) {
	var kids []string
	for i := 0; i < 100; i++ {
		kids = append(kids, alive(fmt.Sprintf("loop%03d", i), 2+i*8, 2, scr("loop")))
	}
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{{1, 0}: strings.Join(kids, ",")},
		map[string]string{
			"loop": "var n = 0\nwhen every 1 seconds\n  n += 1\n  while yes\n    n += 1\n",
			"game": "var ticks = 0\nwhen update\n  ticks += 1\n  repeat 200 times\n    ticks += 0\n",
		}, sprite("game", 5, 5, scr("game")))
	steps(t, w, 2)
	u := away(t, w)
	eng := w.scripts.eng
	game := find(w, "game")
	before := varOf(t, game, "ticks").N
	n := 0
	for w.Tick < u+60+30 {
		steps(t, w, 1)
		n++
		if eng.Skipped() != 0 {
			t.Fatalf("step %d: loaded scripts were skipped (%d) because of offline ticks", w.Tick, eng.Skipped())
		}
		if used := eng.Limits.FrameSteps - eng.StepsLeft(); used > eng.Limits.FrameSteps/lifeBudgetDiv+5000 {
			t.Fatalf("step %d: %d steps used, the offline share is a quarter", w.Tick, used)
		}
	}
	if got := varOf(t, game, "ticks").N - before; got != float64(n) {
		t.Fatalf("the loaded object ran %v times in %d steps", got, n)
	}
	if sink.count("офлайн-тике") == 0 {
		t.Fatalf("the runaway offline handlers must be reported:\n%.600s", sink.all())
	}
	if sink.count("шагов за кадр") != 0 {
		t.Fatalf("the frame budget message must not appear:\n%s", sink.all())
	}
	// the objects keep their state and the tick limit was the one that stopped them
	e := w.chunks[ChunkKey{1, 0}].life[0]
	if v := varOf(t, e, "n").N; v < 1 {
		t.Fatalf("state lost: n=%v", v)
	}
	if lim := w.lifeTickSteps(); lim > eng.Limits.FrameSteps/lifeBudgetDiv {
		t.Fatalf("a single tick may not exceed the offline share: %d", lim)
	}
}

func TestMessagesToSleepingObjects(t *testing.T) {
	w, sink := lifeWorld(t, 60, map[ChunkKey]string{
		{1, 0}: alive("cow", 10, 10, scr("cow")),
	}, map[string]string{
		"cow":  "var got = 0\nvar lists = 0\nwhen message \"moo\"\n  got += value\nwhen message \"herd\"\n  lists += 1\n",
		"hero": "var cow = nothing\nwhen start\n  cow = find(\"cow\")\nwhen key a pressed\n  send \"moo\" with 1 to cow\nwhen key b pressed\n  send \"herd\" with [1, 2] to cow\n",
	}, sprite("hero", 5, 5, scr("hero")))
	in := w.Input.(*InputState)
	steps(t, w, 2)
	u := away(t, w)
	press := func(k string) {
		in.Press(k)
		steps(t, w, 1)
		in.Release(k)
		steps(t, w, 1)
	}
	for i := 0; i < 20; i++ { // 20 messages to a queue of 16: the oldest four are dropped
		press("a")
	}
	press("b")
	cow := w.chunks[ChunkKey{1, 0}].life[0]
	if len(cow.mail) != LifeMailPerObject || w.mailTotal != LifeMailPerObject {
		t.Fatalf("queued %d (total %d), want %d", len(cow.mail), w.mailTotal, LifeMailPerObject)
	}
	if sink.count("самые старые отброшены") != 1 {
		t.Fatalf("one line about the dropped messages expected:\n%s", sink.all())
	}
	if varOf(t, cow, "got").N != 0 {
		t.Fatal("a sleeping object must not run handlers before its life tick")
	}
	for w.Tick < u+60+2 { // its first life tick: the mail arrives
		steps(t, w, 1)
	}
	// the list message dropped its list (nothing) but was still delivered; the last 16 messages: 15 "moo" and 1 "herd"
	if got, lists := varOf(t, cow, "got").N, varOf(t, cow, "lists").N; got != 15 || lists != 1 {
		t.Fatalf("got %v moos and %v herds", got, lists)
	}
	if len(cow.mail) != 0 || w.mailTotal != 0 {
		t.Fatalf("the mail must be consumed: %d (total %d)", len(cow.mail), w.mailTotal)
	}
	// mail that arrives and is not yet delivered when the chunk loads goes to the loaded object
	press("a")
	press("a")
	back(t, w)
	steps(t, w, 3)
	if got := varOf(t, find(w, "cow"), "got").N; got != 17 {
		t.Fatalf("messages queued at the moment of the wake-up must reach the loaded object: got %v", got)
	}
	mustNoErrors(t, sink)
}
