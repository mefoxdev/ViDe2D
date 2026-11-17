package runtime

import (
	"math"
	"slices"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// Collisions. Every object is an axis-aligned box in world coordinates:
//
//   - an object with a size (width and height both above zero) is that rectangle;
//     one with a zero or negative width or height has none (it is not drawn either);
//   - an object without one (a group, an instance, a plain node) is the union of
//     the rectangles of its descendants, so a hero made of several sprites
//     touches as one body (at most the first collisionNodes descendants count);
//   - if there is none either, it is the point (x, y).
//
// Two boxes touch when they overlap by more than boxEps on both axes, so boxes
// that merely lie edge to edge do not touch (a hero standing on the floor does
// not "touch" it, it "stands on" it: touching(tag, down)). A point touches a
// box when it lies in it (left and top edges count, right and bottom do not).
// An object never touches itself, its ancestors or its descendants.
//
// All arithmetic is plain float64 addition and comparison, which is identical
// on every machine. Results are always in scene-tree order.
const (
	boxEps         = 1e-6 // overlap needed to count as touching
	flushTol       = 1e-3 // the gap that still counts as "standing against" in touching(tag, dir)
	cellSize       = 64.0 // side of a broadphase cell
	maxCells       = 64   // an object that covers more cells goes to the "big" list of its tag
	linearTag      = 16   // tags with this few members are scanned without the grid
	collisionNodes = 256
	maxQueryCells  = 256 // a query that covers more cells scans the whole tag
)

type box struct{ x0, y0, x1, y1 float64 }

func axisOverlap(a0, a1, b0, b1 float64) bool {
	aPt, bPt := a1 <= a0, b1 <= b0
	switch {
	case !aPt && !bPt:
		return float64(a0+boxEps) < b1 && float64(b0+boxEps) < a1
	case aPt && bPt:
		return math.Abs(float64(a0-b0)) <= boxEps
	case aPt:
		return a0 >= float64(b0-boxEps) && a0 < float64(b1-boxEps)
	}
	return b0 >= float64(a0-boxEps) && b0 < float64(a1-boxEps)
}

func (a box) overlaps(b box) bool {
	return axisOverlap(a.x0, a.x1, b.x0, b.x1) && axisOverlap(a.y0, a.y1, b.y0, b.y1)
}

func (a box) union(b box) box {
	return box{math.Min(a.x0, b.x0), math.Min(a.y0, b.y0), math.Max(a.x1, b.x1), math.Max(a.y1, b.y1)}
}

// ownBox is the rectangle of an object that has a size.
func ownBox(e *Entity) (box, bool) {
	if !(e.W > 0 && e.H > 0) { // also false for NaN
		return box{}, false
	}
	return box{e.X, e.Y, float64(e.X + e.W), float64(e.Y + e.H)}, true
}

// boxOf is the collision box of e (see the package comment above). The union
// box of a group is cached until the geometry of the group or of anything in
// it changes (geoChanged clears the cache of e and of all its ancestors), and
// in any case for one step only. Recomputing it counts in w.bbWork, which the
// queries pass on to the step budget.
func (w *World) boxOf(e *Entity) box {
	if b, ok := ownBox(e); ok {
		return b
	}
	if len(e.Children) == 0 {
		return box{e.X, e.Y, e.X, e.Y}
	}
	if e.bbOK && e.bbTick == w.Tick {
		return e.bb
	}
	var u box
	found, seen := false, 0
	stack := append(make([]*Entity, 0, 16), e.Children...)
	for len(stack) > 0 && seen < collisionNodes {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		seen++
		if b, ok := ownBox(c); ok {
			if found {
				u = u.union(b)
			} else {
				u, found = b, true
			}
		}
		stack = append(stack, c.Children...)
	}
	if !found {
		u = box{e.X, e.Y, e.X, e.Y}
	}
	w.bbWork += seen
	e.bb, e.bbOK, e.bbTick = u, true, w.Tick
	return u
}

// related reports whether a and b are the same object, or one holds the other.
func related(a, b *Entity) bool {
	if a == b {
		return true
	}
	for p := b.Parent; p != nil; p = p.Parent {
		if p == a {
			return true
		}
	}
	for p := a.Parent; p != nil; p = p.Parent {
		if p == b {
			return true
		}
	}
	return false
}

// ---- the broadphase index ----

type cellKey struct {
	tag  string
	x, y int
}

// collider is the uniform grid of the objects that carry a tag, kept per tag.
// It is built lazily by the first query after the world's content changed and
// then follows the moves made through World.Place; objects moved any other way
// are caught by a sweep once per step.
type collider struct {
	valid   bool
	cells   map[cellKey][]*Entity
	big     map[string][]*Entity // objects too large for the grid
	tags    map[string][]*Entity // every object per tag, scene order
	names   map[string][]*Entity // every object per name, scene order
	namesOK bool
	grid    []*Entity // the indexed objects, scene order
	dirty   []*Entity
	stale   int    // entries of grid that no longer carry a tag
	swept   uint64 // the step the sweep last ran in
	stamp   uint64
}

func (c *collider) invalidate() { c.valid, c.namesOK = false, false }

// collChanged says that the content of the world changed: objects came or went.
// Everything is rebuilt by the next query (the rebuild is charged to the
// script that makes it, see takeWork).
func (w *World) collChanged() { w.coll.invalidate() }

// takeWork returns the work done outside the queries themselves (rebuilding
// the index, recomputing the boxes of groups) since the last call, for the
// step budget.
func (w *World) takeWork() int {
	n := w.bbWork + w.rebuildWork
	w.bbWork, w.rebuildWork = 0, 0
	return n
}

// geoChanged says that the geometry of e (and of its descendants when subtree
// is set) changed through the world. Its ancestors change with it, because a
// group is as large as its members.
func (w *World) geoChanged(e *Entity, subtree bool) {
	e.bbOK = false
	if subtree {
		stack := append([]*Entity(nil), e.Children...)
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x.bbOK = false
			stack = append(stack, x.Children...)
		}
	}
	for p := e.Parent; p != nil; p = p.Parent {
		p.bbOK = false
	}
	if !w.coll.valid {
		return
	}
	w.queueMoved(e)
	if subtree {
		stack := append([]*Entity(nil), e.Children...)
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			w.queueMoved(x)
			stack = append(stack, x.Children...)
		}
	}
	for p := e.Parent; p != nil; p = p.Parent {
		w.queueMoved(p)
	}
}

