package runtime

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

func bruteOverlap(w *World, self *Entity, tag string) []*Entity {
	sb := w.boxOf(self)
	var out []*Entity
	w.Each(func(e *Entity) {
		if e.HasTag(tag) && !related(self, e) && sb.overlaps(w.boxOf(e)) {
			out = append(out, e)
		}
	})
	return out
}

// The grid must give exactly what a scan of every object gives, in the same
// order, while objects move in every way the world allows.
func TestBroadphaseMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p := &proj{}
	n := 300
	for i := 0; i < n; i++ {
		s := spec{name: fmt.Sprint("o", i), x: rng.Intn(1000) - 500, y: rng.Intn(1000) - 500, w: 1 + rng.Intn(40), h: 1 + rng.Intn(40), tag: "t"}
		switch {
		case i%50 == 0:
			s.w, s.h = 3000, 5 // too big for the grid
		case i%7 == 0:
			s = spec{name: fmt.Sprint("g", i), node: true, x: s.x, y: s.y, tag: "t", kids: []spec{
				{name: fmt.Sprint("k1_", i), x: 0, y: 0, w: 5, h: 5}, {name: fmt.Sprint("k2_", i), x: 30, y: 20, w: 9, h: 9},
			}}
		}
		p.specs = append(p.specs, s)
	}
	for i := 0; i < 5; i++ {
		p.specs = append(p.specs, spec{name: fmt.Sprint("self", i), x: rng.Intn(600) - 300, y: rng.Intn(600) - 300, w: 20, h: 20})
	}
	w, _, _ := p.load(t)
	var ents, selves []*Entity
	w.Each(func(e *Entity) {
		if e.HasTag("t") {
			ents = append(ents, e)
		}
		if strings.HasPrefix(e.Name, "self") {
			selves = append(selves, e)
		}
	})
	check := func(round int) {
		for _, s := range selves {
			got, _ := w.overlapping(s, vm.Target{Tag: "t"})
			want := bruteOverlap(w, s, "t")
			if !slices.Equal(got, want) {
				t.Fatalf("round %d: grid found %d objects, a scan finds %d", round, len(got), len(want))
			}
		}
	}
	check(0)
	for round := 1; round <= 60; round++ {
		behind := false
		for k := 0; k < 40; k++ {
			e := ents[rng.Intn(len(ents))]
			kind := rng.Intn(3)
			if round%5 == 0 && k == 0 {
				kind = 3 // now and then something is moved behind the world's back
			}
			switch kind {
			case 0:
				w.Place(e, float64(rng.Intn(1200)-600)+rng.Float64(), float64(rng.Intn(1200)-600), false)
			case 1:
				if len(e.Children) > 0 { // move one member: the group's box changes
					c := e.Children[rng.Intn(len(e.Children))]
					w.Place(c, c.X+float64(rng.Intn(200)-100), c.Y, false)
				}
			case 2:
				e.W, e.H = float64(1+rng.Intn(60)), float64(1+rng.Intn(60))
				w.geoChanged(e, false)
			case 3:
				e.SetPos(float64(rng.Intn(1200)-600), float64(rng.Intn(1200)-600)) // behind the world's back
				behind = true
			}
		}
		for _, s := range selves {
			w.Place(s, float64(rng.Intn(1000)-500), float64(rng.Intn(1000)-500), false)
		}
		if behind {
			w.Tick++ // a new step: the sweep catches what was moved behind the world's back
		}
		check(round)
	}
}

