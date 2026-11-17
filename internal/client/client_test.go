package client

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/project"
	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
)

func writeScene(t *testing.T, path, name string) {
	t.Helper()
	data, err := project.Marshal(scene.New(name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	d := t.TempDir()
	writeScene(t, filepath.Join(d, "b", ProjectFile), "Zebra Run")
	writeScene(t, filepath.Join(d, "a", "game", "scenes", "main.vide.json"), "alpha")
	writeScene(t, filepath.Join(d, "loose.vide.json"), "Loose")
	os.MkdirAll(filepath.Join(d, "empty"), 0o755)
	os.WriteFile(filepath.Join(d, "notes.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(d, "bad"), 0o755)
	os.WriteFile(filepath.Join(d, "bad", ProjectFile), []byte("{broken"), 0o644)
	os.WriteFile(filepath.Join(d, "empty2.vide.json"), nil, 0o644)

	var names []string
	got := Scan(d)
	for _, e := range got {
		names = append(names, e.Name)
	}
	want := []string{"alpha", "bad", "empty2", "Loose", "Zebra Run"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names %v, want %v", names, want)
	}
	for _, e := range got {
		broken := e.Name == "bad" || e.Name == "empty2"
		if (e.Err != "") != broken {
			t.Errorf("%s: Err=%q", e.Name, e.Err)
		}
	}
	if got[0].Root != filepath.Join(d, "a") || got[3].Root != "" {
		t.Errorf("roots: %q %q", got[0].Root, got[3].Root)
	}
}

func TestScanMissingAndSymlink(t *testing.T) {
	if Scan(filepath.Join(t.TempDir(), "nope")) != nil {
		t.Error("missing dir should give no games")
	}
	d, outside := t.TempDir(), t.TempDir()
	writeScene(t, filepath.Join(outside, ProjectFile), "Outside")
	if err := os.Symlink(outside, filepath.Join(d, "link")); err != nil {
		t.Skip("no symlinks")
	}
	if g := Scan(d); len(g) != 0 {
		t.Errorf("symlinked folder was followed: %v", g)
	}
}

func TestFindRuntime(t *testing.T) {
	d := t.TempDir()
	self := func() (string, error) { return filepath.Join(d, "vide-client"), nil }
	none := func(string) (string, error) { return "", errors.New("no") }
	inPath := func(n string) (string, error) { return "/usr/bin/" + n, nil }

	if _, err := FindRuntime(self, none, "linux"); err == nil || !strings.Contains(err.Error(), "vide-run") {
		t.Errorf("want not-found error, got %v", err)
	}
	if p, _ := FindRuntime(self, inPath, "windows"); p != "/usr/bin/vide-run.exe" {
		t.Errorf("PATH fallback: %q", p)
	}
	os.WriteFile(filepath.Join(d, "vide-run"), []byte("x"), 0o755)
	if p, _ := FindRuntime(self, inPath, "linux"); p != filepath.Join(d, "vide-run") {
		t.Errorf("neighbour should win: %q", p)
	}
}

func TestProcessLauncher(t *testing.T) {
	var cmd *exec.Cmd
	p := &ProcessLauncher{Exe: "/x/vide-run", Start: func(c *exec.Cmd) error { cmd = c; return nil }}
	g, err := p.Launch(Entry{File: "/g/project.vide.json", Root: "/g"})
	if err != nil || g != nil {
		t.Fatalf("game=%v err=%v", g, err)
	}
	if want := []string{"/x/vide-run", "-root", "/g", "--", "/g/project.vide.json"}; !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args %v", cmd.Args)
	}
	if _, err := p.Launch(Entry{Err: "broken"}); err == nil {
		t.Error("broken game must not start")
	}
	p.Start = func(*exec.Cmd) error { return errors.New("boom") }
	if _, err := p.Launch(Entry{File: "f"}); err == nil {
		t.Error("start failure must be reported")
	}
}

func TestForPlatform(t *testing.T) {
	for goos, wantProc := range map[string]bool{"linux": true, "windows": true, "darwin": true, "android": false, "ios": false} {
		_, isProc := ForPlatform(goos).(*ProcessLauncher)
		if isProc != wantProc {
			t.Errorf("%s: process launcher=%v", goos, isProc)
		}
	}
}

func TestInProcessLauncher(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, ProjectFile)
	writeScene(t, f, "Game")
	g, err := InProcessLauncher{}.Launch(Entry{File: f, Root: d})
	if err != nil || g == nil {
		t.Fatalf("game=%v err=%v", g, err)
	}
	os.WriteFile(f, []byte("{broken"), 0o644)
	if _, err := (InProcessLauncher{}).Launch(Entry{File: f, Root: d}); err == nil {
		t.Error("corrupt file must fail")
	}
}

func TestScanSymlinkedScenesAndMain(t *testing.T) {
	d, outside := t.TempDir(), t.TempDir()
	writeScene(t, filepath.Join(outside, "main.vide.json"), "Outside")
	// game/scenes is a link to a folder elsewhere
	os.MkdirAll(filepath.Join(d, "a", "game"), 0o755)
	if err := os.Symlink(outside, filepath.Join(d, "a", "game", "scenes")); err != nil {
		t.Skip("no symlinks")
	}
	// the whole game/ folder is a link
	os.MkdirAll(filepath.Join(d, "b"), 0o755)
	os.Symlink(outside, filepath.Join(d, "b", "game"))
	// project.vide.json is a link
	os.MkdirAll(filepath.Join(d, "c"), 0o755)
	os.Symlink(filepath.Join(outside, "main.vide.json"), filepath.Join(d, "c", ProjectFile))
	// a loose scene that is a link
	os.Symlink(filepath.Join(outside, "main.vide.json"), filepath.Join(d, "l.vide.json"))
	if g := Scan(d); len(g) != 0 {
		t.Errorf("links were followed: %v", g)
	}
}

func TestScanOversizeDoc(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "big", ProjectFile)
	os.MkdirAll(filepath.Dir(f), 0o755)
	fh, err := os.Create(f)
	if err != nil {
		t.Fatal(err)
	}
	fh.Truncate(projfs.MaxDocBytes + 1) // sparse: nothing is written
	fh.Close()
	g := Scan(d)
	if len(g) != 1 || g[0].Name != "big" || !strings.Contains(g[0].Err, "larger") {
		t.Fatalf("%+v", g)
	}
	if _, err := (&ProcessLauncher{Exe: "x", Start: func(*exec.Cmd) error { return nil }}).Launch(g[0]); err == nil {
		t.Error("oversize game must not start")
	}
}

func TestScanCapsGames(t *testing.T) {
	d := t.TempDir()
	for i := 0; i < maxGames+20; i++ {
		os.WriteFile(filepath.Join(d, fmt.Sprintf("g%04d.vide.json", i)), []byte("{}"), 0o644)
	}
	if g := Scan(d); len(g) != maxGames {
		t.Fatal(len(g))
	}
}
