package projfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, file string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileRoot(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, ProjectFile)) // never looked at
	scene := filepath.Join(root, "game", "scenes", "l.vide.json")
	touch(t, scene)
	if got := FileRoot(scene); got != filepath.Dir(scene) {
		t.Fatal(got)
	}
}

func TestNestedHint(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"p", "proj")
	for _, sub := range []string{"scenes", "objects", "maps"} {
		h := NestedHint(filepath.Join(root, "game", sub, "x.vide.json"))
		if !strings.Contains(h, "game/"+sub) || !strings.Contains(h, "-root "+root) || !strings.Contains(h, ProjectFile) {
			t.Fatal(sub, h)
		}
	}
	for _, f := range []string{filepath.Join(root, "project.vide.json"), filepath.Join(root, "game", "x.vide.json"), filepath.Join(root, "mygame", "scenes", "x.vide.json")} {
		if h := NestedHint(f); h != "" {
			t.Fatal(f, h)
		}
	}
}

func TestOpenRoot(t *testing.T) {
	root := t.TempDir()
	scene := filepath.Join(root, "game", "scenes", "l.vide.json")
	touch(t, scene)
	if err := os.MkdirAll(filepath.Join(root, "game", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, rel, err := OpenRoot(scene, root)
	if err != nil || r != root || rel != "game/scenes/l.vide.json" {
		t.Fatal(r, rel, err)
	}
	r, rel, err = OpenRoot(scene, "")
	if err != nil || r != filepath.Dir(scene) || rel != "l.vide.json" {
		t.Fatal(r, rel, err)
	}
	if _, _, err = OpenRoot(scene, filepath.Join(root, "game", "objects")); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatal(err)
	}
	// a ".." that leaves the root through the file path
	if _, _, err = OpenRoot(filepath.Join(root, "game", "..", "..", "x.vide.json"), root); err == nil {
		t.Fatal("escaping scene path accepted")
	}
}

func TestSafeFileAndNoLinks(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "p")
	touch(t, filepath.Join(root, "a", "f.txt"))
	touch(t, filepath.Join(parent, "out", "g.txt"))
	if err := os.Symlink(filepath.Join(parent, "out"), filepath.Join(root, "lnk")); err != nil {
		t.Skip(err)
	}
	if _, err := SafeFile(root, "a/f.txt"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../out/g.txt", "/etc/passwd", "lnk/g.txt", "a", "a/missing", "a//f.txt", "a/./f.txt", ""} {
		if _, err := SafeFile(root, rel); err == nil {
			t.Fatalf("%q accepted", rel)
		}
	}
	if NoLinks(root, "a/new/file") != nil {
		t.Fatal("missing components are fine")
	}
	if NoLinks(root, "lnk/new") == nil || NoLinks(root, "../x") == nil {
		t.Fatal("link / escape accepted")
	}
}

func TestRootOps(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "p")
	touch(t, filepath.Join(root, "a", "f.txt"))
	touch(t, filepath.Join(parent, "out", "g.txt"))
	if err := os.Symlink(filepath.Join(parent, "out"), filepath.Join(root, "lnk")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(filepath.Join(root, "a", "f.txt"), filepath.Join(root, "inner")); err != nil { // even a link that stays inside
		t.Skip(err)
	}
	data, err := ReadFile(root, "a/f.txt", 100)
	if err != nil || string(data) != "{}" {
		t.Fatal(string(data), err)
	}
	if _, err := ReadFile(root, "a/f.txt", 1); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatal("size limit", err)
	}
	for _, rel := range []string{"lnk/g.txt", "inner", "../out/g.txt", "a"} {
		if _, err := ReadFile(root, rel, 100); err == nil {
			t.Fatalf("read %q accepted", rel)
		}
		if _, err := OpenFile(root, rel); err == nil {
			t.Fatalf("open %q accepted", rel)
		}
	}
	if _, err := ReadFile(root, "a/none", 100); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	// writes: new files, atomic replace, nothing through links
	if err := WriteFile(root, "new/dir/x.txt", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(root, "new/dir/x.txt", []byte("22")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "new", "dir", "x.txt")); string(b) != "22" {
		t.Fatal(string(b))
	}
	if des, _ := os.ReadDir(filepath.Join(root, "new", "dir")); len(des) != 1 {
		t.Fatal("temp file left behind", len(des))
	}
	if err := CreateNew(root, "new/dir/x.txt", nil); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	for _, rel := range []string{"lnk/new.txt", "lnk/g.txt", "inner", "../x"} {
		if WriteFile(root, rel, []byte("x")) == nil || CreateNew(root, rel, []byte("x")) == nil {
			t.Fatalf("write %q accepted", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "out", "new.txt")); err == nil {
		t.Fatal("wrote through a link")
	}
	if b, _ := os.ReadFile(filepath.Join(parent, "out", "g.txt")); string(b) != "{}" {
		t.Fatal("link target changed")
	}
}

func TestScanSkipsLinksAndStaysInside(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "p")
	touch(t, filepath.Join(root, "a", "f.tdsl"))
	touch(t, filepath.Join(parent, "out", "g.tdsl"))
	if err := os.Symlink(filepath.Join(parent, "out"), filepath.Join(root, "lnk")); err != nil {
		t.Skip(err)
	}
	top, _ := Scan(root)
	var names []string
	Walk(top, func(e *Entry) { names = append(names, e.Path) })
	if strings.Join(names, ",") != "a,a/f.tdsl" {
		t.Fatal(names)
	}
	if top[0].Children[0].Size != 2 {
		t.Fatal("size", top[0].Children[0].Size)
	}
}