func (w *World) queueMoved(e *Entity) {
	if e.gIn && !e.gDirty {
		e.gDirty = true
		w.coll.dirty = append(w.coll.dirty, e)
	}
}

func cellRange(b box) (x0, y0, x1, y1 int, ok bool) {
	fx0, fy0 := math.Floor(b.x0/cellSize), math.Floor(b.y0/cellSize)
	fx1, fy1 := math.Floor(b.x1/cellSize), math.Floor(b.y1/cellSize)
	if math.IsNaN(fx0+fy0+fx1+fy1) || (fx1-fx0+1)*(fy1-fy0+1) > maxCells || math.Abs(fx0) > 1e9 || math.Abs(fy0) > 1e9 || math.Abs(fx1) > 1e9 || math.Abs(fy1) > 1e9 {
		return 0, 0, 0, 0, false
	}
	return int(fx0), int(fy0), int(fx1), int(fy1), true
}

func (w *World) gridInsert(e *Entity) {
	c := &w.coll
	e.gIn = true
	x0, y0, x1, y1, ok := cellRange(w.boxOf(e))
	e.gBig = !ok
	e.gx0, e.gy0, e.gx1, e.gy1 = x0, y0, x1, y1
	for _, tag := range e.Tags {
		if !ok {
			c.big[tag] = append(c.big[tag], e)
			continue
		}
		for cy := y0; cy <= y1; cy++ {
			for cx := x0; cx <= x1; cx++ {
				k := cellKey{tag, cx, cy}
				c.cells[k] = append(c.cells[k], e)
			}
		}
	}
}

func removeEnt(list []*Entity, e *Entity) []*Entity {
	for i, x := range list {
		if x == e {
			list[i] = list[len(list)-1]
			list[len(list)-1] = nil
			return list[:len(list)-1]
		}
	}
	return list
}

func (w *World) gridRemove(e *Entity) {
	c := &w.coll
	for _, tag := range e.Tags {
		if e.gBig {
			c.big[tag] = removeEnt(c.big[tag], e)
			continue
		}
		for cy := e.gy0; cy <= e.gy1; cy++ {
			for cx := e.gx0; cx <= e.gx1; cx++ {
				k := cellKey{tag, cx, cy}
				if l := removeEnt(c.cells[k], e); len(l) == 0 {
					delete(c.cells, k)
				} else {
					c.cells[k] = l
				}
			}
		}
	}
	e.gIn = false
}

