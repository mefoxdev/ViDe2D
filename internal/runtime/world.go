package runtime

import (
	"errors"
	"fmt"
	"sort"

	"github.com/vide2d/vide-studio/internal/scene"
)

// ChunkKey is the coordinate of a map chunk.
type ChunkKey struct{ X, Y int }

// ErrNoChunk is returned for chunk coordinates the map does not have.
var ErrNoChunk = errors.New("no such chunk")

// DefaultChunkRadius is how many chunks around the view stay loaded.
const DefaultChunkRadius = 1

// Options configure World construction.
type Options struct {
	// ChunkRadius is the number of chunks kept loaded around the view
	// (0 means DefaultChunkRadius).
	ChunkRadius int
	// Attach is called for every entity as it enters the world, so that a
	// script VM can set e.Behavior. May be nil.
	Attach func(e *Entity)
}

// chunkUnit is one map chunk, loadable and unloadable on its own. The parsed
// document stays in memory; the entities exist only while loaded.
type chunkUnit struct {
	key    ChunkKey
	node   *scene.Node
	ents   []*Entity
	loaded bool
}

// World is everything that exists while the game runs.
type World struct {
	Name      string
	TileSize  int
	ViewW     int
	ViewH     int
	TickRate  int
	ImageMode scene.ImageMode
	// CamX, CamY is the world position of the top-left corner of the view.
	CamX, CamY int
	// Tick counts simulation steps.
	Tick uint64
	// Optimized reports that the map is cut into chunks that load on demand.
	Optimized bool

	src       *Source
	opts      Options
	mapStatic []*Entity // map content that is always present
	chunks    map[ChunkKey]*chunkUnit
	order     []ChunkKey // all chunks, sorted by (Y, X)
	content   []*Entity  // the scene's own entities
	nextID    int
	live      int
	synced    bool
	syncCamX  int
	syncCamY  int
}

