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
		"garbage":    `not json`,
		"no version": `{"scene":{"name":"M","kind":"Node"}}`,
		"future":     `{"version":99,"scene":{"name":"M","kind":"Node"}}`,
		"no scene":   `{"version":1}`,
		"bad kind":   `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Evil"}]}}`,
		"zero size":  `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Sprite","w":0,"h":4}]}}`,
		"bad color":  `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"X","kind":"Sprite","w":4,"h":4,"color":"red"}]}}`,
		"null child": `{"version":1,"scene":{"name":"M","kind":"Node","children":[null]}}`,
		"empty name": `{"version":1,"scene":{"name":"M","kind":"Node","children":[{"name":"","kind":"Node"}]}}`,
		"too deep":   deep(100),
		"truncated":  `{"version":1,"scene":{"name":"M"`,
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
