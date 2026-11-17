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
	if !strings.Contains(string(out), `"version": 4`) {
		t.Fatalf("not written as v4: %s", out)
	}
}

func TestDocTypesRoundTrip(t *testing.T) {
	// object with an instance of another object
	o := scene.New("Coin")
	o.Type = scene.DocObject
	inst := o.Add(nil, scene.KindInstance, "Glow")
	inst.Ref = "game/objects/glow.vide.json"
	data, err := Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(data)
	if err != nil || back.Type != scene.DocObject || back.Root.Children[0].Ref != "game/objects/glow.vide.json" {
		t.Fatalf("object: %v %+v", err, back)
	}

	// map with two chunks and a sprite inside one
	m := scene.New("World")
	m.Type = scene.DocMap
	c := m.Add(nil, scene.KindChunk, "c1")
	c.CX, c.CY = 2, -1
	c.X, c.Y = m.ChunkOrigin(c)
	sp := m.Add(c, scene.KindSprite, "Tree")
	sp.X, sp.Y = 10, 10
	data, _ = Marshal(m)
	back, err = Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	ch := back.Root.Children[0]
	if ch.CX != 2 || ch.CY != -1 || ch.X != 2*64*16 || ch.W != 64*16 {
		t.Fatalf("chunk: %+v", ch)
	}

	// scene with a map
	sc := scene.New("Level")
	sc.Map = "game/maps/world.vide.json"
	data, _ = Marshal(sc)
	back, err = Unmarshal(data)
	if err != nil || back.Map != sc.Map || back.Type != scene.DocScene {
		t.Fatalf("scene: %v %+v", err, back)
	}
}

