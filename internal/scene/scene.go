// Package scene holds the in-memory model of a ViDe2D scene: a tree of nodes.
package scene

import (
	"fmt"
	"image/color"
	"strings"
)

// Kind is the type of a node.
type Kind string

const (
	KindNode   Kind = "Node"
	KindSprite Kind = "Sprite"
)

// Node is one object in a scene. X/Y are local to the parent, in pixels.
type Node struct {
	ID       int
	Name     string
	Kind     Kind
	X, Y     int
	W, H     int // size in pixels; used by sprites
	Color    color.RGBA
	Parent   *Node
	Children []*Node
}

// Scene is a tree of nodes with a single root.
type Scene struct {
	Root   *Node
	nextID int
}

// New creates a scene with a root node.
func New(rootName string) *Scene {
	s := &Scene{}
	s.Root = s.newNode(KindNode, rootName)
	return s
}

func (s *Scene) newNode(kind Kind, name string) *Node {
	s.nextID++
	return &Node{ID: s.nextID, Name: name, Kind: kind, Color: color.RGBA{0x8a, 0x8f, 0x9c, 0xff}}
}

// Add creates a child of parent (the root if nil) with a unique sibling name.
func (s *Scene) Add(parent *Node, kind Kind, name string) *Node {
	if parent == nil {
		parent = s.Root
	}
	n := s.newNode(kind, uniqueName(parent, name))
	if kind == KindSprite {
		n.W, n.H = 16, 16
	}
	n.Parent = parent
	parent.Children = append(parent.Children, n)
	return n
}

// Remove deletes n and its subtree. The root cannot be removed.
func (s *Scene) Remove(n *Node) bool {
	if n == nil || n.Parent == nil {
		return false
	}
	p := n.Parent
	for i, c := range p.Children {
		if c == n {
			p.Children = append(p.Children[:i], p.Children[i+1:]...)
			break
		}
	}
	n.Parent = nil
	return true
}

// Rename changes a node's name, keeping it unique among siblings.
// It returns false if the trimmed name is empty.
func (s *Scene) Rename(n *Node, name string) bool {
	name = strings.TrimSpace(name)
	if n == nil || name == "" {
		return false
	}
	if n.Parent == nil {
		n.Name = name
		return true
	}
	n.Name = "" // exclude self from the taken set
	n.Name = uniqueName(n.Parent, name)
	return true
}

func uniqueName(parent *Node, base string) string {
	taken := map[string]bool{}
	for _, c := range parent.Children {
		taken[c.Name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		if name := fmt.Sprintf("%s%d", base, i); !taken[name] {
			return name
		}
	}
}

// WorldPos returns the node's position in scene coordinates.
func (n *Node) WorldPos() (x, y int) {
	for c := n; c != nil; c = c.Parent {
		x += c.X
		y += c.Y
	}
	return
}

// Walk visits n and all descendants in draw order (parents first).
func (n *Node) Walk(fn func(n *Node, depth int)) { n.walk(fn, 0) }

func (n *Node) walk(fn func(*Node, int), depth int) {
	fn(n, depth)
	for _, c := range n.Children {
		c.walk(fn, depth+1)
	}
}
