// Package dock implements a dockable panel layout: a tree of splits whose
// leaves are tab groups. The tree logic here is pure and UI-independent.
package dock

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
)

// PanelID names a panel.
type PanelID string

// Dir is the split direction.
type Dir int

const (
	// SplitH puts A on the left and B on the right.
	SplitH Dir = iota
	// SplitV puts A on top and B at the bottom.
	SplitV
)

// Zone is a drop target inside a leaf.
type Zone int

const (
	ZoneNone Zone = iota
	ZoneCenter
	ZoneLeft
	ZoneRight
	ZoneTop
	ZoneBottom
)

const (
	headerH  = 26
	minRatio = 0.05
	maxDepth = 16
)

// Node is a split (A and B set) or a leaf (Panels set).
type Node struct {
	Dir    Dir
	Ratio  float64 // share of A, for splits
	A, B   *Node
	Panels []PanelID
	Active int
}

// Leaf creates a tab group.
func Leaf(ids ...PanelID) *Node { return &Node{Panels: ids} }

// IsLeaf reports whether n is a tab group.
func (n *Node) IsLeaf() bool { return n.A == nil }

// FindLeaf returns the leaf holding id, or nil.
func (n *Node) FindLeaf(id PanelID) *Node {
	if n.IsLeaf() {
		for _, p := range n.Panels {
			if p == id {
				return n
			}
		}
		return nil
	}
	if l := n.A.FindLeaf(id); l != nil {
		return l
	}
	return n.B.FindLeaf(id)
}

func (n *Node) parentOf(t *Node) *Node {
	if n.IsLeaf() {
		return nil
	}
	if n.A == t || n.B == t {
		return n
	}
	if p := n.A.parentOf(t); p != nil {
		return p
	}
	return n.B.parentOf(t)
}

// Panels lists all panels in the tree, in layout order.
func (n *Node) AllPanels() []PanelID {
	if n.IsLeaf() {
		return append([]PanelID(nil), n.Panels...)
	}
	return append(n.A.AllPanels(), n.B.AllPanels()...)
}

// Activate makes id the visible tab of its group. It reports whether id exists.
func (n *Node) Activate(id PanelID) bool {
	leaf := n.FindLeaf(id)
	if leaf == nil {
		return false
	}
	for i, p := range leaf.Panels {
		if p == id {
			leaf.Active = i
		}
	}
	return true
}

// Remove takes id out of the tree. An emptied leaf is collapsed into its
// sibling. It returns false if id is not present.
func (root *Node) Remove(id PanelID) bool {
	leaf := root.FindLeaf(id)
	if leaf == nil {
		return false
	}
	for i, p := range leaf.Panels {
		if p == id {
			leaf.Panels = append(leaf.Panels[:i], leaf.Panels[i+1:]...)
			break
		}
	}
	leaf.Active = min(max(leaf.Active, 0), max(len(leaf.Panels)-1, 0))
	if len(leaf.Panels) == 0 && leaf != root {
		parent := root.parentOf(leaf)
		sib := parent.A
		if sib == leaf {
			sib = parent.B
		}
		*parent = *sib
	}
	return true
}

// Dock moves id next to ref (a panel in the target leaf): into its tab group
// (ZoneCenter) or into a new split on the given side. It is a no-op returning
// false if ref is missing, id == ref, or the move would not change anything.
func (root *Node) Dock(id, ref PanelID, z Zone) bool {
	if id == ref || z == ZoneNone || root.FindLeaf(id) == nil || root.FindLeaf(ref) == nil {
		return false
	}
	root.Remove(id)
	target := root.FindLeaf(ref)
	if z == ZoneCenter {
		target.Panels = append(target.Panels, id)
		target.Active = len(target.Panels) - 1
		return true
	}
	old := *target
	nl := Leaf(id)
	var n Node
	switch z {
	case ZoneLeft:
		n = Node{Dir: SplitH, Ratio: 0.3, A: nl, B: &old}
	case ZoneRight:
		n = Node{Dir: SplitH, Ratio: 0.7, A: &old, B: nl}
	case ZoneTop:
		n = Node{Dir: SplitV, Ratio: 0.3, A: nl, B: &old}
	default:
		n = Node{Dir: SplitV, Ratio: 0.7, A: &old, B: nl}
	}
	*target = n
	return true
}

