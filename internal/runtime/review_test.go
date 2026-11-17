package runtime

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// chunkScripted builds a project whose map has the given chunk contents
// (chunk (cx, cy) -> node JSON), with every script file written.
func chunkScripted(t *testing.T, chunks map[ChunkKey]string, scripts map[string]string) (*World, *InputState, *logSink, string) {
	t.Helper()
	root := t.TempDir()
	for name, src := range scripts {
		write(t, root, "scripts/"+name+".tdsl", src)
	}
	var cs []string
	for _, k := range []ChunkKey{{0, 0}, {1, 0}, {5, 0}, {0, 3}} {
		if kids, ok := chunks[k]; ok {
			cs = append(cs, chunk(k.X, k.Y, kids))
		}
	}
	write(t, root, "game/maps/m.vide.json", doc("map", "", strings.Join(cs, ",")))
	write(t, root, "project.vide.json", doc("scene", `"map":"game/maps/m.vide.json","view":{"w":320,"h":180},`, ""))
	in, sink := NewInputState(), &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: in, Log: sink.log, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	return w, in, sink, root
}

func scr(name string) string { return fmt.Sprintf(`,"script":"scripts/%s.tdsl"`, name) }

func TestChunkReloadKeepsScriptStateAndDestroyedObjects(t *testing.T) {
	w, in, sink, _ := chunkScripted(t, map[ChunkKey]string{
		{0, 0}: sprite("coin", 10, 10, scr("coin")) + "," + sprite("pickup", 50, 10, scr("pickup")) + "," + sprite("plain", 90, 10, ""),
		{5, 0}: sprite("far", 0, 0, ""),
	}, map[string]string{
		"coin":   "var n = 0\nvar items = [1]\nwhen start\n  y += 100\nwhen update\n  n += 1\n  x += 1\n  add n to items\n",
		"pickup": "when key space pressed\n  destroy\n",
	})
	coin := find(w, "coin")
	id := coin.ID
	steps(t, w, 3)
	in.Press("space")
	steps(t, w, 1)
	in.Release("space")
	steps(t, w, 1)
	if find(w, "pickup") != nil {
		t.Fatal("pickup survived its destroy")
	}
	x, y := coin.X, coin.Y // x = 10+5, y = 110 (start ran once)
	if x != 15 || y != 110 {
		t.Fatalf("coin at %v,%v", x, y)
	}
	before := w.Len()
	// walk away: the camera moves to chunk (5,0), chunk (0,0) unloads
	w.CamX = 5 * 1024
	steps(t, w, 1)
	if w.ChunkLoaded(0, 0) || find(w, "coin") != nil || w.Entity(id) != nil {
		t.Fatal("chunk (0,0) is still loaded")
	}
	if !w.ChunkState(0, 0).Destroyed["1"] || len(w.ChunkState(0, 0).Entities) != 2 {
		t.Fatalf("state: %+v", w.ChunkState(0, 0))
	}
	x, y = coin.X, coin.Y // the unloading step still ran the script once
	oldN, _ := coin.Behavior.(*scriptBehavior).inst.Global("n")
	steps(t, w, 20) // the world goes on without them
	// and back
	w.CamX = 0
	steps(t, w, 1)
	coin2 := find(w, "coin")
	if coin2 == nil || find(w, "pickup") != nil || find(w, "plain") == nil {
		t.Fatalf("after reload: coin=%v pickup=%v", coin2, find(w, "pickup"))
	}
	if coin2.ID != id {
		t.Fatalf("id changed %d -> %d", id, coin2.ID)
	}
	if coin2 == coin {
		t.Fatal("expected a fresh entity object")
	}
	// the step that reloaded it already ran the update phase? No: syncChunks is
	// last in Step, so the next step is the first with the restored script.
	if coin2.X != x || coin2.Y != y {
		t.Fatalf("position lost: %v,%v want %v,%v", coin2.X, coin2.Y, x, y)
	}
	steps(t, w, 1)
	if coin2.X != x+1 || coin2.Y != y {
		t.Fatalf("after one more step %v,%v (start must not run again)", coin2.X, coin2.Y)
	}
	if w.Len() != before { // the same population as before the walk
		t.Fatalf("population %d, was %d", w.Len(), before)
	}
	if strings.Contains(sink.all(), "Во время игры") {
		t.Fatalf("log: %s", sink.all())
	}
	// variables continued counting from where they were
	sb := coin2.Behavior.(*scriptBehavior)
	n, _ := sb.inst.Global("n")
	items, _ := sb.inst.Global("items")
	if n.N != oldN.N+1 || len(items.L.Items) != int(oldN.N)+2 {
		t.Fatalf("n=%v items=%d", n.N, len(items.L.Items))
	}
}

