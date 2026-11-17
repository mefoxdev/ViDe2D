package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// gp is a throw-away game on disk with several scenes.
type gp struct {
	t     *testing.T
	root  string
	in    *InputState
	sink  *logSink
	audio *FakeAudio
	store Store
	seed  uint64
}

func newGP(t *testing.T) *gp {
	t.Helper()
	return &gp{t: t, root: t.TempDir(), in: NewInputState(), sink: &logSink{}, audio: &FakeAudio{}, store: NewMemStore(), seed: 9}
}

func (g *gp) script(name, src string) { write(g.t, g.root, "scripts/"+name+".tdsl", src) }

// scene writes a scene document (rel "" is the main scene, project.vide.json).
func (g *gp) scene(rel string, rate, vw, vh int, kids ...string) {
	if rel == "" {
		rel = "project.vide.json"
	}
	write(g.t, g.root, rel, doc("scene", fmt.Sprintf(`"tick_rate":%d,"view":{"w":%d,"h":%d},`, rate, vw, vh), strings.Join(kids, ",")))
}

func (g *gp) wav(rel string, n int) { write(g.t, g.root, rel, string(wavBytes(n))) }

func (g *gp) opts() Options {
	return Options{Scripts: true, Input: g.in, Log: g.sink.log, Seed: g.seed, Audio: g.audio, Store: g.store}
}

func (g *gp) sim() *Sim {
	g.t.Helper()
	w, _, err := Load(filepath.Join(g.root, "project.vide.json"), g.root, g.opts())
	if err != nil {
		g.t.Fatal(err)
	}
	return NewSim(w)
}

// wavBytes makes a mono 8 kHz 16-bit PCM WAV with n samples.
func wavBytes(n int) []byte {
	var b bytes.Buffer
	le := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	le(uint32(36 + 2*n))
	b.WriteString("WAVEfmt ")
	le(uint32(16))
	le(uint16(1))
	le(uint16(1))
	le(uint32(8000))
	le(uint32(16000))
	le(uint16(2))
	le(uint16(16))
	b.WriteString("data")
	le(uint32(2 * n))
	for i := 0; i < n; i++ {
		le(int16(i % 100))
	}
	return b.Bytes()
}

// ---- wait and slide in the world ----

func TestWaitInTheWorldIsTickBasedAtEveryRate(t *testing.T) {
	for _, rate := range []int{60, 90, 120} {
		w, _, sink := scriptWorld(t, rate, map[string]string{"a": "when start\n  wait 1 second\n  x += 10\n"}, []string{"a"})
		a := find(w, "a")
		steps(t, w, rate) // started in step 1; goes on at step 1 + rate
		if a.X != 0 {
			t.Fatalf("%d Hz: moved early", rate)
		}
		steps(t, w, 1)
		if a.X != 10 {
			t.Fatalf("%d Hz: x = %v after %d steps", rate, a.X, w.Tick)
		}
		mustNoErrors(t, sink)
	}
}

func TestWaitDoesNotDependOnWallClock(t *testing.T) {
	w, _, _ := scriptWorld(t, 60, map[string]string{"a": "when start\n  wait 2 seconds\n  x += 1\n"}, []string{"a"})
	sim := NewSim(w)
	// a long stall is dropped by the clock: the game does not skip ahead in time
	if n, _ := sim.Update(10 * time.Second); n != 15 {
		t.Fatalf("%d steps", n)
	}
	if find(w, "a").X != 0 {
		t.Fatal("wait ended after a stall of real time")
	}
}

func TestSlideInTheWorldGlidesAndFinishes(t *testing.T) {
	w, _, sink := scriptWorld(t, 90, map[string]string{"a": "when start\n  slide to 90, 45 in 1 second\n  show\n  tag = \"done\"\n"}, []string{"a"})
	a := find(w, "a")
	steps(t, w, 1+45)
	if d := a.X - 45; d > 1e-9 || d < -1e-9 {
		t.Fatalf("halfway x = %v", a.X)
	}
	steps(t, w, 45)
	if a.X != 90 || a.Y != 45 || !a.HasTag("done") {
		t.Fatalf("%v %v %v", a.X, a.Y, a.Tags)
	}
	mustNoErrors(t, sink)
}

