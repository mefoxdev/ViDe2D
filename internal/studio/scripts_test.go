package studio

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/tdsl"
)

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{"Hero": "Hero", "Big Tree": "Big_Tree", "../x": "x", "": "script", "///": "script", "Игрок 1": "Игрок_1", "a/b\\c": "a_b_c"}
	for in, want := range cases {
		if got := safeFileName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestNewScript(t *testing.T) {
	dir := t.TempDir()
	s := &Studio{}
	s.proj.dir = dir
	sc := scene.New("Main")
	a := sc.Add(nil, scene.KindSprite, "Hero")
	b := sc.Add(nil, scene.KindSprite, "Hero")
	if err := s.newScript(a); err != nil || a.Script != "scripts/Hero.tdsl" {
		t.Fatalf("a: %v %q", err, a.Script)
	}
	if err := s.newScript(b); err != nil || b.Script != "scripts/Hero2.tdsl" {
		t.Fatalf("b: %v %q", err, b.Script)
	}
	data, err := os.ReadFile(filepath.Join(dir, "scripts", "Hero.tdsl"))
	if err != nil || !strings.Contains(string(data), "when start") {
		t.Fatalf("content: %v %q", err, data)
	}
	if !s.scriptExists("scripts/Hero.tdsl") || s.scriptExists("scripts/Nope.tdsl") || s.scriptExists("../x.tdsl") {
		t.Fatal("scriptExists wrong")
	}
	if (&Studio{}).newScript(a) == nil {
		t.Fatal("expected error without a scripts folder")
	}
}

func TestScriptFileRejectsEscape(t *testing.T) {
	s := &Studio{}
	s.proj.dir = t.TempDir()
	for _, bad := range []string{"../a.tdsl", "/etc/passwd", "a.txt", "", "a\\b.tdsl"} {
		if s.scriptFile(bad) != "" {
			t.Errorf("%q accepted", bad)
		}
	}
	if s.scriptFile("ok/a.tdsl") == "" {
		t.Error("valid path rejected")
	}
}

func TestValidTag(t *testing.T) {
	if !validTag("coin") || !validTag("") || validTag("a\x07") || validTag(strings.Repeat("я", 65)) || !validTag(strings.Repeat("я", 64)) {
		t.Fatal("validTag wrong")
	}
}

func TestCodeLayoutRowsFollowDiagnostics(t *testing.T) {
	s := &Studio{}
	r := image.Rect(0, 0, 400, 300)
	if l := s.codeLayout(r); l.rows != 0 || l.problems.Dy() != 0 {
		t.Fatalf("no problems: %+v", l)
	}
	s.code.diag = make([]*tdsl.Error, 9)
	if l := s.codeLayout(r); l.rows != codeMaxRows || l.problems.Dy() != codeMaxRows*codeRowH+6 {
		t.Fatalf("many problems: %+v", l)
	}
}
