package scene

import "testing"

func TestAddUniqueNames(t *testing.T) {
	s := New("Main")
	a := s.Add(nil, KindSprite, "Sprite")
	b := s.Add(nil, KindSprite, "Sprite")
	if a.Name != "Sprite" || b.Name != "Sprite2" {
		t.Fatalf("names: %q %q", a.Name, b.Name)
	}
}

func TestWorldPos(t *testing.T) {
	s := New("Main")
	a := s.Add(nil, KindNode, "A")
	a.X, a.Y = 10, 20
	b := s.Add(a, KindSprite, "B")
	b.X, b.Y = 1, 2
	if x, y := b.WorldPos(); x != 11 || y != 22 {
		t.Fatalf("got %d,%d", x, y)
	}
}

func TestRemove(t *testing.T) {
	s := New("Main")
	a := s.Add(nil, KindNode, "A")
	if s.Remove(s.Root) {
		t.Fatal("root removed")
	}
	if !s.Remove(a) || len(s.Root.Children) != 0 {
		t.Fatal("remove failed")
	}
}

func TestWalkOrder(t *testing.T) {
	s := New("Main")
	a := s.Add(nil, KindNode, "A")
	s.Add(a, KindNode, "A1")
	s.Add(nil, KindNode, "B")
	var got []string
	s.Root.Walk(func(n *Node, _ int) { got = append(got, n.Name) })
	want := []string{"Main", "A", "A1", "B"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
}

func TestRename(t *testing.T) {
	s := New("Main")
	a := s.Add(nil, KindNode, "A")
	b := s.Add(nil, KindNode, "B")
	if !s.Rename(b, "  A ") || b.Name != "A2" {
		t.Fatalf("got %q", b.Name)
	}
	if !s.Rename(a, "A") || a.Name != "A" {
		t.Fatalf("self rename: %q", a.Name)
	}
	if s.Rename(a, "   ") || a.Name != "A" {
		t.Fatal("empty name accepted")
	}
}