// ZoneAt maps a point inside leaf rectangle r to a drop zone.
func ZoneAt(r image.Rectangle, p image.Point) Zone {
	if !p.In(r) {
		return ZoneNone
	}
	if p.Y < r.Min.Y+headerH {
		return ZoneCenter
	}
	body := image.Rect(r.Min.X, r.Min.Y+headerH, r.Max.X, r.Max.Y)
	if body.Dx() == 0 || body.Dy() == 0 {
		return ZoneCenter
	}
	fx := float64(p.X-body.Min.X) / float64(body.Dx())
	fy := float64(p.Y-body.Min.Y) / float64(body.Dy())
	if fx >= .25 && fx <= .75 && fy >= .25 && fy <= .75 {
		return ZoneCenter
	}
	z, best := ZoneLeft, fx
	if d := 1 - fx; d < best {
		z, best = ZoneRight, d
	}
	if fy < best {
		z, best = ZoneTop, fy
	}
	if d := 1 - fy; d < best {
		z = ZoneBottom
	}
	return z
}

// ZoneRect returns the highlight area for zone z inside leaf rectangle r.
func ZoneRect(r image.Rectangle, z Zone) image.Rectangle {
	switch z {
	case ZoneLeft:
		return image.Rect(r.Min.X, r.Min.Y, r.Min.X+r.Dx()/2, r.Max.Y)
	case ZoneRight:
		return image.Rect(r.Min.X+r.Dx()/2, r.Min.Y, r.Max.X, r.Max.Y)
	case ZoneTop:
		return image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+r.Dy()/2)
	case ZoneBottom:
		return image.Rect(r.Min.X, r.Min.Y+r.Dy()/2, r.Max.X, r.Max.Y)
	}
	return r
}

// ---- serialization ----

type jsonNode struct {
	Dir    string    `json:"dir,omitempty"`
	Ratio  float64   `json:"ratio,omitempty"`
	A      *jsonNode `json:"a,omitempty"`
	B      *jsonNode `json:"b,omitempty"`
	Panels []string  `json:"panels,omitempty"`
	Active int       `json:"active,omitempty"`
}

func toJSON(n *Node) *jsonNode {
	if n.IsLeaf() {
		j := &jsonNode{Active: n.Active}
		for _, p := range n.Panels {
			j.Panels = append(j.Panels, string(p))
		}
		return j
	}
	d := "h"
	if n.Dir == SplitV {
		d = "v"
	}
	return &jsonNode{Dir: d, Ratio: n.Ratio, A: toJSON(n.A), B: toJSON(n.B)}
}

// Marshal serializes the layout.
func Marshal(root *Node) ([]byte, error) { return json.MarshalIndent(toJSON(root), "", "  ") }

// Unmarshal parses a layout and checks that it contains exactly the required
// panels, each once.
func Unmarshal(data []byte, required []PanelID) (*Node, error) {
	var j jsonNode
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("invalid layout: %w", err)
	}
	n, err := fromJSON(&j, 0)
	if err != nil {
		return nil, err
	}
	seen := map[PanelID]int{}
	for _, p := range n.AllPanels() {
		seen[p]++
	}
	for _, p := range required {
		if seen[p] != 1 {
			return nil, fmt.Errorf("invalid layout: panel %q appears %d times", p, seen[p])
		}
	}
	if len(seen) != len(required) {
		return nil, errors.New("invalid layout: unknown panels")
	}
	return n, nil
}

func fromJSON(j *jsonNode, depth int) (*Node, error) {
	if j == nil || depth > maxDepth {
		return nil, errors.New("invalid layout: bad structure")
	}
	if j.A == nil && j.B == nil {
		if len(j.Panels) == 0 || j.Active < 0 || j.Active >= len(j.Panels) {
			return nil, errors.New("invalid layout: bad tab group")
		}
		n := &Node{Active: j.Active}
		for _, p := range j.Panels {
			n.Panels = append(n.Panels, PanelID(p))
		}
		return n, nil
	}
	if j.A == nil || j.B == nil || len(j.Panels) != 0 || !(j.Ratio >= minRatio && j.Ratio <= 1-minRatio) {
		return nil, errors.New("invalid layout: bad split")
	}
	n := &Node{Ratio: j.Ratio}
	switch j.Dir {
	case "h":
		n.Dir = SplitH
	case "v":
		n.Dir = SplitV
	default:
		return nil, fmt.Errorf("invalid layout: bad direction %q", j.Dir)
	}
	var err error
	if n.A, err = fromJSON(j.A, depth+1); err != nil {
		return nil, err
	}
	if n.B, err = fromJSON(j.B, depth+1); err != nil {
		return nil, err
	}
	return n, nil
}
