package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vide2d/vide-studio/internal/scene"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// doc builds a version 4 document of the given type around nodes JSON.
func doc(typ, extra, rootKids string) string {
	return fmt.Sprintf(`{"version":4,"type":%q,%s"scene":{"name":"R","kind":"Node","children":[%s]}}`, typ, extra, rootKids)
}

func sprite(name string, x, y int, extra string) string {
	return fmt.Sprintf(`{"name":%q,"kind":"Sprite","x":%d,"y":%d,"w":16,"h":16%s}`, name, x, y, extra)
}

func inst(name string, x, y int, ref string) string {
	return fmt.Sprintf(`{"name":%q,"kind":"Instance","x":%d,"y":%d,"ref":%q}`, name, x, y, ref)
}

func chunk(cx, cy int, kids string) string {
	return fmt.Sprintf(`{"name":"c%d_%d","kind":"Chunk","chunk":[%d,%d],"children":[%s]}`, cx, cy, cx, cy, kids)
}

func load(t *testing.T, root, scenePath string) (*World, error) {
	t.Helper()
	w, _, err := Load(filepath.Join(root, scenePath), root, Options{})
	return w, err
}

func names(w *World) []string {
	var out []string
	w.Each(func(e *Entity) { out = append(out, e.Name) })
	return out
}

func TestLoadInstancesNestedAndOrder(t *testing.T) {
	root := t.TempDir()
	write(t, root, "game/objects/leaf.vide.json", doc("object", "", sprite("leafspr", 1, 2, `,"tag":"leaf","script":"scripts/a.tdsl"`)))
	write(t, root, "game/objects/mid.vide.json", doc("object", "", sprite("midspr", 10, 0, "")+","+inst("inner", 5, 5, "game/objects/leaf.vide.json")))
	write(t, root, "project.vide.json", doc("scene", "", sprite("first", 0, 0, "")+","+inst("m", 100, 200, "game/objects/mid.vide.json")+","+sprite("last", 3, 3, "")))
	w, err := load(t, root, "project.vide.json")
	if err != nil {
		t.Fatal(err)
	}
	want := "R first m midspr inner leafspr last"
	if got := strings.Join(names(w), " "); got != want {
		t.Fatalf("order %q, want %q", got, want)
	}
	var leaf *Entity
	w.Each(func(e *Entity) {
		if e.Name == "leafspr" {
			leaf = e
		}
	})
	// m(100,200) + inner(5,5) + leafspr(1,2)
	if leaf.X != 106 || leaf.Y != 207 || !leaf.HasTag("leaf") || leaf.Script != "scripts/a.tdsl" {
		t.Fatalf("leaf = %+v", leaf)
	}
	if leaf.Parent == nil || leaf.Parent.Name != "inner" {
		t.Fatal("bad parent")
	}
	if w.Len() != 7 {
		t.Fatalf("Len = %d", w.Len())
	}
}

