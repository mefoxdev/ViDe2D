package runtime

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/tdsl/vm"
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
	// Attach is called for every entity as it enters the world, so that
	// custom logic can set e.Behavior. May be nil. It runs after the script
	// attachment below and may replace its Behavior.
	Attach func(e *Entity)

	// Scripts turns on TrueDSL: every entity with a Script gets a script
	// behavior (compiled on its first step, see script.go).
	Scripts bool
	// Input is the keyboard the scripts see. Nil means a fresh InputState,
	// which the Ebitengine front end then feeds from the real keyboard.
	Input Input
	// Log receives the "print" output of scripts and script errors, one
	// message per call (it may span several lines). Nil means stderr.
	Log func(msg string)
	// Seed seeds the random numbers of scripts. The same seed and the same
	// input give the same game; the caller picks it (never the engine).
	Seed uint64
}

// chunkUnit is one map chunk, loadable and unloadable on its own. The parsed
// document stays in memory; the entities exist only while loaded.
type chunkUnit struct {
	key    ChunkKey
	node   *scene.Node
	ents   []*Entity
	loaded bool
	state  ChunkState
	ids    map[string]int // entity id by node path: the same node keeps its id across reloads
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
	// Input is the keyboard state scripts read; nil without scripts.
	Input Input

	src         *Source
	opts        Options
	mapStatic   []*Entity    // map content that is always present
	scene       *scene.Scene // the scene document, for the names of all objects
	mapDoc      *scene.Scene
	chunks      map[ChunkKey]*chunkUnit
	order       []ChunkKey // all chunks, sorted by (Y, X)
	content     []*Entity  // the scene's own entities
	scripts     *scriptRuntime
	byID        map[int]*Entity // live entities that scripts can address
	destroyQ    []*Entity
	pending     []*Entity // spawned this step: alive for scripts, joined to the world at the end of the step
	spawnLive   int       // live objects made by "spawn" (pending ones included)
	templates   map[string]*scene.Node
	nodes       []*scene.Node // see allNodes
	coll        collider
	bbWork      int // see takeWork
	rebuildWork int
	spawnDepth  int // nesting of "spawn" inside the start-up code of spawned objects
	nextID      int
	live        int
	synced      bool
	syncCamX    int
	syncCamY    int
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
		src: src, opts: opts, scene: sc, chunks: map[ChunkKey]*chunkUnit{}, byID: map[int]*Entity{},
	}
	if opts.Scripts {
		w.Input = opts.Input
		if w.Input == nil {
			w.Input = NewInputState()
		}
		w.scripts = newScriptRuntime(w)
	}
	if sc.Map != "" {
		if err := w.loadMap(sc); err != nil {
			return nil, err
		}
	}
	ents, err := w.expandNode(sc.Root, 0, 0, nil, 0, nil, scope{}) // the root's own offset counts, as in WorldPos
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
	w.mapDoc = m
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
			w.chunks[k] = &chunkUnit{key: k, node: c, state: newChunkState(), ids: map[string]int{}}
			w.order = append(w.order, k)
			continue
		}
		// Free-form map: everything is loaded whole and stays.
		ents, err := w.expandNode(c, 0, 0, nil, 0, nil, scope{})
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
func (w *World) expandNode(n *scene.Node, ox, oy int, parent *Entity, depth int, stack []string, sc scope) ([]*Entity, error) {
	var out []*Entity
	if err := w.expand(n, ox, oy, parent, depth, stack, &out, sc); err != nil {
		w.discard(out)
		return nil, err
	}
	return out, nil
}

// discard forgets entities that were created but are not kept (a failed
// load): the live count and the id index are rolled back.
func (w *World) discard(ents []*Entity) {
	for _, e := range ents {
		w.forget(e)
	}
	w.live -= len(ents)
}

// scope tells expand which chunk a node belongs to and where it sits in it:
// path is the node's index path ("3/0/2"), the stable identity of the node.
type scope struct {
	unit *chunkUnit
	path string
}

func (s scope) child(i int) scope {
	if s.unit == nil {
		return s
	}
	return scope{s.unit, s.path + "/" + strconv.Itoa(i)}
}