func TestTouchingBoxRules(t *testing.T) {
	mk := func(x, y, w, h float64) *Entity { return &Entity{X: x, Y: y, W: w, H: h} }
	w := &World{}
	cases := []struct {
		a, b *Entity
		want bool
	}{
		{mk(0, 0, 10, 10), mk(5, 5, 10, 10), true},
		{mk(0, 0, 10, 10), mk(10, 0, 10, 10), false},        // edge to edge
		{mk(0, 0, 10, 10), mk(9.999, 0, 10, 10), true},      // 0.001 deep
		{mk(0, 0, 10, 10), mk(9.9999999, 0, 10, 10), false}, // within the epsilon
		{mk(5, 5, 0, 0), mk(0, 0, 10, 10), true},            // a point inside
		{mk(0, 0, 0, 0), mk(0, 0, 10, 10), true},            // the left-top edge belongs to the box
		{mk(10, 5, 0, 0), mk(0, 0, 10, 10), false},          // the right edge does not
		{mk(3, 3, 0, 0), mk(3, 3, 0, 0), true},              // two equal points
		{mk(3, 3, 0, 0), mk(3, 4, 0, 0), false},
		{mk(10, 10, -10, -10), mk(2, 2, 3, 3), false}, // a negative size is no box: the object is a point
	}
	for i, c := range cases {
		if got := w.boxOf(c.a).overlaps(w.boxOf(c.b)); got != c.want {
			t.Errorf("case %d: %v, want %v", i, got, c.want)
		}
		if got := w.boxOf(c.b).overlaps(w.boxOf(c.a)); got != c.want {
			t.Errorf("case %d reversed: %v, want %v", i, got, c.want)
		}
	}
}

func TestTagChangeAndDestroyUpdateTheIndex(t *testing.T) {
	w, _, sink := (&proj{
		scripts: map[string]string{"a": `when key space pressed
  tag = "other"
when key enter pressed
  print "n {count("x")} {touching("x")}"
`},
		specs: []spec{
			{name: "a", x: 0, y: 0, w: 10, h: 10, script: "a", tag: "x"},
			{name: "b", x: 5, y: 5, w: 10, h: 10, tag: "x"},
		},
	}).load(t)
	in := w.Input.(*InputState)
	in.Press("enter")
	steps(t, w, 1)
	in.Release("enter")
	in.Press("space")
	steps(t, w, 1)
	in.Release("space")
	in.Press("enter")
	steps(t, w, 1)
	mustNoErrors(t, sink)
	if got := sink.join(""); got != "n 2 yes|n 1 yes" {
		t.Fatalf("got %q", got)
	}
	w.Destroy(find(w, "b"))
	steps(t, w, 1)
	in.Release("enter")
	in.Press("enter")
	steps(t, w, 1)
	if got := sink.join(""); got != "n 2 yes|n 1 yes|n 0 no" {
		t.Fatalf("got %q", got)
	}
}

// A script that asks for touches in a huge crowd is held back by the step budget,
// not by the machine: the work of every query is charged.
func TestCollisionQueriesAreChargedToTheBudget(t *testing.T) {
	p := &proj{scripts: map[string]string{"q": "when update\n  repeat 50 times\n    var a = all(\"t\")\n"}}
	p.specs = append(p.specs, spec{name: "q", node: true, script: "q"})
	for i := 0; i < 2000; i++ {
		p.specs = append(p.specs, spec{name: fmt.Sprint("t", i), x: i % 100 * 20, y: i / 100 * 20, w: 10, h: 10, tag: "t"})
	}
	w, _, sink := p.load(t)
	steps(t, w, 1)
	// 50 lists of 2000 items cost 50 * (2000/4 + 2000/16) steps: well within the frame, no error
	mustNoErrors(t, sink)
	p.scripts["q"] = "when update\n  repeat 5000 times\n    var a = all(\"t\")\n"
	w, _, sink = p.load(t)
	steps(t, w, 1)
	if sink.count("TD401") == 0 {
		t.Fatalf("expected the budget to stop it:\n%s", sink.all())
	}
}

func TestManyTouchersStayWithinBudget(t *testing.T) {
	p := &proj{scripts: map[string]string{"m": "when update\n  x += 1\nwhen touch \"w\"\n  y += 0\n"}}
	for i := 0; i < 400; i++ {
		p.specs = append(p.specs, spec{name: fmt.Sprint("m", i), x: i * 3 % 600, y: i / 200 * 30, w: 10, h: 10, script: "m"})
	}
	for i := 0; i < 3000; i++ {
		p.specs = append(p.specs, spec{name: fmt.Sprint("wall", i), x: i % 150 * 8, y: i / 150 * 8, w: 8, h: 8, tag: "w"})
	}
	w, _, sink := p.load(t)
	steps(t, w, 5)
	mustNoErrors(t, sink)
	if sink.count("TD401") != 0 {
		t.Fatalf("budget hit by 400 touchers over 3000 walls:\n%s", sink.all())
	}
}

func vmTarget(tag string) vm.Target { return vm.Target{Tag: tag} }
