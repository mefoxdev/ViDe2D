package projfs

import (
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]Kind{
		"hero.tdsl": KindCode, "HERO.TDSL": KindCode, "a.png": KindImage, "a.PNG": KindImage,
		"x.wav": KindSound, "x.ogg": KindSound, "x.mp3": KindSound,
		"level1.vide.json": KindScene, "project.vide.json": KindScene,
		"notes.txt": KindOther, "data.json": KindOther, "noext": KindOther, "a.jpg": KindOther,
	}
	for name, want := range cases {
		if got := Classify(name); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestScanTreeOrderAndFilters(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "b.png", "A.tdsl", "assets/z.wav", "assets/sub/hero.png", "scenes/l1.vide.json", ".git/config", ".hidden", "readme.txt")
	top, trunc := Scan(root)
	if trunc {
		t.Fatal("unexpected truncation")
	}
	var order []string
	Walk(top, func(e *Entry) { order = append(order, e.Path) })
	want := []string{"assets", "assets/sub", "assets/sub/hero.png", "assets/z.wav", "scenes", "scenes/l1.vide.json", "A.tdsl", "b.png", "readme.txt"}
	if len(order) != len(want) {
		t.Fatalf("order %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v want %v", order, want)
		}
	}
	if n := len(Files(top)); n != 5 { // A.tdsl b.png hero.png z.wav l1.vide.json
		t.Fatalf("usable files: %d", n)
	}
	if n := len(Files(top, KindImage)); n != 2 {
		t.Fatalf("images: %d", n)
	}
}

func TestScanSkipsSymlinksAndMissing(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	mk(t, other, "secret.png")
	if err := os.Symlink(filepath.Join(other, "secret.png"), filepath.Join(root, "link.png")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink(other, filepath.Join(root, "dirlink")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if top, _ := Scan(root); len(top) != 0 {
		t.Fatalf("symlinks listed: %v", top[0].Path)
	}
	if top, _ := Scan(filepath.Join(root, "nope")); len(top) != 0 {
		t.Fatal("missing root should be empty")
	}
	if top, _ := Scan(""); top != nil {
		t.Fatal("empty root should give nil")
	}
}

func TestScanLimits(t *testing.T) {
	root := t.TempDir()
	deep := ""
	for i := 0; i < MaxDepth+3; i++ {
		deep += "d/"
	}
	mk(t, root, deep+"f.png")
	_, trunc := Scan(root)
	if !trunc {
		t.Fatal("deep tree should be reported as truncated")
	}
}

func TestValidRelAndResolve(t *testing.T) {
	good := []string{"a.png", "assets/a.png", "a b/c.tdsl"}
	bad := []string{"", "../a", "/a", "a/../b", "a\\b", "./a", "a//b", "a/", "a\x00"}
	for _, p := range good {
		if !ValidRel(p) {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range bad {
		if ValidRel(p) {
			t.Errorf("%q should be invalid", p)
		}
	}
	root := t.TempDir()
	if Resolve(root, "../x") != "" || Resolve("", "a") != "" || Resolve(root, "a/b") != filepath.Join(root, "a", "b") {
		t.Fatal("Resolve wrong")
	}
}

func TestClassifyPath(t *testing.T) {
	cases := map[string]Kind{
		"game/objects/coin.vide.json":     KindObject,
		"game/objects/sub/coin.vide.json": KindObject,
		"game/maps/world.vide.json":       KindMap,
		"game/scenes/main.vide.json":      KindScene,
		"project.vide.json":               KindScene,
		"other/objects/x.vide.json":       KindScene,
		"game/objects/readme.txt":         KindOther,
		"game/objects/a.png":              KindImage,
	}
	for p, want := range cases {
		if got := ClassifyPath(p); got != want {
			t.Errorf("%s: got %v want %v", p, got, want)
		}
	}
}