func (w *World) expand(n *scene.Node, ox, oy int, parent *Entity, depth int, stack []string, out *[]*Entity, sc scope) error {
	if n.Template || n.Kind == scene.KindChunk {
		return nil
	}
	if sc.unit != nil && sc.unit.state.Destroyed[sc.path] {
		return nil // destroyed earlier: it does not come back with the chunk
	}
	if w.live >= MaxEntities {
		return fmt.Errorf("more than %d entities", MaxEntities)
	}
	x, y := ox+n.X, oy+n.Y
	e := &Entity{
		Name: n.Name, Kind: n.Kind, X: float64(x), Y: float64(y), PrevX: float64(x), PrevY: float64(y),
		Script: n.Script, Parent: parent, Visible: true,
	}
	if n.Tag != "" {
		e.Tags = []string{n.Tag}
	}
	switch n.Kind {
	case scene.KindSprite:
		e.W, e.H, e.Color, e.Texture = float64(n.W), float64(n.H), n.Color, n.Texture
	case scene.KindText:
		e.W, e.H, e.Color, e.Text, e.FontSize = float64(n.W), float64(n.H), n.Color, n.Text, n.FontSize
	}
	e.fresh = true
	if sc.unit != nil {
		e.key, e.chunk = sc.path, sc.unit
		if id := sc.unit.ids[sc.path]; id != 0 {
			e.ID = id
		} else {
			w.nextID++
			e.ID = w.nextID
			sc.unit.ids[sc.path] = e.ID
		}
	} else {
		w.nextID++
		e.ID = w.nextID
	}
	if parent != nil {
		parent.Children = append(parent.Children, e)
	}
	w.live++
	w.byID[e.ID] = e
	*out = append(*out, e)
	if w.scripts != nil && e.Script != "" {
		w.scripts.attach(e)
	}
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
	for i, c := range children {
		if err := w.expand(c, ox2, oy2, e, depth, stack, out, sc.child(i)); err != nil {
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
	for i, c := range u.node.Children {
		es, err := w.expandNode(c, u.node.X, u.node.Y, nil, 0, nil, scope{u, strconv.Itoa(i)})
		if err != nil {
			w.discard(ents) // nothing of this chunk stays: live count and id index are rolled back
			return fmt.Errorf("chunk %d,%d: %w", cx, cy, err)
		}
		ents = append(ents, es...)
	}
	u.ents, u.loaded = ents, true
	u.state.restore(ents)
	w.collChanged()
	return nil
}

// UnloadChunk drops the entities of chunk (cx, cy), first saving their state
// in the chunk's ChunkState. It reports whether the chunk was loaded.
func (w *World) UnloadChunk(cx, cy int) bool {
	u := w.chunks[ChunkKey{cx, cy}]
	if u == nil || !u.loaded {
		return false
	}
	u.state.save(u.ents)
	w.live -= len(u.ents)
	for _, e := range u.ents {
		w.forget(e)
	}
	u.ents, u.loaded = nil, false
	w.collChanged()
	return true
}

// ChunkState returns the remembered state of chunk (cx, cy), or nil if the
// map has no such chunk. See ChunkState for what it holds.
func (w *World) ChunkState(cx, cy int) *ChunkState {
	if u := w.chunks[ChunkKey{cx, cy}]; u != nil {
		return &u.state
	}
	return nil
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

// Step advances the simulation by exactly one fixed step. The frame order is
// the one of reference.md section 8: plain Behavior.Step of every entity, then
// the phases (start, input, update, timers), each over all entities in
// scene-tree order, then the destruction of entities that were destroyed.
func (w *World) Step() error {
	w.Tick++
	w.Each(func(e *Entity) { e.PrevX, e.PrevY = e.X, e.Y })
	var steps []*Entity
	w.Each(func(e *Entity) {
		if e.Behavior != nil {
			steps = append(steps, e)
		}
	})
	if w.scripts != nil {
		w.scripts.beginFrame()
	}
	for _, e := range steps {
		e.Behavior.Step(w, e)
	}
	for ph := PhaseInit; ph <= PhaseTouches; ph++ {
		for _, e := range steps {
			if p, ok := e.Behavior.(Phased); ok {
				p.RunPhase(w, e, ph)
			}
		}
	}
	if w.scripts != nil {
		w.scripts.deliverMessages()
		w.scripts.endFrame()
	}
	w.Each(func(e *Entity) { e.fresh = false })
	w.flushSpawns()
	w.flushDestroyed()
	if w.Input != nil {
		w.Input.Advance()
	}
	return w.syncChunks()
}

// Destroy removes e and everything inside it (its descendants) from the world
// at the end of the current step. Until then they are still alive, as
// reference.md section 6 says.
func (w *World) Destroy(e *Entity) {
	if e == nil || e.Dead || e.pendingDestroy {
		return
	}
	stack := []*Entity{e}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x.pendingDestroy || x.Dead {
			continue
		}
		x.pendingDestroy = true
		w.destroyQ = append(w.destroyQ, x)
		stack = append(stack, x.Children...)
	}
}

// Place puts e at world position (x, y) and moves all its descendants by the
// same shift, so a group carries its members along. Unless teleport is set
// (or e was created in this very step) the previous position is kept, so the
// renderer glides from the old place to the new one; this is the same for
// "x = ..." and "move to" in scripts.
func (w *World) Place(e *Entity, x, y float64, teleport bool) {
	dx, dy := float64(x-e.X), float64(y-e.Y)
	tp := teleport || e.fresh
	e.X, e.Y = x, y
	if tp {
		e.PrevX, e.PrevY = x, y
	}
	stack := append([]*Entity(nil), e.Children...)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		c.X, c.Y = float64(c.X+dx), float64(c.Y+dy)
		if tp {
			c.PrevX, c.PrevY = c.X, c.Y
		}
		stack = append(stack, c.Children...)
	}
	w.geoChanged(e, true)
}

// LocalPos is the position of e relative to its parent group, which is what
// scripts see as x and y.
func (e *Entity) LocalPos() (x, y float64) {
	if e.Parent == nil {
		return e.X, e.Y
	}
	return float64(e.X - e.Parent.X), float64(e.Y - e.Parent.Y)
}

// WorldFromLocal converts a position relative to e's parent group to world
// coordinates.
func (e *Entity) WorldFromLocal(x, y float64) (float64, float64) {
	if e.Parent == nil {
		return x, y
	}
	return float64(e.Parent.X + x), float64(e.Parent.Y + y)
}

func (w *World) flushDestroyed() {
	if len(w.destroyQ) == 0 {
		return
	}
	gone := make(map[*Entity]bool, len(w.destroyQ))
	parents := map[*Entity]bool{}
	for _, e := range w.destroyQ {
		gone[e] = true
		e.Dead = true
		w.forget(e)
		if e.chunk != nil {
			e.chunk.state.Destroyed[e.key] = true
			delete(e.chunk.state.Entities, e.key)
		}
		if e.Parent != nil && !e.Parent.pendingDestroy {
			parents[e.Parent] = true
		}
	}
	w.destroyQ = w.destroyQ[:0]
	for p := range parents {
		for q := p; q != nil; q = q.Parent {
			q.bbOK = false // the group lost members
		}
		kids := p.Children[:0]
		for _, c := range p.Children {
			if !c.Dead {
				kids = append(kids, c)
			}
		}
		clear(p.Children[len(kids):])
		p.Children = kids
	}
	drop := func(list []*Entity) []*Entity {
		out := list[:0]
		for _, e := range list {
			if gone[e] {
				w.live--
				continue
			}
			out = append(out, e)
		}
		clear(list[len(out):])
		return out
	}
	w.mapStatic = drop(w.mapStatic)
	w.content = drop(w.content)
	for _, u := range w.chunks {
		u.ents = drop(u.ents)
	}
	w.collChanged()
}

// forget removes a destroyed or unloaded entity from the indexes.
func (w *World) forget(e *Entity) {
	delete(w.byID, e.ID)
	if e.spawned {
		w.spawnLive--
		e.spawned = false
	}
	if w.scripts != nil {
		w.scripts.eng.Detach(vm.ObjID(e.ID))
	}
}

// flushSpawns lets the objects made by "spawn" in this step join the world.
func (w *World) flushSpawns() {
	if len(w.pending) == 0 {
		return
	}
	for _, e := range w.pending {
		e.pending = false
	}
	w.content = append(w.content, w.pending...)
	w.pending = w.pending[:0]
	w.collChanged()
}

// Entity returns the live entity with the given id, or nil.
func (w *World) Entity(id int) *Entity { return w.byID[id] }
