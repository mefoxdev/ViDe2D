package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/ebiten/v2"
)

// recStore is a Store that records how it was finished.
type recStore struct {
	*MemStore
	syncs, closes int
}

func (r *recStore) Sync() error  { r.syncs++; return nil }
func (r *recStore) Close() error { r.closes++; return nil }

func TestGameCloseSavesAndSilences(t *testing.T) {
	g := newGP(t)
	rs := &recStore{MemStore: NewMemStore()}
	g.store = rs
	g.script("a", "when key a pressed\n  play music \"theme\"\n  remember \"k\" as 1\n")
	g.wav("assets/theme.wav", 100)
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	g.in.Press("a")
	simSteps(t, sim, 2)
	game := NewGame(sim.World, nil)
	if err := game.Close(); err != nil {
		t.Fatal(err)
	}
	if rs.syncs != 1 || rs.closes != 1 || g.audio.MusicNow != "" || g.audio.StopAlls != 1 {
		t.Fatalf("syncs %d closes %d music %q stopAlls %d", rs.syncs, rs.closes, g.audio.MusicNow, g.audio.StopAlls)
	}
	if err := game.Close(); err != nil { // twice is harmless
		t.Fatal(err)
	}
}

func TestGameCloseWritesTheSaveFile(t *testing.T) {
	g := newGP(t)
	saves := t.TempDir()
	g.store = NewFileStore(saves, g.root, nil)
	g.script("a", "when start\n  remember \"k\" as 7\n")
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	simSteps(t, sim, 2) // less than a second: nothing was saved by the timer
	if err := NewGame(sim.World, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if NewFileStore(saves, g.root, nil).Len() != 1 {
		t.Fatal("Close must leave the memory saved")
	}
	if _, err := os.Stat(filepath.Join(saves)); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceContextIsOneForTheWholeProcess(t *testing.T) {
	old, oldCtx := openContext, device.ctx
	defer func() { openContext, device.ctx = old, oldCtx }()
	device.ctx = nil
	opened := 0
	openContext = func() (*oto.Context, error) { opened++; return &oto.Context{}, nil }
	a, b := NewDeviceAudio(nil), NewDeviceAudio(nil)
	c1, c2 := a.context(), b.context()
	if c1 == nil || c1 != c2 || opened != 1 {
		t.Fatalf("opened %d times, contexts %p %p", opened, c1, c2)
	}
	if a.context() != c1 || opened != 1 {
		t.Fatal("reopened")
	}
	// a failure is not cached for the process: a later game may find a device
	device.ctx = nil
	openContext = func() (*oto.Context, error) { opened++; return nil, os.ErrNotExist }
	var lines []string
	d := NewDeviceAudio(func(m string) { lines = append(lines, m) })
	if d.context() != nil || len(lines) != 1 {
		t.Fatal("a missing device must log once")
	}
	openContext = func() (*oto.Context, error) { return &oto.Context{}, nil }
	if NewDeviceAudio(nil).context() == nil {
		t.Fatal("a new DeviceAudio must try again")
	}
}

var _ ebiten.Game = (*Game)(nil)

func TestMusicIsRecordedOnlyWhenTheDeviceStartedIt(t *testing.T) {
	g := newGP(t)
	g.audio.FailMusic = 1
	g.script("a", "when key a pressed\n  play music \"theme\"\n")
	g.wav("assets/theme.wav", 100)
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	press := func() {
		g.in.Press("a")
		simSteps(t, sim, 1)
		g.in.Release("a")
		simSteps(t, sim, 1)
	}
	press() // the device refuses
	if g.audio.MusicNow != "" {
		t.Fatal("no music expected")
	}
	press() // the same call tries again
	if g.audio.MusicNow != "assets/theme.wav" {
		t.Fatalf("a failed start must not count as playing: now %q", g.audio.MusicNow)
	}
	// a new device (SetAudio) plays nothing yet, so the same music starts again
	fa := &FakeAudio{}
	sim.World.SetAudio(fa)
	press()
	if fa.MusicNow != "assets/theme.wav" {
		t.Fatal("music must restart on a new audio device")
	}
}

func TestFailedSoundsAreLookedUpAgainAfterAWhile(t *testing.T) {
	g := newGP(t)
	g.scene("", 60, 320, 180)
	s := NewSounds(NewSource(g.root))
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	if _, err := s.Get("late.wav"); err == nil {
		t.Fatal("missing file must fail")
	}
	g.wav("assets/late.wav", 50) // the file appears
	if _, err := s.Get("late.wav"); err == nil {
		t.Fatal("the failure is remembered for a while")
	}
	clock = clock.Add(failedSoundTTL + time.Second)
	if _, err := s.Get("late.wav"); err != nil {
		t.Fatalf("after the TTL the file must be looked up again: %v", err)
	}
	if len(s.failed) != 0 || len(s.fOrder) != 0 {
		t.Fatal("the failure record must be gone")
	}
}
