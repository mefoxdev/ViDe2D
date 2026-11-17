package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustFail(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestLoadDefaultRootIsSceneFolder(t *testing.T) {
	root := t.TempDir()
	write(t, root, "game/objects/o.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	write(t, root, "project.vide.json", doc("scene", "", inst("i", 0, 0, "game/objects/o.vide.json")))
	w, src, err := Load(filepath.Join(root, "project.vide.json"), "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if src.Root != root || len(names(w)) == 0 {
		t.Fatal(src.Root, names(w))
	}
}

// A scene in game/scenes with no explicit root has the scene folder as root:
// the runtime never guesses a folder above it.
func TestLoadNestedSceneNoGuessing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "game/objects/o.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	write(t, root, "project.vide.json", doc("scene", "", ""))
	write(t, root, "game/scenes/l.vide.json", doc("scene", "", inst("i", 0, 0, "game/objects/o.vide.json")))
	file := filepath.Join(root, "game/scenes/l.vide.json")
	_, src, err := Load(file, "", Options{})
	if src != nil || err == nil {
		t.Fatal("the reference must not resolve above the scene folder")
	}
	w, src, err := Load(file, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if src.Root != root || len(names(w)) == 0 {
		t.Fatal(src.Root)
	}
}

func TestLoadSceneOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	write(t, other, "x.vide.json", doc("scene", "", ""))
	_, _, err := Load(filepath.Join(other, "x.vide.json"), root, Options{})
	mustFail(t, err, "outside the project root")
	// root equal to the file itself or a sibling prefix is not "inside"
	write(t, root+"-sibling", "y.vide.json", doc("scene", "", ""))
	t.Cleanup(func() { os.RemoveAll(root + "-sibling") })
	_, _, err = Load(filepath.Join(root+"-sibling", "y.vide.json"), root, Options{})
	mustFail(t, err, "outside the project root")
	_, _, err = Load(filepath.Join(root, "x.vide.json"), filepath.Join(root, "nofolder"), Options{})
	mustFail(t, err, "not a folder")
}

func TestLoadEscapingRefs(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	write(t, parent, "secret.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	for _, ref := range []string{"../secret.vide.json", "/etc/passwd", "game/../../secret.vide.json", "a\\b.vide.json", "./x.vide.json"} {
		write(t, root, "project.vide.json", doc("scene", "", inst("i", 0, 0, ref)))
		w, err := load(t, root, "project.vide.json")
		if err == nil {
			t.Fatalf("ref %q accepted (%v)", ref, names(w))
		}
	}
}

func TestLoadSymlinks(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	write(t, parent, "outside/o.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	write(t, root, "game/objects/real.vide.json", doc("object", "", sprite("s", 0, 0, "")))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Skip("symlinks unavailable:", err)
		}
	}
	// final component is a link, to a file inside the project
	must(os.Symlink("real.vide.json", filepath.Join(root, "game/objects/link.vide.json")))
	// a directory component is a link, to a folder outside
	must(os.Symlink(filepath.Join(parent, "outside"), filepath.Join(root, "game/objects/ext")))
	for _, ref := range []string{"game/objects/link.vide.json", "game/objects/ext/o.vide.json"} {
		write(t, root, "project.vide.json", doc("scene", "", inst("i", 0, 0, ref)))
		_, err := load(t, root, "project.vide.json")
		mustFail(t, err, "symbolic links")
	}
	// the scene file itself, and a folder on the way to it
	must(os.Symlink(filepath.Join(root, "game/objects/real.vide.json"), filepath.Join(root, "scene.vide.json")))
	_, _, err := Load(filepath.Join(root, "scene.vide.json"), root, Options{})
	mustFail(t, err, "symbolic links")
	_, _, err = Load(filepath.Join(root, "game/objects/ext/o.vide.json"), root, Options{})
	mustFail(t, err, "symbolic links")
	// the root itself may be a link: the user chose it
	must(os.Symlink(root, filepath.Join(parent, "rootlink")))
	write(t, root, "project.vide.json", doc("scene", "", inst("i", 0, 0, "game/objects/real.vide.json")))
	if _, _, err := Load(filepath.Join(parent, "rootlink", "project.vide.json"), "", Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestSourceTextureEscapes(t *testing.T) {
	root := t.TempDir()
	src := NewSource(root)
	im := NewImages(src)
	for _, rel := range []string{"../x.png", "/x.png", "a/../../x.png"} {
		if _, err := im.Get(rel); err == nil {
			t.Fatalf("%q accepted", rel)
		}
	}
}
