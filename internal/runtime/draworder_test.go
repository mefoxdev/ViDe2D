package runtime

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestDrawListOrdersByLayerThenSceneOrder(t *testing.T) {
	p := &proj{
		scripts: map[string]string{"s": "when start\n  layer = 5\n"},
		specs: []spec{
			{name: "a", w: 4, h: 4},
			{name: "b", w: 4, h: 4, script: "s"}, // layer 5
			{name: "grp", node: true, kids: []spec{{name: "c", w: 4, h: 4}, {name: "d", w: 4, h: 4}}},
			{name: "e", w: 4, h: 4},
			{name: "hidden", w: 4, h: 4},
		},
	}
	w, _, sink := p.load(t)
	steps(t, w, 1)
	set := func(name string, layer float64) {
		find(w, name).Layer = layer
	}
	order := func() string {
		var out []string
		for _, e := range w.DrawList(nil) {
			out = append(out, e.Name)
		}
		return strings.Join(out, " ")
	}
	find(w, "hidden").Visible = false
	// b is on layer 5, everybody else on 0: scene order for the rest; the group (a Node) is not drawn itself
	if got := order(); got != "a c d e b" {
		t.Fatalf("got %q", got)
	}
	set("a", 10)
	set("d", -1)
	set("e", 5) // same layer as b: scene order, b first
	if got := order(); got != "d c b e a" {
		t.Fatalf("got %q", got)
	}
	// the layer is global: a child of a group is not bound to its group's place
	set("c", 7)
	if got := order(); got != "d b e c a" {
		t.Fatalf("got %q", got)
	}
	mustNoErrors(t, sink)
}

func TestDrawListIsStableForManyEqualLayers(t *testing.T) {
	var specs []spec
	for i := 0; i < 300; i++ {
		specs = append(specs, spec{name: fmt.Sprintf("o%03d", i), w: 2, h: 2})
	}
	w, _, _ := (&proj{specs: specs}).load(t)
	find(w, "o150").Layer = -1
	find(w, "o010").Layer = 1
	list := w.DrawList(nil)
	if len(list) != 300 || list[0].Name != "o150" || list[299].Name != "o010" {
		t.Fatalf("first %s last %s", list[0].Name, list[299].Name)
	}
	for i, k := 1, 0; i < 299; i++ {
		if k == 150 || k == 10 {
			k++
		}
		if k == 150 || k == 10 {
			k++
		}
		if list[i].Name != fmt.Sprintf("o%03d", k) {
			t.Fatalf("position %d: %s, want o%03d", i, list[i].Name, k)
		}
		k++
	}
}

func TestDrawGeoRotatesAroundTheCenter(t *testing.T) {
	// a 10x6 box at (100, 50) with a 1x1 source image
	g := drawGeo(1, 1, 10, 6, 100, 50, 0)
	if x, y := g.Apply(0, 0); x != 100 || y != 50 {
		t.Fatalf("no angle: %v,%v", x, y)
	}
	if x, y := g.Apply(1, 1); x != 110 || y != 56 {
		t.Fatalf("no angle: %v,%v", x, y)
	}
	for _, angle := range []float64{90, 180, -90, 450, 37.5} {
		g := drawGeo(1, 1, 10, 6, 100, 50, angle)
		// the center of the box stays where it was
		cx, cy := g.Apply(0.5, 0.5)
		if math.Abs(cx-105) > 1e-9 || math.Abs(cy-53) > 1e-9 {
			t.Fatalf("angle %v moves the center to %v,%v", angle, cx, cy)
		}
	}
	// 90 degrees is clockwise on the screen (y points down): the top-left corner goes to the top-right
	g = drawGeo(1, 1, 10, 6, 100, 50, 90)
	x, y := g.Apply(0, 0)
	if math.Abs(x-108) > 1e-9 || math.Abs(y-48) > 1e-9 {
		t.Fatalf("90 degrees: top-left corner at %v,%v, want 108,48", x, y)
	}
	// 360 is no turn at all, exactly
	g = drawGeo(1, 1, 10, 6, 100, 50, 360)
	if x, y := g.Apply(1, 1); x != 110 || y != 56 {
		t.Fatalf("360: %v,%v", x, y)
	}
}

func TestDrawGeoFlipMirrorsInsideTheBox(t *testing.T) {
	g := drawGeoFlip(1, 1, 10, 6, 100, 50, 0, true)
	x0, y0 := g.Apply(0, 0) // the image's left edge lands on the box's right edge
	x1, _ := g.Apply(1, 1)
	if x0 != 110 || y0 != 50 || x1 != 100 {
		t.Fatalf("flip: left edge at %v,%v and right edge at %v", x0, y0, x1)
	}
}