func TestDocTypeRules(t *testing.T) {
	bad := map[string]string{
		"unknown type":     `{"version":3,"type":"level","scene":{"name":"M","kind":"Node"}}`,
		"object with map":  `{"version":3,"type":"object","map":"game/maps/w.vide.json","scene":{"name":"M","kind":"Node"}}`,
		"map path":         `{"version":3,"type":"scene","map":"../w.vide.json","scene":{"name":"M","kind":"Node"}}`,
		"map wrong folder": `{"version":3,"type":"scene","map":"game/objects/w.vide.json","scene":{"name":"M","kind":"Node"}}`,
		"tile size":        `{"version":3,"tile_size":999,"scene":{"name":"M","kind":"Node"}}`,
		"instance ref":     `{"version":3,"scene":{"name":"M","kind":"Node","children":[{"name":"I","kind":"Instance","ref":"game/scenes/s.vide.json"}]}}`,
		"instance up":      `{"version":3,"scene":{"name":"M","kind":"Node","children":[{"name":"I","kind":"Instance","ref":"game/objects/../x.vide.json"}]}}`,
		"instance kids":    `{"version":3,"scene":{"name":"M","kind":"Node","children":[{"name":"I","kind":"Instance","ref":"game/objects/o.vide.json","children":[{"name":"x","kind":"Node"}]}]}}`,
		"chunk in scene":   `{"version":3,"scene":{"name":"M","kind":"Node","children":[{"name":"c","kind":"Chunk","chunk":[0,0]}]}}`,
		"map needs chunks": `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"n","kind":"Node"}]}}`,
		"duplicate chunks": `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"a","kind":"Chunk","chunk":[1,1]},{"name":"b","kind":"Chunk","chunk":[1,1]}]}}`,
		"chunk coords":     `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"a","kind":"Chunk","chunk":[99999,0]}]}}`,
		"chunk no coords":  `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"a","kind":"Chunk"}]}}`,
		"chunk overflow":   `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"a","kind":"Chunk","chunk":[0,0],"children":[{"name":"S","kind":"Sprite","x":1020,"y":0,"w":16,"h":16}]}]}}`,
		"chunk negative":   `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"a","kind":"Chunk","chunk":[0,0],"children":[{"name":"S","kind":"Sprite","x":-1,"y":0,"w":16,"h":16}]}]}}`,
	}
	for name, in := range bad {
		if _, err := Unmarshal([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	good := `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"a","kind":"Chunk","chunk":[0,0],"children":[{"name":"S","kind":"Sprite","x":1008,"y":1008,"w":16,"h":16}]}]}}`
	if _, err := Unmarshal([]byte(good)); err != nil {
		t.Fatalf("sprite exactly filling the chunk corner must be accepted: %v", err)
	}
}

func TestMapOptimization(t *testing.T) {
	free := `{"version":3,"type":"map","optimization":false,"scene":{"name":"M","kind":"Node","children":[
	  {"name":"Hill","kind":"Sprite","x":5000,"y":-300,"w":4000,"h":900},
	  {"name":"c","kind":"Chunk","chunk":[0,0],"children":[{"name":"S","kind":"Sprite","x":1020,"y":0,"w":16,"h":16}]}]}}`
	s, err := Unmarshal([]byte(free))
	if err != nil {
		t.Fatalf("a non-optimized map is free-form: %v", err)
	}
	if s.Optimization || s.Status() != "" {
		t.Fatalf("flag/status: %v %q", s.Optimization, s.Status())
	}
	// the same content is rejected when the map is optimized
	opt := strings.Replace(free, `"optimization":false`, `"optimization":true`, 1)
	if _, err := Unmarshal([]byte(opt)); err == nil {
		t.Fatal("an optimized map must be cut into chunks")
	}
	// a map file without the parameter is optimized
	legacy := `{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"c","kind":"Chunk","chunk":[0,0]}]}}`
	m, err := Unmarshal([]byte(legacy))
	if err != nil || !m.Optimization || m.Status() != scene.StatusOptimized {
		t.Fatalf("legacy map: %v %+v", err, m)
	}
	// round trip keeps the parameter, and only maps may have it
	data, _ := Marshal(s)
	if !strings.Contains(string(data), `"optimization": false`) {
		t.Fatalf("parameter not written: %s", data)
	}
	if _, err := Unmarshal([]byte(`{"version":3,"type":"object","optimization":true,"scene":{"name":"M","kind":"Node"}}`)); err == nil {
		t.Fatal("objects have no optimization parameter")
	}
	if _, err := Unmarshal([]byte(`{"version":3,"type":"map","scene":{"name":"M","kind":"Node","children":[{"name":"n","kind":"Node","children":[{"name":"c","kind":"Chunk","chunk":[0,0]}]}]},"optimization":false}`)); err == nil {
		t.Fatal("chunks must sit directly in the map")
	}
}

func TestStatusOptimized(t *testing.T) {
	m := scene.New("W")
	m.Type, m.Optimization = scene.DocMap, true
	c := m.Add(nil, scene.KindChunk, "c")
	if m.Status() != scene.StatusOptimized {
		t.Fatal("empty chunk map should be optimized")
	}
	sp := m.Add(c, scene.KindSprite, "S")
	sp.X = m.ChunkSize()
	if m.Status() != "" {
		t.Fatal("overflowing content must lose the status")
	}
	sp.X = 0
	m.Add(nil, scene.KindNode, "loose")
	if m.Status() != "" {
		t.Fatal("loose objects outside chunks must lose the status")
	}
}

func TestSceneViewAndTickRate(t *testing.T) {
	s := scene.New("S")
	if s.ViewW != 320 || s.ViewH != 180 || s.TickRate != 60 {
		t.Fatalf("bad defaults: %dx%d @%d", s.ViewW, s.ViewH, s.TickRate)
	}
	s.ViewW, s.ViewH, s.TickRate = 640, 360, 120
	data, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewW != 640 || got.ViewH != 360 || got.TickRate != 120 {
		t.Fatalf("round trip lost view/tick_rate: %dx%d @%d", got.ViewW, got.ViewH, got.TickRate)
	}
	for _, bad := range []string{
		`{"version":4,"tick_rate":75,"scene":{"name":"S","kind":"Node"}}`,
		`{"version":4,"view":{"w":0,"h":10},"scene":{"name":"S","kind":"Node"}}`,
		`{"version":4,"type":"object","tick_rate":60,"scene":{"name":"S","kind":"Node"}}`,
	} {
		if _, err := Unmarshal([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	old, err := Unmarshal([]byte(`{"version":3,"scene":{"name":"S","kind":"Node"}}`))
	if err != nil || old.ViewW != 320 || old.TickRate != 60 {
		t.Fatalf("v3 file should get defaults: %v", err)
	}
}