func TestDestroyedObjectCancelsItsWaitingTasks(t *testing.T) {
	w, in, sink := scriptWorld(t, 60, map[string]string{"a": "when start\n  wait 1 second\n  print \"never\"\nwhen key space pressed\n  destroy\n"}, []string{"a"})
	steps(t, w, 2)
	if w.scripts.eng.Waiting() != 1 {
		t.Fatalf("waiting %d", w.scripts.eng.Waiting())
	}
	in.Press("space")
	steps(t, w, 1)
	if find(w, "a") != nil || w.scripts.eng.Waiting() != 0 {
		t.Fatalf("a=%v waiting %d", find(w, "a"), w.scripts.eng.Waiting())
	}
	steps(t, w, 120)
	if sink.count("never") != 0 {
		t.Fatal("a destroyed object's run went on")
	}
}

func TestWaitingTaskSurvivesChunkUnloadAndReload(t *testing.T) {
	w, _, sink, _ := chunkScripted(t, map[ChunkKey]string{
		{0, 0}: sprite("g00", 0, 0, scr("c")),
		{4, 0}: sprite("g40", 0, 0, ""),
	}, map[string]string{"c": "var seen = 0\nwhen start\n  seen = 1\n  wait 1 second\n  seen = 2\n  print \"done {seen}\"\n"})
	steps(t, w, 3)
	w.CamX = 5 * 1024
	steps(t, w, 1)
	if w.ChunkLoaded(0, 0) {
		t.Fatal("chunk did not unload")
	}
	unloadTick := w.Tick
	if w.scripts.eng.Waiting() != 0 {
		t.Fatalf("an unloaded object still counts as waiting: %d", w.scripts.eng.Waiting())
	}
	steps(t, w, 200)
	if sink.count("done") != 0 {
		t.Fatal("ran while unloaded")
	}
	w.CamX = 0
	steps(t, w, 1)
	if !w.ChunkLoaded(0, 0) {
		t.Fatal("chunk not back")
	}
	reloadTick := w.Tick
	steps(t, w, 1) // the script instance comes back in the first step the object takes part in
	if w.scripts.eng.Waiting() != 1 {
		t.Fatalf("the waiting run was lost: %d", w.scripts.eng.Waiting())
	}
	reloadTick = w.Tick
	for sink.count("done") == 0 && w.Tick < reloadTick+200 {
		steps(t, w, 1)
	}
	// the run was suspended at step 1 for 60 steps: 61 - unloadTick steps were left,
	// counted from the first step after the reload
	if want := reloadTick + (61 - unloadTick); w.Tick != want || sink.join("done") != "done 2" {
		t.Fatalf("finished at tick %d, want %d: %s", w.Tick, want, sink.all())
	}
	mustNoErrors(t, sink)
}

// ---- sound ----

func soundGame(t *testing.T, script string) (*gp, *Sim) {
	g := newGP(t)
	g.script("a", script)
	g.wav("assets/coin.wav", 800)
	g.wav("assets/theme.wav", 800)
	g.wav("assets/sub/boom.wav", 800)
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	return g, g.sim()
}

func TestPlaySoundFindsFilesLikeImages(t *testing.T) {
	g, sim := soundGame(t, "when start\n  play sound \"coin.wav\"\n  play sound \"coin\"\n  play sound \"assets/sub/boom.wav\"\n  play music \"theme\"\n  play music \"theme.wav\"\n")
	simSteps(t, sim, 1)
	if got := strings.Join(g.audio.Sounds, ","); got != "assets/coin.wav,assets/coin.wav,assets/sub/boom.wav" {
		t.Fatalf("sounds %q", got)
	}
	if strings.Join(g.audio.Music, ",") != "assets/theme.wav" { // the same music is not restarted
		t.Fatalf("music %v", g.audio.Music)
	}
	mustNoErrors(t, g.sink)
}