func TestFailedChunkLoadRollsBack(t *testing.T) {
	w, _, _, _ := chunkScripted(t, map[ChunkKey]string{
		{0, 0}: sprite("a", 0, 0, ""),
		{5, 0}: sprite("ok", 0, 0, "") + "," + inst("bad", 5, 5, "game/objects/missing.vide.json"),
	}, nil)
	live, ids := w.Len(), len(w.byID)
	if err := w.LoadChunk(5, 0); err == nil {
		t.Fatal("expected an error")
	}
	if w.Len() != live || len(w.byID) != ids || w.ChunkLoaded(5, 0) {
		t.Fatalf("not rolled back: live %d->%d byID %d->%d", live, w.Len(), ids, len(w.byID))
	}
}

func TestScriptNamesDoNotDependOnLoadedChunks(t *testing.T) {
	w, _, sink, _ := chunkScripted(t, map[ChunkKey]string{
		{0, 0}: sprite("g00", 0, 0, scr("peek")),
		{0, 3}: sprite("g03", 0, 0, ""), // far away, not loaded
	}, map[string]string{"peek": "when start\n  print g03.x\n"})
	if w.ChunkLoaded(0, 3) {
		t.Fatal("test needs chunk (0,3) unloaded")
	}
	steps(t, w, 1)
	if sink.count("не запущен") != 0 || sink.count("TD499") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func groupWorld(t *testing.T, scripts map[string]string) (*World, *InputState, *logSink) {
	t.Helper()
	root := t.TempDir()
	for n, s := range scripts {
		write(t, root, "scripts/"+n+".tdsl", s)
	}
	grp := fmt.Sprintf(`{"name":"grp","kind":"Node","x":100,"y":50%s,"children":[%s,%s]}`, scr("grp"),
		sprite("kid", 10, 5, scr("kid")), `{"name":"inner","kind":"Node","x":1,"y":1,"children":[`+sprite("deep", 2, 2, "")+`]}`)
	write(t, root, "project.vide.json", doc("scene", "", grp+","+sprite("other", 300, 0, "")))
	in, sink := NewInputState(), &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: in, Log: sink.log})
	if err != nil {
		t.Fatal(err)
	}
	return w, in, sink
}

func TestGroupCoordinatesAreRelativeToTheParent(t *testing.T) {
	w, in, sink := groupWorld(t, map[string]string{
		"kid": "when start\n  print \"{x},{y}\"\n  x = 20\nwhen key a pressed\n  move to 1, 2\nwhen key b pressed\n  move right by 3\n",
		"grp": "when key space pressed\n  x += 5\nwhen key d pressed\n  destroy\n",
	})
	kid, grp, deep := find(w, "kid"), find(w, "grp"), find(w, "deep")
	steps(t, w, 1)
	if sink.all() != "10,5" || kid.X != 120 || kid.Y != 55 {
		t.Fatalf("log %q kid %v,%v", sink.all(), kid.X, kid.Y)
	}
	// moving the parent carries the children (including grandchildren)
	deepX, kidX := deep.X, kid.X
	in.Press("space")
	steps(t, w, 1)
	in.Release("space")
	if grp.X != 105 || kid.X != kidX+5 || deep.X != deepX+5 || find(w, "other").X != 300 {
		t.Fatalf("grp %v kid %v deep %v", grp.X, kid.X, deep.X)
	}
	// the child's own x stays relative
	in.Press("a")
	steps(t, w, 1)
	in.Release("a")
	if kid.X != 106 || kid.Y != 52 {
		t.Fatalf("move to 1,2 gave world %v,%v, want 106,52", kid.X, kid.Y)
	}
	in.Press("b")
	steps(t, w, 1)
	in.Release("b")
	if kid.X != 109 {
		t.Fatalf("kid.X = %v", kid.X)
	}
	// destroying the parent destroys the whole group, at the end of the frame
	before := w.Len()
	in.Press("d")
	steps(t, w, 1)
	if find(w, "grp") != nil || find(w, "kid") != nil || find(w, "deep") != nil || find(w, "inner") != nil || find(w, "other") == nil {
		t.Fatal("group not destroyed")
	}
	if w.Len() != before-4 {
		t.Fatalf("len %d, was %d", w.Len(), before)
	}
	if w.Entity(kid.ID) != nil || !kid.Dead {
		t.Fatal("kid still indexed")
	}
}

