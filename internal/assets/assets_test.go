package assets

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, p string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestValidPath(t *testing.T) {
	good := []string{"a.png", "dir/a.png", "A.PNG"}
	bad := []string{"", "../a.png", "/a.png", "a/../b.png", "a\\b.png", "a.txt", "./a.png", "a//b.png", "dir/", "a.png\x00"}
	for _, p := range good {
		if !ValidPath(p) {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range bad {
		if ValidPath(p) {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func TestScanAndDecode(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "hero.png"), 16, 24)
	writePNG(t, filepath.Join(root, "tiles", "grass.png"), 8, 8)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "broken.png"), []byte("not a png"), 0o644)
	writePNG(t, filepath.Join(root, "huge.png"), MaxSide+1, 1)

	list, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0] != (Info{"hero.png", 16, 24}) || list[1] != (Info{"tiles/grass.png", 8, 8}) {
		t.Fatalf("scan: %+v", list)
	}
	img, err := Decode(root, "hero.png")
	if err != nil || img.Bounds().Dx() != 16 {
		t.Fatalf("decode: %v", err)
	}
	for _, bad := range []string{"../x.png", "broken.png", "huge.png", "missing.png", "/etc/passwd"} {
		if _, err := Decode(root, bad); err == nil {
			t.Errorf("Decode(%q) should fail", bad)
		}
	}
}

func TestScanMissingRoot(t *testing.T) {
	list, err := Scan(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(list) != 0 {
		t.Fatalf("%v %v", list, err)
	}
}

func TestSymlinkSkipped(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writePNG(t, filepath.Join(outside, "secret.png"), 4, 4)
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "link.png")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if list, _ := Scan(root); len(list) != 0 {
		t.Fatalf("symlink listed: %+v", list)
	}
	if _, err := Decode(root, "link.png"); err == nil {
		t.Fatal("symlink decoded")
	}
}