// NewWorld builds the world of scene sc (a scene document) from source src.
// Scene content and the map's static part are loaded; the chunks of an
// optimized map are loaded by the radius policy around the camera.
func NewWorld(src *Source, sc *scene.Scene, opts Options) (*World, error) {
	if sc.Type != scene.DocScene {
		return nil, fmt.Errorf("a %s document cannot be run; run a scene", sc.Type)
	}
	if !scene.ValidTickRate(sc.TickRate) {
		return nil, fmt.Errorf("bad tick rate %d (must be 60, 90 or 120)", sc.TickRate)
	}
	if sc.ViewW < 1 || sc.ViewH < 1 {
		return nil, fmt.Errorf("bad view size %dx%d", sc.ViewW, sc.ViewH)
	}
	if opts.ChunkRadius <= 0 {
		opts.ChunkRadius = DefaultChunkRadius
	}
	w := &World{
		Name: sc.Root.Name, TileSize: sc.TileSize, ViewW: sc.ViewW, ViewH: sc.ViewH,
		TickRate: sc.TickRate, ImageMode: sc.ImageMode,
		src: src, opts: opts, chunks: map[ChunkKey]*chunkUnit{},
	}
	if sc.Map != "" {
		if err := w.loadMap(sc); err != nil {
			return nil, err
		}
	}
	ents, err := w.expandNode(sc.Root, 0, 0, nil, 0, nil) // the root's own offset counts, as in WorldPos
	if err != nil {
		return nil, err
	}
	w.content = ents
	if err := w.syncChunks(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *World) loadMap(sc *scene.Scene) error {
	m, err := w.src.Doc(sc.Map)
	if err != nil {
		return fmt.Errorf("map: %w", err)
	}
	if m.Type != scene.DocMap {
		return fmt.Errorf("map %s is a %s document, not a map", sc.Map, m.Type)
	}
	if m.TileSize != sc.TileSize {
		return fmt.Errorf("map %s has tile_size %d but the scene has %d", sc.Map, m.TileSize, sc.TileSize)
	}
	w.Optimized = m.Optimization
	for _, c := range m.Root.Children {
		if c.Template {
			continue
		}
		if c.Kind == scene.KindChunk && w.Optimized {
			if len(w.chunks) >= MaxChunks {
				return fmt.Errorf("map %s has more than %d chunks", sc.Map, MaxChunks)
			}
			k := ChunkKey{c.CX, c.CY}
			w.chunks[k] = &chunkUnit{key: k, node: c}
			w.order = append(w.order, k)
			continue
		}
		// Free-form map: everything is loaded whole and stays.
		ents, err := w.expandNode(c, 0, 0, nil, 0, nil)
		if err != nil {
			return fmt.Errorf("map %s: %w", sc.Map, err)
		}
		w.mapStatic = append(w.mapStatic, ents...)
	}
	sort.Slice(w.order, func(i, j int) bool {
		a, b := w.order[i], w.order[j]
		return a.Y < b.Y || (a.Y == b.Y && a.X < b.X)
	})
	return nil
}

// expandNode creates entities for n and its subtree, parents first, in
// scene-tree order. (ox, oy) is the world position of n's parent. Templates
// are skipped with their whole subtree. On error nothing is kept.
func (w *World) expandNode(n *scene.Node, ox, oy int, parent *Entity, depth int, stack []string) ([]*Entity, error) {
	var out []*Entity
	if err := w.expand(n, ox, oy, parent, depth, stack, &out); err != nil {
		w.live -= len(out)
		return nil, err
	}
	return out, nil
}

func (w *World) expand(n *scene.Node, ox, oy int, parent *Entity, depth int, stack []string, out *[]*Entity) error {
	if n.Template || n.Kind == scene.KindChunk {
		return nil
	}
	if w.live >= MaxEntities {
		return fmt.Errorf("more than %d entities", MaxEntities)
	}
	x, y := ox+n.X, oy+n.Y
	e := &Entity{
		Name: n.Name, Kind: n.Kind, X: x, Y: y, PrevX: x, PrevY: y,
		Script: n.Script, Parent: parent,
	}
	if n.Tag != "" {
		e.Tags = []string{n.Tag}
	}
	switch n.Kind {
	case scene.KindSprite:
		e.W, e.H, e.Color, e.Texture = n.W, n.H, n.Color, n.Texture
	case scene.KindText:
		e.W, e.H, e.Color, e.Text, e.FontSize = n.W, n.H, n.Color, n.Text, n.FontSize
	}
	w.nextID++
	e.ID = w.nextID
	w.live++
	*out = append(*out, e)
	if w.opts.Attach != nil {
		w.opts.Attach(e)
	}

	children, ox2, oy2 := n.Children, x, y
	if n.Kind == scene.KindInstance {
		if depth >= MaxInstanceDepth {
			return fmt.Errorf("%q: objects are nested deeper than %d", n.Name, MaxInstanceDepth)
		}
		for _, r := range stack {
			if r == n.Ref {
				return fmt.Errorf("%q: object %s contains itself (cycle: %v -> %s)", n.Name, n.Ref, stack, n.Ref)
			}
		}
		doc, err := w.src.Doc(n.Ref)
		if err != nil {
			return fmt.Errorf("%q: %w", n.Name, err)
		}
		if doc.Type != scene.DocObject {
			return fmt.Errorf("%q: %s is a %s document, not an object", n.Name, n.Ref, doc.Type)
		}
		children = doc.Root.Children // the object's root offset is not applied, as in the studio
		stack = append(stack[:len(stack):len(stack)], n.Ref)
		depth++
	}
	for _, c := range children {
		if err := w.expand(c, ox2, oy2, e, depth, stack, out); err != nil {
			return err
		}
	}
	return nil
}

// ---- chunks ----

// ChunkKeys lists every chunk of the map, loaded or not, sorted by (Y, X).
func (w *World) ChunkKeys() []ChunkKey { return append([]ChunkKey(nil), w.order...) }

// ChunkLoaded reports whether chunk (cx, cy) is currently loaded.
func (w *World) ChunkLoaded(cx, cy int) bool {
	u := w.chunks[ChunkKey{cx, cy}]
	return u != nil && u.loaded
}

// LoadedChunks lists the loaded chunks sorted by (Y, X).
func (w *World) LoadedChunks() []ChunkKey {
	var out []ChunkKey
	for _, k := range w.order {
		if w.chunks[k].loaded {
			out = append(out, k)
		}
	}
	return out
}

// LoadChunk creates the entities of chunk (cx, cy). Loading a loaded chunk
// does nothing. On error the chunk stays unloaded.
func (w *World) LoadChunk(cx, cy int) error {
	u := w.chunks[ChunkKey{cx, cy}]
	if u == nil {
		return fmt.Errorf("%w: %d,%d", ErrNoChunk, cx, cy)
	}
	if u.loaded {
		return nil
	}
	var ents []*Entity
	for _, c := range u.node.Children {
		es, err := w.expandNode(c, u.node.X, u.node.Y, nil, 0, nil)
		if err != nil {
			w.live -= len(ents)
			return fmt.Errorf("chunk %d,%d: %w", cx, cy, err)
		}
		ents = append(ents, es...)
	}
	u.ents, u.loaded = ents, true
	return nil
}

// UnloadChunk drops the entities of chunk (cx, cy). It reports whether the
// chunk was loaded.
func (w *World) UnloadChunk(cx, cy int) bool {
	u := w.chunks[ChunkKey{cx, cy}]
	if u == nil || !u.loaded {
		return false
	}
	w.live -= len(u.ents)
	u.ents, u.loaded = nil, false
	return true
}

// UpdateChunks applies the radius policy: chunks that touch the view grown by
// radius chunks are loaded, all others are unloaded.
func (w *World) UpdateChunks(radius int) error {
	size := w.TileSize * scene.ChunkTiles
	x0, y0 := floorDiv(w.CamX, size)-radius, floorDiv(w.CamY, size)-radius
	x1, y1 := floorDiv(w.CamX+w.ViewW-1, size)+radius, floorDiv(w.CamY+w.ViewH-1, size)+radius
	for _, k := range w.order {
		if k.X >= x0 && k.X <= x1 && k.Y >= y0 && k.Y <= y1 {
			if err := w.LoadChunk(k.X, k.Y); err != nil {
				return err
			}
		} else {
			w.UnloadChunk(k.X, k.Y)
		}
	}
	return nil
}

// syncChunks runs the policy when the camera changed since the last run.
func (w *World) syncChunks() error {
	if len(w.order) == 0 || (w.synced && w.syncCamX == w.CamX && w.syncCamY == w.CamY) {
		return nil
	}
	w.synced, w.syncCamX, w.syncCamY = true, w.CamX, w.CamY
	return w.UpdateChunks(w.opts.ChunkRadius)
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// ---- iteration and stepping ----

// Each visits every live entity in draw order: map content (static part, then
// loaded chunks sorted by position), then the scene's entities, each in
// scene-tree order.
func (w *World) Each(fn func(e *Entity)) {
	for _, e := range w.mapStatic {
		fn(e)
	}
	for _, k := range w.order {
		for _, e := range w.chunks[k].ents {
			fn(e)
		}
	}
	for _, e := range w.content {
		fn(e)
	}
}

// Len is the number of live entities.
func (w *World) Len() int { return w.live }

// Step advances the simulation by exactly one fixed step.
func (w *World) Step() error {
	w.Tick++
	w.Each(func(e *Entity) { e.PrevX, e.PrevY = e.X, e.Y })
	var steps []*Entity
	w.Each(func(e *Entity) {
		if e.Behavior != nil {
			steps = append(steps, e)
		}
	})
	for _, e := range steps {
		e.Behavior.Step(w, e)
	}
	return w.syncChunks()
}