func TestMoveToAndAssignmentBehaveTheSame(t *testing.T) {
	w, in, _, _ := scriptWorldPrev(t)
	a := find(w, "a")
	// the object was created in step 1: its start-phase placement does not glide
	steps(t, w, 1)
	if a.X != 7 || a.PrevX != 7 {
		t.Fatalf("start placement: x=%v prev=%v", a.X, a.PrevX)
	}
	in.Press("space")
	steps(t, w, 1)
	in.Release("space")
	if a.X != 50 || a.PrevX != 7 {
		t.Fatalf("move to: x=%v prev=%v (must glide)", a.X, a.PrevX)
	}
	in.Press("a")
	steps(t, w, 1)
	in.Release("a")
	if a.X != 90 || a.PrevX != 50 {
		t.Fatalf("x = ...: x=%v prev=%v (must glide)", a.X, a.PrevX)
	}
}

func scriptWorldPrev(t *testing.T) (*World, *InputState, *logSink, int) {
	w, in, sink := scriptWorld(t, 60, map[string]string{"a": "when start\n  x = 7\nwhen key space pressed\n  move to 50, 0\nwhen key a pressed\n  x = 90\n"}, []string{"a"})
	return w, in, sink, 0
}

func TestNonFiniteAndHugeValuesAreRejectedByTheHost(t *testing.T) {
	w, _, sink := scriptWorld(t, 60, map[string]string{"a": "var v = 10\nwhen start\n  repeat 400 times\n    v *= 10\n  x = v\n  move right by v\n  move to v, v\n  width = v - v\n  y = 3000000000\n"}, []string{"a"})
	steps(t, w, 1)
	a := find(w, "a")
	if a.X != 0 || a.Y != 0 || a.W != 16 || sink.count("TD205") < 1 {
		t.Fatalf("x=%v y=%v w=%v log=%s", a.X, a.Y, a.W, sink.all())
	}
	// the host itself refuses too, whatever the VM does
	h := &scriptHost{w}
	id := vm.ObjID(a.ID)
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 2e9} {
		if h.MoveBy(id, bad, 0) == nil || h.MoveTo(id, 0, bad) == nil || h.SetProp(id, vm.PropX, vm.Num(bad)) == nil ||
			h.SetProp(id, vm.PropWidth, vm.Num(bad)) == nil || h.SetProp(id, vm.PropAngle, vm.Num(bad)) == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	if a.X != 0 || a.Y != 0 {
		t.Fatal("host moved on bad input")
	}
	if err := h.MoveBy(id, 1e9, 0); err != nil {
		t.Fatalf("1e9 is allowed: %v", err)
	}
	if err := h.MoveBy(id, 1, 0); err == nil {
		t.Fatal("beyond 1e9 accepted")
	}
}

func TestLogIsSanitized(t *testing.T) {
	w, _, sink := scriptWorld(t, 60, nil, []string{"a"})
	h := &scriptHost{w}
	h.Log("red \x1b[31mtext\x07\r\nline2\tend\x7f")
	if got := sink.msgs[0]; strings.ContainsAny(got, "\x1b\x07\r\x7f") || !strings.Contains(got, "\n") || !strings.Contains(got, "\t") || !strings.Contains(got, "[31m") {
		t.Fatalf("%q", got)
	}
	// error reports go through the same filter
	w.logf("bad \x1b[2J name")
	if strings.Contains(sink.msgs[1], "\x1b") {
		t.Fatal("escape survived in logf")
	}
}

func TestEscapeReachesScriptsAndDoesNotQuit(t *testing.T) {
	w, in, sink := scriptWorld(t, 60, map[string]string{"a": "when key escape pressed\n  x += 1\n"}, []string{"a"})
	in.Press("escape")
	steps(t, w, 1)
	if find(w, "a").X != 1 || sink.count("клавишу") != 0 {
		t.Fatalf("x=%v log=%s", find(w, "a").X, sink.all())
	}
	src, err := os.ReadFile("game.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "Termination") && strings.Contains(strings.Split(string(src), "Ctrl+Q")[0], "KeyEscape") {
		t.Fatal("game.go still quits on Escape")
	}
	if !strings.Contains(string(src), "KeyQ") {
		t.Fatal("Ctrl+Q quit is gone")
	}
}

func TestSeedZeroIsAnExplicitSeed(t *testing.T) {
	run := func(seed uint64) string {
		root := t.TempDir()
		write(t, root, "scripts/a.tdsl", "when start\n  print random(1, 1000000)\n")
		write(t, root, "project.vide.json", doc("scene", "", sprite("a", 0, 0, scr("a"))))
		sink := &logSink{}
		w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Log: sink.log, Seed: seed})
		if err != nil {
			t.Fatal(err)
		}
		steps(t, w, 1)
		return sink.all()
	}
	if run(0) != run(0) || run(0) == run(1) {
		t.Fatal("seed 0 is not a stable, distinct seed")
	}
}
