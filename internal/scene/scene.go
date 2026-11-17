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
	KindText   Kind = "Text"
	// KindInstance is a placed copy of an object document (Ref).
	KindInstance Kind = "Instance"
	// KindChunk is one piece of a map: a square of ChunkTiles×ChunkTiles tiles.
	KindChunk Kind = "Chunk"
)

// DocType says what a document is.
type DocType string

const (
	// DocScene is the finished product: it can host a game (it is the server)
	// and may use a map. A scene cannot be imported into anything.
	DocScene DocType = "scene"
	// DocObject is a building block that can be imported into objects and scenes.
	DocObject DocType = "object"
	// DocMap is a game map made of chunks that can contain objects.
	DocMap DocType = "map"
)

const (
	// ChunkTiles is the side of a map chunk, in tiles. Chunks are small so that
	// they can be loaded and unloaded while the game runs.
	ChunkTiles = 64
	// DefaultTileSize is the tile side in pixels.
	DefaultTileSize = 16
)

// ImageMode controls how images are displayed.
type ImageMode string

const (
	// ImagePixel is pixel-native: nearest-neighbour sampling, whole-pixel
	// placement, so pixels stay crisp and behave as in retro games.
	ImagePixel ImageMode = "pixel"
	// ImageNormal is smooth: linear filtering.
	ImageNormal ImageMode = "normal"
)

// Node is one object in a scene. X/Y are local to the parent, in pixels.
type Node struct {
	ID       int
	Name     string
	Kind     Kind
	X, Y     int
	W, H     int // size in pixels; used by sprites
	Color    color.RGBA
	Texture  string // sprite image: slash-separated path relative to the assets folder
	Tag      string // group label used by scripts (touch "coin")
	Script   string // TrueDSL script: slash-separated path relative to the scripts folder
	Template bool   // a prototype for `spawn`; not part of the running scene
	Text     string // Text nodes: the label shown
	Ref      string // Instance nodes: project-relative path of the object document
	CX, CY   int    // Chunk nodes: chunk coordinates in the map
	FontSize int    // Text nodes: font size in pixels
	Parent   *Node
	Children []*Node
}

// Scene is a tree of nodes with a single root.
type Scene struct {
	Root      *Node
	ImageMode ImageMode
	Type      DocType
	TileSize  int    // tile side in pixels
	Map       string // scenes only: project-relative path of the map document
	nextID    int
}

// New creates a scene with a root node.
func New(rootName string) *Scene {
	s := &Scene{ImageMode: ImagePixel, Type: DocScene, TileSize: DefaultTileSize}
	s.Root = s.newNode(KindNode, rootName)
	return s
}

func (s *Scene) newNode(kind Kind, name string) *Node {
	s.nextID++
	return &Node{ID: s.nextID, Name: name, Kind: kind, Color: color.RGBA{0x8a, 0x8f, 0x9c, 0xff}}
}

// ChunkSize is the side of a chunk in pixels.
func (s *Scene) ChunkSize() int { return ChunkTiles * s.TileSize }

// ChunkOrigin returns the position of chunk n in map coordinates.
func (s *Scene) ChunkOrigin(n *Node) (x, y int) { return n.CX * s.ChunkSize(), n.CY * s.ChunkSize() }

// Add creates a child of parent (the root if nil) with a unique sibling name.
func (s *Scene) Add(parent *Node, kind Kind, name string) *Node {
	if parent == nil {
		parent = s.Root
	}
	n := s.newNode(kind, uniqueName(parent, name))
	switch kind {
	case KindSprite:
		n.W, n.H = 16, 16
	case KindChunk:
		n.W, n.H = s.ChunkSize(), s.ChunkSize()
	case KindText:
		n.W, n.H = 100, 14
		n.Text, n.FontSize = "Text", 10
		n.Color = color.RGBA{0xe2, 0xe5, 0xec, 0xff}
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

// HasBox reports whether the node has its own width and height.
func (n *Node) HasBox() bool { return n.Kind == KindSprite || n.Kind == KindText }

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

// ChunkOverflow returns a description of the first object inside chunk c that
// sticks out of the chunk's size×size square, or "" if everything fits.
// Positions of chunk children are relative to the chunk. Instances have no
// known size here and are not checked.
func ChunkOverflow(c *Node, size int) string {
	msg := ""
	var walk func(n *Node, ox, oy int)
	walk = func(n *Node, ox, oy int) {
		for _, ch := range n.Children {
			x, y := ox+ch.X, oy+ch.Y
			if ch.HasBox() && (x < 0 || y < 0 || x+ch.W > size || y+ch.H > size) && msg == "" {
				msg = "\"" + ch.Name + "\" sticks out of chunk " + c.Name
			}
			walk(ch, x, y)
		}
	}
	walk(c, 0, 0)
	return msg
}