func TestPlayMusicStopMusicAndOtherMusic(t *testing.T) {
	g, sim := soundGame(t, "when key a pressed\n  play music \"theme\"\nwhen key b pressed\n  stop music\nwhen key c pressed\n  play music \"assets/sub/boom.wav\"\n")
	for _, k := range []string{"a", "a", "b", "a", "c"} {
		g.in.Press(k)
		simSteps(t, sim, 1)
		g.in.Release(k)
		simSteps(t, sim, 1)
	}
	if strings.Join(g.audio.Music, ",") != "assets/theme.wav,assets/theme.wav,assets/sub/boom.wav" || g.audio.MusicNow != "assets/sub/boom.wav" {
		t.Fatalf("music %v now %q", g.audio.Music, g.audio.MusicNow)
	}
	if g.audio.MusicStops != 1 {
		t.Fatalf("stops %d", g.audio.MusicStops)
	}
	mustNoErrors(t, g.sink)
}

func TestPlaySoundErrorsAreTD304AndTheGameGoesOn(t *testing.T) {
	g := newGP(t)
	g.script("a", "when start\n  play sound \"missing.wav\"\nwhen start\n  play sound \"../secret.wav\"\nwhen start\n  play sound \"fake.wav\"\nwhen start\n  play sound \"big.ogg\"\nwhen start\n  play sound \"coin.wav\"\nwhen start\n  play sound \"\"\nwhen start\n  play sound \"pic.png\"\n")
	g.wav("assets/coin.wav", 100)
	write(g.t, g.root, "assets/fake.wav", "this is not a wave file")
	write(g.t, g.root, "assets/big.ogg", "OggS garbage")
	write(g.t, g.root, "assets/pic.png", "x")
	write(g.t, g.root, "secret.wav", string(wavBytes(10)))
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	simSteps(t, sim, 1)
	if n := g.sink.count("TD304"); n != 6 {
		t.Fatalf("%d TD304:\n%s", n, g.sink.all())
	}
	if len(g.audio.Sounds) != 1 {
		t.Fatalf("sounds %v", g.audio.Sounds)
	}
}

func TestPlaySoundDoesNotFollowLinksOrLeaveTheProject(t *testing.T) {
	g := newGP(t)
	g.script("a", "when start\n  play sound \"link.wav\"\nwhen start\n  play sound \"/etc/passwd\"\nwhen start\n  play sound \"assets/dirlink/x.wav\"\n")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "o.wav"), wavBytes(100), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(g.root, "assets"), 0o755)
	if err := os.Symlink(filepath.Join(outside, "o.wav"), filepath.Join(g.root, "assets", "link.wav")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	os.WriteFile(filepath.Join(outside, "x.wav"), wavBytes(100), 0o644)
	os.Symlink(outside, filepath.Join(g.root, "assets", "dirlink"))
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	simSteps(t, sim, 1)
	if len(g.audio.Sounds) != 0 || g.sink.count("TD304") != 3 {
		t.Fatalf("sounds %v\n%s", g.audio.Sounds, g.sink.all())
	}
}

func TestSoundFileSizeLimit(t *testing.T) {
	g := newGP(t)
	g.script("a", "when start\n  play sound \"big.wav\"\n")
	data := wavBytes(100)
	big := make([]byte, MaxSoundBytes+1)
	copy(big, data)
	write(t, g.root, "assets/big.wav", string(big))
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	simSteps(t, sim, 1)
	if g.sink.count("TD304") != 1 || !strings.Contains(g.sink.all(), "32 МБ") || len(g.audio.Sounds) != 0 {
		t.Fatalf("%s", g.sink.all())
	}
}

func TestSimultaneousSoundsAreLimitedTo32(t *testing.T) {
	g, sim := soundGame(t, "when key a pressed\n  repeat 40 times\n    play sound \"coin.wav\"\n")
	g.in.Press("a")
	simSteps(t, sim, 1)
	g.in.Release("a")
	if len(g.audio.Sounds) != MaxSounds || g.audio.Playing() != MaxSounds {
		t.Fatalf("%d sounds, playing %d", len(g.audio.Sounds), g.audio.Playing())
	}
	if g.sink.count("не больше 32") != 1 {
		t.Fatalf("the limit message must come once:\n%s", g.sink.all())
	}
	// when some finish, new ones are accepted again
	g.audio.Finish(5)
	g.in.Press("a")
	simSteps(t, sim, 1)
	if len(g.audio.Sounds) != MaxSounds+5 {
		t.Fatalf("%d sounds", len(g.audio.Sounds))
	}
	mustNoErrors(t, g.sink)
}