func TestLoadCycleAndDepth(t *testing.T) {
	root := t.TempDir()
	write(t, root, "game/objects/a.vide.json", doc("object", "", inst("b", 0, 0, "game/objects/b.vide.json")))
	write(t, root, "game/objects/b.vide.json", doc("object", "", inst("a", 0, 0, "game/objects/a.vide.json")))
	write(t, root, "game/objects/self.vide.json", doc("object", "", inst("me", 0, 0, "game/objects/self.vide.json")))
	write(t, root, "s1.vide.json", doc("scene", "", inst("a", 0, 0, "game/objects/a.vide.json")))
	write(t, root, "s2.vide.json", doc("scene", "", inst("s", 0, 0, "game/objects/self.vide.json")))
	for _, s := range []string{"s1.vide.json", "s2.vide.json"} {
		if _, err := load(t, root, s); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("%s: err = %v, want cycle", s, err)
		}
	}
	// A legal chain deeper than the limit is refused too.
	for i := 0; i < MaxInstanceDepth+2; i++ {
		body := sprite("s", 0, 0, "")
		if i+1 < MaxInstanceDepth+2 {
			body = inst("n", 0, 0, fmt.Sprintf("game/objects/d%d.vide.json", i+1))
		}
		write(t, root, fmt.Sprintf("game/objects/d%d.vide.json", i), doc("object", "", body))
	}
	write(t, root, "deep.vide.json", doc("scene", "", inst("n", 0, 0, "game/objects/d0.vide.json")))
	if _, err := load(t, root, "deep.vide.json"); err == nil || !strings.Contains(err.Error(), "nested deeper") {
		t.Fatalf("deep: err = %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	root := t.TempDir()
	write(t, root, "missing.vide.json", doc("scene", "", inst("x", 0, 0, "game/objects/nope.vide.json")))
	write(t, root, "badobj.vide.json", doc("scene", "", inst("x", 0, 0, "game/objects/bad.vide.json")))
	write(t, root, "game/objects/bad.vide.json", `{"version":4,"scene":`)
	write(t, root, "game/objects/notobj.vide.json", doc("scene", "", ""))
	write(t, root, "wrongtype.vide.json", doc("scene", "", inst("x", 0, 0, "game/objects/notobj.vide.json")))
	write(t, root, "obj.vide.json", doc("object", "", ""))
	write(t, root, "nomap.vide.json", doc("scene", `"map":"game/maps/none.vide.json",`, ""))
	write(t, root, "game/maps/small.vide.json", doc("map", `"tile_size":8,`, ""))
	write(t, root, "tilemis.vide.json", doc("scene", `"map":"game/maps/small.vide.json",`, ""))
	write(t, root, "garbage.vide.json", "\x00\x01 not json")
	write(t, root, "empty.vide.json", "")
	cases := map[string]string{
		"missing.vide.json":   "nope.vide.json",
		"badobj.vide.json":    "bad.vide.json",
		"wrongtype.vide.json": "not an object",
		"obj.vide.json":       "cannot be run",
		"nomap.vide.json":     "map:",
		"tilemis.vide.json":   "tile_size",
		"garbage.vide.json":   "invalid project file",
		"empty.vide.json":     "invalid project file",
		"absent.vide.json":    "cannot open",
	}
	for file, want := range cases {
		_, err := load(t, root, file)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", file, err, want)
		}
	}
}

func TestSymlinksAndHugeFilesRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "o.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	if err := os.Symlink(filepath.Join(outside, "o.vide.json"), filepath.Join(root, "link.vide.json")); err != nil {
		t.Skip("no symlinks")
	}
	if err := os.MkdirAll(filepath.Join(root, "game"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "game", "objects")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "s.vide.json", doc("scene", "", inst("x", 0, 0, "game/objects/o.vide.json")))
	if _, err := load(t, root, "s.vide.json"); err == nil || !strings.Contains(err.Error(), "symbolic") {
		t.Fatalf("err = %v, want symlink refusal", err)
	}
	src := NewSource(root)
	for _, rel := range []string{"../x.vide.json", "/etc/passwd", "a\\b.vide.json"} {
		if _, err := src.Doc(rel); err == nil {
			t.Errorf("%q accepted", rel)
		}
	}
	write(t, root, "big.vide.json", strings.Repeat(" ", MaxDocBytes+10))
	if _, err := src.Doc("big.vide.json"); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("big: %v", err)
	}
}

func TestTemplatesExcluded(t *testing.T) {
	root := t.TempDir()
	write(t, root, "game/objects/o.vide.json", doc("object", "", sprite("keep", 0, 0, "")+","+sprite("tmpl", 0, 0, `,"template":true`)))
	write(t, root, "project.vide.json", doc("scene", "",
		sprite("a", 0, 0, "")+","+
			`{"name":"proto","kind":"Node","template":true,"children":[`+sprite("inside", 0, 0, "")+`]},`+
			inst("tinst", 0, 0, "game/objects/o.vide.json")[:len(inst("tinst", 0, 0, "game/objects/o.vide.json"))-1]+`,"template":true}`+","+
			inst("real", 0, 0, "game/objects/o.vide.json")))
	w, err := load(t, root, "project.vide.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(w), " "); got != "R a real keep" {
		t.Fatalf("got %q", got)
	}
}