// collEnsure brings the index up to date.
func (w *World) collEnsure() {
	c := &w.coll
	if !c.valid {
		w.rebuildColl()
		return
	}
	if c.swept != w.Tick {
		// Between steps anything may have been moved without telling the world
		// (a custom Behavior, a test): look at every indexed object once.
		c.swept = w.Tick
		for _, e := range c.grid {
			if e.gIn && !e.gDirty {
				if x0, y0, x1, y1, ok := cellRange(w.boxOf(e)); ok == e.gBig || x0 != e.gx0 || y0 != e.gy0 || x1 != e.gx1 || y1 != e.gy1 {
					e.gDirty = true
					c.dirty = append(c.dirty, e)
				}
			}
		}
	}
	for _, e := range c.dirty {
		e.gDirty = false
		if e.gIn && !e.Dead {
			w.gridRemove(e)
			w.gridInsert(e)
		}
	}
	c.dirty = c.dirty[:0]
}

func (w *World) rebuildColl() {
	c := &w.coll
	for _, e := range c.grid {
		e.gIn, e.gDirty, e.gListed = false, false, false
	}
	c.cells = map[cellKey][]*Entity{}
	c.big = map[string][]*Entity{}
	c.tags = map[string][]*Entity{}
	c.names = map[string][]*Entity{}
	c.grid, c.dirty, c.namesOK = c.grid[:0], c.dirty[:0], false
	c.valid, c.swept, c.stale = true, w.Tick, 0
	n := 0
	w.Each(func(e *Entity) {
		e.ord = n
		n++
		if len(e.Tags) == 0 {
			return
		}
		for _, tag := range e.Tags {
			c.tags[tag] = append(c.tags[tag], e)
		}
		c.grid = append(c.grid, e)
		e.gListed = true
		w.gridInsert(e)
	})
	w.rebuildWork += n / 16
}

func (w *World) namesEnsure() {
	c := &w.coll
	w.collEnsure()
	if c.namesOK {
		return
	}
	c.names = map[string][]*Entity{}
	n := 0
	w.Each(func(e *Entity) {
		e.ord = n
		n++
		c.names[e.Name] = append(c.names[e.Name], e)
	})
	c.namesOK = true
	w.rebuildWork += n / 16
}

// findByName returns the first live object with the name.
func (w *World) findByName(name string) *Entity {
	w.namesEnsure()
	if l := w.coll.names[name]; len(l) > 0 {
		return l[0]
	}
	return nil
}