func TestSoundCacheIsBounded(t *testing.T) {
	g := newGP(t)
	for i := 0; i < soundCacheEntries+10; i++ {
		g.wav(fmt.Sprintf("assets/s%03d.wav", i), 10)
	}
	s := NewSounds(NewSource(g.root))
	for i := 0; i < soundCacheEntries+10; i++ {
		if _, err := s.Get(fmt.Sprintf("s%03d.wav", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.cache) != soundCacheEntries || len(s.order) != soundCacheEntries {
		t.Fatalf("cache %d", len(s.cache))
	}
	// the failures are remembered, and bounded too
	for i := 0; i < maxFailedSounds+20; i++ {
		s.Get(fmt.Sprintf("none%d.wav", i))
	}
	if len(s.failed) != maxFailedSounds {
		t.Fatalf("failed %d", len(s.failed))
	}
	// a cached clip is the same object (read once)
	a, _ := s.Get(fmt.Sprintf("s%03d.wav", soundCacheEntries+9))
	b, _ := s.Get(fmt.Sprintf("s%03d.wav", soundCacheEntries+9))
	if a != b {
		t.Fatal("not cached")
	}
}

func TestDecodersRejectGarbageWithoutPanicking(t *testing.T) {
	for _, f := range []ClipFormat{FormatWAV, FormatOGG, FormatMP3} {
		for _, data := range [][]byte{nil, []byte("x"), bytes.Repeat([]byte{0xff}, 5000), []byte("RIFF\x00\x00\x00\x00WAVEfmt "), []byte("OggS\x00\x02" + strings.Repeat("\x00", 100))} {
			c := &Clip{Name: "x", Format: f, Data: data}
			if err := checkClip(c); err == nil && f != FormatMP3 {
				t.Errorf("%v accepted %q", f, data)
			}
		}
	}
	// a good WAV passes, and decodes to stereo 16-bit at the asked rate
	c := &Clip{Name: "ok", Format: FormatWAV, Data: wavBytes(800)}
	if err := checkClip(c); err != nil {
		t.Fatal(err)
	}
	st, err := DecodeClip(c, 44100)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if n, _ := buf.ReadFrom(st); n < 800*2*2*44100/8000-8 {
		t.Fatalf("decoded %d bytes", n)
	}
}

func TestDemoCoinSoundDecodes(t *testing.T) {
	root, _ := filepath.Abs("../../examples/demo")
	s := NewSounds(NewSource(root))
	if _, err := s.Get("coin.wav"); err != nil {
		t.Fatal(err)
	}
}

func TestNoAudioDeviceDegradesToASingleLine(t *testing.T) {
	// a device that cannot open: forced by marking it failed the way a failed open does
	var lines []string
	d := NewDeviceAudio(func(m string) { lines = append(lines, m) })
	d.fail(fmt.Errorf("no card")) // what context() does when the device cannot be opened
	c := &Clip{Name: "x", Format: FormatWAV, Data: wavBytes(100)}
	for i := 0; i < 5; i++ {
		d.PlaySound(c)
		d.PlayMusic(c)
	}
	d.StopAll()
	if d.Playing() != 0 || len(lines) != 1 || !strings.Contains(lines[0], "без звука") {
		t.Fatalf("%v", lines)
	}
}

// ---- scenes ----

func sceneGame(t *testing.T) *gp {
	g := newGP(t)
	g.script("main", "shared coins = 0\nshared who = nothing\nvar here = 1\nwhen key a pressed\n  coins += 5\n  who = self\n  go to scene \"level2\"\n  print \"after go\"\nwhen key r pressed\n  coins += 1\n  restart scene\nwhen key u pressed\n  go to scene \"nowhere\"\nwhen key m pressed\n  remember \"k\" as coins\n")
	g.script("l2", "when start\n  print \"l2 coins {coins} who {who}\"\nwhen key b pressed\n  go to scene \"main\"\nwhen key r pressed\n  coins += 100\n  restart scene\nwhen key x pressed\n  go to scene \"bad\"\n")
	g.scene("", 60, 320, 180, sprite("hero", 0, 0, scr("main")))
	g.scene("game/scenes/level2.vide.json", 120, 200, 100, sprite("l2", 0, 0, scr("l2")), sprite("extra", 40, 0, ""))
	return g
}

func TestGoToSceneSwapsTheWorldAtTheEndOfTheFrameKeepingShared(t *testing.T) {
	g := sceneGame(t)
	sim := g.sim()
	w1 := sim.World
	simSteps(t, sim, 2)
	g.in.Press("a")
	simSteps(t, sim, 1)
	g.in.Release("a")
	w2 := sim.World
	if w2 == w1 || w2.ViewW != 200 || w2.ViewH != 100 || w2.TickRate != 120 || w2.Tick != 0 || w2.SceneRel() != "game/scenes/level2.vide.json" {
		t.Fatalf("world not swapped: %+v", w2)
	}
	if sim.Clock.rate != 120 {
		t.Fatal("clock not restarted with the new tick rate")
	}
	if sink := g.sink.join("after go"); sink != "after go" {
		t.Fatalf("the rest of the handler must run (applied at the end of the frame): %q", g.sink.all())
	}
	simSteps(t, sim, 1)
	// shared kept (5), the object stored in a shared variable became nothing
	if got := g.sink.join("l2"); got != "l2 coins 5 who nothing" {
		t.Fatalf("%q\n%s", got, g.sink.all())
	}
	// and back: the main scene starts with its own objects, shared still carried
	g.in.Press("b")
	simSteps(t, sim, 1)
	g.in.Release("b")
	if sim.World.SceneRel() != "project.vide.json" || sim.World.TickRate != 60 || find(sim.World, "extra") != nil {
		t.Fatalf("not back: %s", sim.World.SceneRel())
	}
	if v, _ := sim.World.scripts.eng.Shared("coins"); v.N != 5 {
		t.Fatalf("coins %v", v)
	}
	mustNoErrors(t, g.sink)
}

func TestRestartSceneResetsSharedAndKeepsMemory(t *testing.T) {
	g := sceneGame(t)
	sim := g.sim()
	simSteps(t, sim, 1)
	g.in.Press("m") // remember coins (0)
	simSteps(t, sim, 1)
	g.in.Release("m")
	g.in.Press("a")
	simSteps(t, sim, 1) // go to level2 with coins = 5
	g.in.Release("a")
	g.in.Press("r")
	simSteps(t, sim, 1) // restart level2: coins += 100 first, then reset
	g.in.Release("r")
	w := sim.World
	if w.SceneRel() != "game/scenes/level2.vide.json" || w.Tick != 0 {
		t.Fatalf("restart must restart the current scene: %s tick %d", w.SceneRel(), w.Tick)
	}
	simSteps(t, sim, 1)
	if v, _ := w.scripts.eng.Shared("coins"); v.N != 0 {
		t.Fatalf("shared not reset: %v", v)
	}
	if got := g.sink.lines("l2"); len(got) != 2 || got[1] != "l2 coins 0 who nothing" {
		t.Fatalf("%q", got)
	}
	if v, ok, _ := g.store.Get("k"); !ok || v.N != 0 {
		t.Fatal("memory of the game must survive a restart")
	}
	mustNoErrors(t, g.sink)
}

func TestFirstSceneRequestInAFrameWins(t *testing.T) {
	g := newGP(t)
	g.script("a", "when start\n  go to scene \"two\"\n  go to scene \"three\"\n  restart scene\n")
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	g.scene("game/scenes/two.vide.json", 60, 320, 180)
	g.scene("game/scenes/three.vide.json", 60, 320, 180)
	sim := g.sim()
	simSteps(t, sim, 1)
	if sim.World.SceneRel() != "game/scenes/two.vide.json" {
		t.Fatal(sim.World.SceneRel())
	}
}

func TestUnknownSceneIsTD305(t *testing.T) {
	g := sceneGame(t)
	write(t, g.root, "game/objects/o.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	write(t, g.root, "game/scenes/bad.vide.json", "{ not json")
	g.script("main", "when key u pressed\n  go to scene \"nowhere\"\nwhen key i pressed\n  go to scene \"../project\"\nwhen key o pressed\n  go to scene \"bad\"\n")
	sim := g.sim()
	for _, k := range []string{"u", "i", "o"} {
		g.in.Press(k)
		simSteps(t, sim, 1)
		g.in.Release(k)
		simSteps(t, sim, 1)
	}
	if g.sink.count("TD305") != 3 || !strings.Contains(g.sink.all(), "Есть сцены: bad, level2") {
		t.Fatalf("%s", g.sink.all())
	}
	if sim.World.SceneRel() != "project.vide.json" {
		t.Fatal("moved to an unknown scene")
	}
}

func TestSceneNamesAndTheMainScene(t *testing.T) {
	g := sceneGame(t)
	write(t, g.root, "game/objects/o.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	src := NewSource(g.root)
	src.Main = "project.vide.json"
	for name, want := range map[string]string{"level2": "game/scenes/level2.vide.json", "main": "project.vide.json", "R": "project.vide.json"} {
		if got, err := src.ResolveScene(name); err != nil || got != want {
			t.Errorf("%q: %q %v", name, got, err)
		}
	}
	for _, name := range []string{"", "o", "nope", "a/b", "..", "x\x00", strings.Repeat("a", 200), "level2.vide.json"} {
		_, err := src.ResolveScene(name)
		var he *vm.HostError
		if err == nil || !asHostError(err, &he) || he.Code != "TD305" {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func asHostError(err error, out **vm.HostError) bool {
	he, ok := err.(*vm.HostError)
	*out = he
	return ok
}

func TestSceneChangeDropsWaitingRunsAndMessages(t *testing.T) {
	g := newGP(t)
	g.script("a", "when start\n  wait 1 second\n  print \"old wait ended\"\nwhen key a pressed\n  send \"hello\"\n  go to scene \"two\"\n")
	g.script("b", "when message \"hello\"\n  print \"got hello\"\n")
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	g.scene("game/scenes/two.vide.json", 60, 320, 180, sprite("b", 0, 0, scr("b")))
	sim := g.sim()
	simSteps(t, sim, 3)
	g.in.Press("a")
	simSteps(t, sim, 1)
	g.in.Release("a")
	simSteps(t, sim, 200)
	if g.sink.count("old wait") != 0 || g.sink.count("got hello") != 0 {
		t.Fatalf("state of the old scene leaked:\n%s", g.sink.all())
	}
	if sim.World.scripts.eng.Waiting() != 0 {
		t.Fatal("waiting runs carried over")
	}
}

func TestBrokenTargetSceneKeepsTheOldWorld(t *testing.T) {
	g := newGP(t)
	g.script("a", "when start\n  go to scene \"two\"\n")
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	// a valid document whose map is missing: it resolves, but cannot be built
	write(t, g.root, "game/scenes/two.vide.json", doc("scene", `"map":"game/maps/none.vide.json","view":{"w":320,"h":180},`, ""))
	sim := g.sim()
	w := sim.World
	simSteps(t, sim, 2)
	if sim.World != w || g.sink.count("Не удалось перейти") != 1 {
		t.Fatalf("%s", g.sink.all())
	}
}

func TestSceneSwapIsDeterministic(t *testing.T) {
	run := func() string {
		g := sceneGame(t)
		g.script("l2", "when start\n  print \"n {random(1, 1000)} {coins}\"\n")
		sim := g.sim()
		simSteps(t, sim, 1)
		g.in.Press("a")
		simSteps(t, sim, 1)
		simSteps(t, sim, 2)
		return g.sink.join("n ")
	}
	a, b := run(), run()
	if a == "" || a != b {
		t.Fatalf("%q %q", a, b)
	}
}

func TestMusicGoesOnAcrossGoToSceneAndStopsOnRestart(t *testing.T) {
	g := newGP(t)
	g.script("a", "when key a pressed\n  play music \"theme\"\nwhen key b pressed\n  go to scene \"two\"\nwhen key c pressed\n  restart scene\n")
	g.wav("assets/theme.wav", 100)
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	g.scene("game/scenes/two.vide.json", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	press := func(k string) {
		g.in.Press(k)
		simSteps(t, sim, 1)
		g.in.Release(k)
		simSteps(t, sim, 1)
	}
	press("a")
	press("b")
	if g.audio.MusicNow == "" || g.audio.StopAlls != 0 {
		t.Fatal("music must go on across go to scene")
	}
	press("a") // the same music: not restarted in the new scene
	if len(g.audio.Music) != 1 {
		t.Fatalf("%v", g.audio.Music)
	}
	press("c")
	if g.audio.MusicNow != "" || g.audio.StopAlls != 1 {
		t.Fatal("restart scene must silence the game")
	}
}