func chunkWorld(t *testing.T) *World {
	t.Helper()
	root := t.TempDir()
	write(t, root, "game/maps/m.vide.json", doc("map", "",
		chunk(0, 0, sprite("g00", 0, 0, ""))+","+chunk(1, 0, sprite("g10", 4, 4, ""))+","+chunk(5, 0, sprite("g50", 0, 0, ""))+","+chunk(0, 3, sprite("g03", 0, 0, ""))))
	write(t, root, "project.vide.json", doc("scene", `"map":"game/maps/m.vide.json","view":{"w":320,"h":180},`, ""))
	w, err := load(t, root, "project.vide.json")
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestChunkLoadUnload(t *testing.T) {
	w := chunkWorld(t)
	if !w.Optimized || len(w.ChunkKeys()) != 4 {
		t.Fatalf("optimized=%v keys=%v", w.Optimized, w.ChunkKeys())
	}
	// Camera at 0,0, radius 1: chunks (0,0) and (1,0) are near; (5,0) and (0,3) are far.
	if got := w.LoadedChunks(); len(got) != 2 || got[0] != (ChunkKey{0, 0}) || got[1] != (ChunkKey{1, 0}) {
		t.Fatalf("loaded %v", got)
	}
	var g10 *Entity
	w.Each(func(e *Entity) {
		if e.Name == "g10" {
			g10 = e
		}
	})
	if g10 == nil || g10.X != 64*16+4 || g10.Y != 4 {
		t.Fatalf("g10 = %+v", g10)
	}
	before := w.Len()
	if err := w.LoadChunk(5, 0); err != nil {
		t.Fatal(err)
	}
	if !w.ChunkLoaded(5, 0) || w.Len() != before+1 {
		t.Fatal("chunk 5,0 not loaded")
	}
	if err := w.LoadChunk(5, 0); err != nil || w.Len() != before+1 {
		t.Fatal("double load changed the world")
	}
	if !w.UnloadChunk(5, 0) || w.UnloadChunk(5, 0) || w.Len() != before {
		t.Fatal("unload")
	}
	if err := w.LoadChunk(9, 9); !errors.Is(err, ErrNoChunk) {
		t.Fatalf("err = %v", err)
	}
	// Moving the camera is picked up by the next Step.
	w.CamX = 5 * 64 * 16
	if err := w.Step(); err != nil {
		t.Fatal(err)
	}
	if got := w.LoadedChunks(); len(got) != 1 || got[0] != (ChunkKey{5, 0}) {
		t.Fatalf("after move: %v", got)
	}
	// Negative camera and the vertical axis.
	w.CamX, w.CamY = -10, 3*64*16-5
	if err := w.UpdateChunks(0); err != nil {
		t.Fatal(err)
	}
	if got := w.LoadedChunks(); len(got) != 1 || got[0] != (ChunkKey{0, 3}) {
		t.Fatalf("radius 0: %v", got)
	}
}

func TestFreeFormMapLoadsWhole(t *testing.T) {
	root := t.TempDir()
	write(t, root, "game/maps/m.vide.json", doc("map", `"optimization":false,`, sprite("far", 99999, 0, "")+","+sprite("near", 1, 1, "")))
	write(t, root, "project.vide.json", doc("scene", `"map":"game/maps/m.vide.json",`, sprite("s", 0, 0, "")))
	w, err := load(t, root, "project.vide.json")
	if err != nil {
		t.Fatal(err)
	}
	if w.Optimized || len(w.ChunkKeys()) != 0 {
		t.Fatal("free-form map must not be chunked")
	}
	// Map drawn first, then scene content.
	if got := strings.Join(names(w), " "); got != "far near R s" {
		t.Fatalf("got %q", got)
	}
}

func TestClockExactSteps(t *testing.T) {
	for _, rate := range []int{60, 90, 120} {
		for _, frame := range []time.Duration{time.Second / 60, 7 * time.Millisecond, 16666667 * time.Nanosecond, time.Millisecond} {
			c := NewClock(rate)
			total := 0
			for elapsed := time.Duration(0); elapsed < 10*time.Second; elapsed += frame {
				total += c.Advance(min(frame, 10*time.Second-elapsed))
			}
			if total != 10*rate {
				t.Errorf("rate %d frame %v: %d steps, want %d", rate, frame, total, 10*rate)
			}
		}
	}
}

func TestClockAlphaAndCatchUp(t *testing.T) {
	c := NewClock(60)
	if n := c.Advance(time.Second / 120); n != 0 || c.Alpha() < 0.49 || c.Alpha() > 0.51 {
		t.Fatalf("n=%d alpha=%v", n, c.Alpha())
	}
	if n := c.Advance(-time.Hour); n != 0 {
		t.Fatal("negative dt ran steps")
	}
	if n := c.Advance(time.Hour); n != 15 { // capped at rate/4
		t.Fatalf("stall ran %d steps", n)
	}
	if c.Alpha() < 0 || c.Alpha() >= 1 {
		t.Fatalf("alpha %v", c.Alpha())
	}
}

type mover struct{ dx int }

func (m mover) Step(_ *World, e *Entity) { e.SetPos(e.X+m.dx, e.Y) }

func TestSimDeterministicAcrossRates(t *testing.T) {
	// A behavior that moves 1 px per step: after N seconds the position is
	// N*rate, so state matches per step count, and Tick matches exactly.
	for _, rate := range []int{60, 90, 120} {
		root := t.TempDir()
		write(t, root, "project.vide.json", doc("scene", fmt.Sprintf(`"tick_rate":%d,`, rate), sprite("m", 0, 0, "")))
		w, src, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Attach: func(e *Entity) {
			if e.Name == "m" {
				e.Behavior = mover{1}
			}
		}})
		if err != nil || src == nil {
			t.Fatal(err)
		}
		sim := NewSim(w)
		for i := 0; i < 5*1000; i++ { // 5 s in 1 ms frames
			if _, err := sim.Update(time.Millisecond); err != nil {
				t.Fatal(err)
			}
		}
		var m *Entity
		w.Each(func(e *Entity) {
			if e.Name == "m" {
				m = e
			}
		})
		if int(w.Tick) != 5*rate || m.X != 5*rate || m.PrevX != m.X-1 {
			t.Errorf("rate %d: tick=%d x=%d prev=%d", rate, w.Tick, m.X, m.PrevX)
		}
	}
}