// query returns the objects of target t, other than self and its relatives,
// whose box passes hit, in scene order. Only objects that can overlap region
// are looked at. With first set it stops at the first object found. The second
// result is the number of boxes examined, for the step budget.
func (w *World) query(self *Entity, t vm.Target, region box, first bool, hit func(box) bool) ([]*Entity, int) {
	if t.HasObj {
		e := w.byID[int(t.Obj)]
		if e == nil || e.Dead || e.pending || related(self, e) || !hit(w.boxOf(e)) {
			return nil, 1
		}
		return []*Entity{e}, 1
	}
	w.collEnsure()
	c := &w.coll
	list := c.tags[t.Tag]
	if len(list) == 0 {
		return nil, 0
	}
	var out []*Entity
	work := 0
	try := func(e *Entity) bool {
		work++
		if !related(self, e) && hit(w.boxOf(e)) {
			out = append(out, e)
			return first
		}
		return false
	}
	x0, y0, x1, y1, ok := cellRange(region)
	if len(list) <= linearTag || !ok || (x1-x0+1)*(y1-y0+1) > maxQueryCells {
		for _, e := range list {
			if try(e) {
				return out, work
			}
		}
		return out, work
	}
	c.stamp++
	for _, e := range c.big[t.Tag] {
		e.qstamp = c.stamp
		if try(e) {
			return out, work
		}
	}
	for cy := y0; cy <= y1; cy++ {
		for cx := x0; cx <= x1; cx++ {
			for _, e := range c.cells[cellKey{t.Tag, cx, cy}] {
				if e.qstamp == c.stamp {
					continue
				}
				e.qstamp = c.stamp
				if try(e) {
					return out, work
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b *Entity) int { return a.ord - b.ord })
	return out, work
}

// ---- operations used by the script host ----

func expand(b box, d vm.Dir, by float64) box {
	switch d {
	case vm.Left:
		b.x0 = float64(b.x0 - by)
	case vm.Right:
		b.x1 = float64(b.x1 + by)
	case vm.Up:
		b.y0 = float64(b.y0 - by)
	default:
		b.y1 = float64(b.y1 + by)
	}
	return b
}

// touchesAny reports whether self overlaps an object of t; with dir the box of
// self is first grown by flushTol toward dir.
func (w *World) touchesAny(self *Entity, t vm.Target, dir vm.Dir, useDir bool) (bool, int) {
	sb := w.boxOf(self)
	if useDir {
		sb = expand(sb, dir, flushTol)
	}
	region := box{sb.x0 - boxEps, sb.y0 - boxEps, sb.x1 + boxEps, sb.y1 + boxEps}
	out, work := w.query(self, t, region, true, sb.overlaps)
	return len(out) > 0, work
}

// overlapping lists the objects of t that overlap self, in scene order.
func (w *World) overlapping(self *Entity, t vm.Target) ([]*Entity, int) {
	sb := w.boxOf(self)
	region := box{sb.x0 - boxEps, sb.y0 - boxEps, sb.x1 + boxEps, sb.y1 + boxEps}
	return w.query(self, t, region, false, sb.overlaps)
}

// allowedMove is how far self can move by (dx, dy) (one of them is zero)
// before it is stopped flush against an object carrying tag.
func (w *World) allowedMove(self *Entity, dx, dy float64, tag string) (float64, int) {
	sb := w.boxOf(self)
	end := box{float64(sb.x0 + dx), float64(sb.y0 + dy), float64(sb.x1 + dx), float64(sb.y1 + dy)}
	region := sb.union(end)
	horizontal := dx != 0
	dist := math.Abs(dx + dy)
	limit := dist
	hit := func(cb box) bool {
		var perp bool
		var gap float64
		switch {
		case horizontal && dx > 0:
			perp = axisOverlap(sb.y0, sb.y1, cb.y0, cb.y1)
			gap = float64(cb.x0 - sb.x1)
		case horizontal:
			perp = axisOverlap(sb.y0, sb.y1, cb.y0, cb.y1)
			gap = float64(sb.x0 - cb.x1)
		case dy > 0:
			perp = axisOverlap(sb.x0, sb.x1, cb.x0, cb.x1)
			gap = float64(cb.y0 - sb.y1)
		default:
			perp = axisOverlap(sb.x0, sb.x1, cb.x0, cb.x1)
			gap = float64(sb.y0 - cb.y1)
		}
		// ahead of the front edge (an object already overlapping does not block)
		// and within reach of this move
		if !perp || gap < -boxEps || gap > dist {
			return false
		}
		limit = math.Min(limit, math.Max(gap, 0))
		return false
	}
	_, work := w.query(self, vm.Target{Tag: tag}, region, false, hit)
	return limit, work
}

// distanceBetween is the distance between the centers of two boxes.
func (w *World) distanceBetween(a, b *Entity) float64 {
	ab, bb := w.boxOf(a), w.boxOf(b)
	dx := float64(float64(ab.x0+ab.x1)/2 - float64(bb.x0+bb.x1)/2)
	dy := float64(float64(ab.y0+ab.y1)/2 - float64(bb.y0+bb.y1)/2)
	return math.Sqrt(float64(float64(dx*dx) + float64(dy*dy)))
}

// retag gives e its new tags. While the index is valid it is updated in
// place (a script that changes a tag in a loop must not make the next query
// rebuild everything); the work is charged to the frame.
func (w *World) retag(e *Entity, tags []string) {
	c := &w.coll
	if !c.valid || e.pending || e.Dead {
		e.Tags = tags
		return
	}
	cost := 1
	if e.gIn {
		w.gridRemove(e)
	}
	for _, t := range e.Tags {
		l := c.tags[t]
		cost += len(l)
		if i, ok := ordIndex(l, e.ord); ok {
			l = slices.Delete(l, i, i+1)
		}
		if len(l) == 0 {
			delete(c.tags, t)
		} else {
			c.tags[t] = l
		}
	}
	hadTags := len(e.Tags) > 0
	e.Tags = tags
	for _, t := range tags {
		l := c.tags[t]
		cost += len(l)
		i, _ := ordIndex(l, e.ord)
		c.tags[t] = slices.Insert(l, i, e)
	}
	switch {
	case len(tags) > 0:
		if !e.gListed {
			e.gListed = true
			c.grid = append(c.grid, e)
		}
		w.gridInsert(e)
	case hadTags:
		c.stale++
		if c.stale > 32 && c.stale*2 > len(c.grid) {
			out := c.grid[:0]
			for _, x := range c.grid {
				if x.gIn {
					out = append(out, x)
				} else {
					x.gListed = false
				}
			}
			clear(c.grid[len(out):])
			c.grid, c.stale = out, 0
		}
	}
	w.rebuildWork += cost / 64
}

// ordIndex finds the place of ord in a list sorted by scene order.
func ordIndex(l []*Entity, ord int) (int, bool) {
	i, ok := slices.BinarySearchFunc(l, ord, func(e *Entity, o int) int { return e.ord - o })
	return i, ok
}
