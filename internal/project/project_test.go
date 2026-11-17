package project

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/scene"
)

func sample() *scene.Scene {
	s := scene.New("Main")
	a := s.Add(nil, scene.KindNode, "Group")
	a.X, a.Y = 5, -7
	h := s.Add(a, scene.KindSprite, "Hero")
	h.X, h.Y, h.W, h.H = 1, 2, 16, 24
	h.Color.R, h.Color.G, h.Color.B = 0x12, 0xab, 0xff
	h.Texture = "chars/hero.png"
	s.ImageMode = scene.ImageNormal
	h.Tag, h.Script = "player", "hero.tdsl"
	s.Root.Script = "game.tdsl"
	tpl := s.Add(nil, scene.KindSprite, "Star")
	tpl.Template, tpl.Tag = true, "danger"
	lbl := s.Add(nil, scene.KindText, "Score")
	lbl.Text, lbl.FontSize = "Score: {score}", 12
	return s
}

func TestRoundTrip(t *testing.T) {
	data, err := Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Marshal(s)
	if string(again) != string(data) {
		t.Fatalf("round trip differs:\n%s\n%s", data, again)
	}
	h := s.Root.Children[0].Children[0]
	if s.ImageMode != scene.ImageNormal || h.Texture != "chars/hero.png" {
		t.Fatalf("texture/mode lost: %+v %q", h, s.ImageMode)
	}
	if h.Tag != "player" || h.Script != "hero.tdsl" || s.Root.Script != "game.tdsl" {
		t.Fatalf("meta lost: %+v", h)
	}
	var star, score *scene.Node
	s.Root.Walk(func(n *scene.Node, _ int) {
		switch n.Name {
		case "Star":
			star = n
		case "Score":
			score = n
		}
	})
	if star == nil || !star.Template || score == nil || score.Text != "Score: {score}" || score.FontSize != 12 || score.W != 100 {
		t.Fatalf("template/text lost: %+v %+v", star, score)
	}
	if h.Name != "Hero" || h.W != 16 || h.Color.G != 0xab {
		t.Fatalf("bad hero: %+v", h)
	}
}

func TestSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.vide.json")
	data, _ := Marshal(sample())
	if err := Save(p, data); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
}

func TestBadFiles(t *testing.T) {
	cases := map[string]string{
		"garbage":       `not json`,
		"no version":    `{"scene":{"name":"M","kind":"Node"}}`,
		"future":        `{"version":99,"scene":{"name":"M","kind":"Node"}}`,
		"no scene":      `{"version":1}`,
		"bad kind":      `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Evil"}]}}`,
		"zero size":     `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Sprite","w":0,"h":4}]}}`,
		"bad color":     `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Sprite","w":4,"h":4,"color":"red"}]}}`,
		"null child":    `{"version":1,"scene":{"name":"M","kind":"Node","children":[null]}}`,
		"empty name":    `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"","kind":"Node"}]}}`,
		"image mode":    `{"version":1,"image_mode":"blurry","scene":{"name":"M","kind":"Node"}}`,
		"tex abs":       `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Sprite","w":4,"h":4,"texture":"/etc/passwd"}]}}`,
		"tex up":        `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Sprite","w":4,"h":4,"texture":"../secret.png"}]}}`,
		"tex backslash": `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Sprite","w":4,"h":4,"texture":"a\\b.png"}]}}`,
		"script bad":    `{"version":1,"scene":{"name":"M","kind":"Node","script":"../x.tdsl"}}`,
		"script ext":    `{"version":1,"scene":{"name":"M","kind":"Node","script":"x.png"}}`,
		"root template": `{"version":1,"scene":{"name":"M","kind":"Node","template":true}}`,
		"tag long":      `{"version":1,"scene":{"name":"M","kind":"Node","tag":"` + strings.Repeat("a", 65) + `"}}`,
		"tag control":   `{"version":1,"scene":{"name":"M","kind":"Node","tag":"a\u0007b"}}`,
		"font size":     `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"T","kind":"Text","w":5,"h":5,"font_size":999}]}}`,
		"text size":     `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"T","kind":"Text","w":0,"h":5}]}}`,
		"text texture":  `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"T","kind":"Text","w":5,"h":5,"texture":"a.png"}]}}`,
		"too deep":      deep(100),
		"truncated":     `{"version":1,"scene":{"name":"M"`,
	}
	for name, in := range cases {
		if _, err := Unmarshal([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func deep(n int) string {
	var b strings.Builder
	b.WriteString(`{"version":1,"scene":`)
	for i := 0; i < n; i++ {
		b.WriteString(`{"name":"N","kind":"Node","children":[`)
	}
	b.WriteString(`{"name":"L","kind":"Node"}`)
	for i := 0; i < n; i++ {
		b.WriteString(`]}`)
	}
	b.WriteString(`}`)
	return b.String()
}

func TestUpgradeFromVersion1(t *testing.T) {
	in := `{"version":1,"scene":{"name":"M","kind":"Node","script":"game.tdsl","children":[
	  {"name":"H","kind":"Sprite","w":4,"h":4,"texture":"hero.png","script":"hero.tdsl"}]}}`
	s, err := Unmarshal([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Root.Children[0]
	if h.Texture != "assets/hero.png" || h.Script != "scripts/hero.tdsl" || s.Root.Script != "scripts/game.tdsl" {
		t.Fatalf("not upgraded: %+v root=%q", h, s.Root.Script)
	}
	out, _ := Marshal(s)
	if !strings.Contains(string(out), `"version": 2`) {
		t.Fatalf("not written as v2: %s", out)
	}
}