func TestInterpolation(t *testing.T) {
	e := &Entity{}
	e.Teleport(10, 0)
	e.SetPos(20, 0)
	if x, _ := e.Lerp(0.5); x != 15 {
		t.Fatalf("x = %v", x)
	}
}

func TestInvalidTickRate(t *testing.T) {
	sc := scene.New("S")
	sc.TickRate = 75
	if _, err := NewWorld(NewSource(t.TempDir()), sc, Options{}); err == nil {
		t.Fatal("tick rate 75 accepted")
	}
}

func TestFit(t *testing.T) {
	cases := []struct {
		name        string
		ww, wh      int
		mode        scene.ImageMode
		scale, x, y float64
		w, h        float64
	}{
		{"pixel exact", 960, 540, scene.ImagePixel, 3, 0, 0, 960, 540},
		{"pixel letterbox", 1000, 700, scene.ImagePixel, 3, 20, 80, 960, 540},
		{"pixel height bound", 1920, 720, scene.ImagePixel, 4, 320, 0, 1280, 720},
		{"pixel window smaller than view", 200, 100, scene.ImagePixel, 1, -60, -40, 320, 180},
		{"pixel odd bars floor", 321, 181, scene.ImagePixel, 1, 0, 0, 320, 180},
		{"normal aspect", 1000, 700, scene.ImageNormal, 1000.0 / 320, 0, (700 - 180*1000.0/320) / 2, 1000, 180 * 1000.0 / 320},
		{"normal wide window", 1920, 540, scene.ImageNormal, 3, 480, 0, 960, 540},
	}
	for _, c := range cases {
		p := Fit(c.ww, c.wh, 320, 180, c.mode)
		if p.Scale != c.scale || p.X != c.x || p.Y != c.y || p.W != c.w || p.H != c.h {
			t.Errorf("%s: %+v, want scale %v at %v,%v size %vx%v", c.name, p, c.scale, c.x, c.y, c.w, c.h)
		}
		if c.mode == scene.ImagePixel && (p.Scale != float64(int(p.Scale)) || p.X != float64(int(p.X)) || p.Y != float64(int(p.Y))) {
			t.Errorf("%s: pixel mode not whole: %+v", c.name, p)
		}
	}
	if p := Fit(0, 0, 320, 180, scene.ImagePixel); p.Scale != 1 {
		t.Fatal("degenerate window")
	}
}

func TestProjectRoot(t *testing.T) {
	sep := string(filepath.Separator)
	if got := ProjectRoot(filepath.FromSlash("/p/game/scenes/l.vide.json")); got != sep+"p" {
		t.Fatal(got)
	}
	if got := ProjectRoot(filepath.FromSlash("/p/project.vide.json")); got != sep+"p" {
		t.Fatal(got)
	}
}

func TestDemoProjectLoads(t *testing.T) {
	file := filepath.Join("..", "..", "examples", "demo", "project.vide.json")
	w, _, err := Load(file, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Len() == 0 {
		t.Fatal("empty world")
	}
}

func TestFitPixelCropIsSymmetricRoundedDown(t *testing.T) {
	// window 100x50 shows a 320x180 view at scale 1: 220 columns are cropped.
	p := Fit(99, 50, 320, 180, scene.ImagePixel)
	if p.X != -111 { // floor(-221/2)
		t.Fatalf("X = %v, want -111", p.X)
	}
}
