package dock

import (
	"image"
	"reflect"
	"testing"
)

func def() *Node {
	return &Node{Dir: SplitV, Ratio: .8,
		A: &Node{Dir: SplitH, Ratio: .2, A: Leaf("scene"),
			B: &Node{Dir: SplitH, Ratio: .8, A: Leaf("viewport"), B: Leaf("inspector")}},
		B: Leaf("console")}
}

var all = []PanelID{"scene", "viewport", "inspector", "console"}

func sameSet(t *testing.T, n *Node) {
	t.Helper()
	got := map[PanelID]int{}
	for _, p := range n.AllPanels() {
		got[p]++
	}
	for _, p := range all {
		if got[p] != 1 {
			t.Fatalf("panel %s present %d times: %v", p, got[p], n.AllPanels())
		}
	}
}

func TestRemoveCollapses(t *testing.T) {
	r := def()
	if !r.Remove("console") {
		t.Fatal("remove failed")
	}
	if r.Dir != SplitH || r.FindLeaf("console") != nil {
		t.Fatalf("bad collapse: %+v", r)
	}
}

func TestDockCenterTabs(t *testing.T) {
	r := def()
	if !r.Dock("console", "inspector", ZoneCenter) {
		t.Fatal()
	}
	sameSet(t, r)
	l := r.FindLeaf("inspector")
	if !reflect.DeepEqual(l.Panels, []PanelID{"inspector", "console"}) || l.Active != 1 {
		t.Fatalf("%+v", l)
	}
}

func TestDockEdges(t *testing.T) {
	for _, z := range []Zone{ZoneLeft, ZoneRight, ZoneTop, ZoneBottom} {
		r := def()
		if !r.Dock("console", "viewport", z) {
			t.Fatal(z)
		}
		sameSet(t, r)
		// console and viewport must now be siblings
		c, v := r.FindLeaf("console"), r.FindLeaf("viewport")
		p := r.parentOf(c)
		if p == nil || !((p.A == c && p.B == v) || (p.A == v && p.B == c)) {
			t.Fatalf("zone %d: not siblings", z)
		}
	}
}

func TestDockNoops(t *testing.T) {
	r := def()
	if r.Dock("scene", "scene", ZoneCenter) || r.Dock("scene", "nope", ZoneLeft) || r.Dock("nope", "scene", ZoneLeft) || r.Dock("scene", "console", ZoneNone) {
		t.Fatal("expected no-op")
	}
	sameSet(t, r)
}

func TestDockWithinGroup(t *testing.T) {
	r := def()
	r.Dock("console", "inspector", ZoneCenter)
	// pull console out of the tab group to the bottom of the viewport
	if !r.Dock("console", "inspector", ZoneBottom) {
		t.Fatal()
	}
	sameSet(t, r)
}

func TestZoneAt(t *testing.T) {
	r := image.Rect(0, 0, 400, 326)
	cases := []struct {
		p image.Point
		z Zone
	}{
		{image.Pt(200, 10), ZoneCenter},  // header
		{image.Pt(200, 190), ZoneCenter}, // middle
		{image.Pt(10, 190), ZoneLeft},
		{image.Pt(390, 190), ZoneRight},
		{image.Pt(200, 40), ZoneTop},
		{image.Pt(200, 320), ZoneBottom},
		{image.Pt(500, 5), ZoneNone},
	}
	for _, c := range cases {
		if z := ZoneAt(r, c.p); z != c.z {
			t.Errorf("%v: got %d want %d", c.p, z, c.z)
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	r := def()
	r.Dock("console", "inspector", ZoneCenter)
	data, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(data, all)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, r) {
		t.Fatalf("round trip differs")
	}
}

func TestUnmarshalRejects(t *testing.T) {
	bad := map[string]string{
		"garbage":   `x`,
		"missing":   `{"panels":["scene","viewport","inspector"]}`,
		"duplicate": `{"panels":["scene","viewport","inspector","console","console"]}`,
		"unknown":   `{"panels":["scene","viewport","inspector","console","evil"]}`,
		"ratio":     `{"dir":"h","ratio":0,"a":{"panels":["scene","viewport"]},"b":{"panels":["inspector","console"]}}`,
		"dir":       `{"dir":"x","ratio":0.5,"a":{"panels":["scene","viewport"]},"b":{"panels":["inspector","console"]}}`,
		"half":      `{"dir":"h","ratio":0.5,"a":{"panels":["scene","viewport","inspector","console"]}}`,
		"active":    `{"panels":["scene","viewport","inspector","console"],"active":9}`,
		"empty":     `{"dir":"h","ratio":0.5,"a":{},"b":{"panels":["scene","viewport","inspector","console"]}}`,
	}
	for name, in := range bad {
		if _, err := Unmarshal([]byte(in), all); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
