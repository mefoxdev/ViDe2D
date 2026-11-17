package studio

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/vide2d/vide-studio/internal/project"
	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
)

// document is one open scene, object or map. The Studio keeps the state of the
// current document in its own fields (scene, sel, camera, ...); stash copies
// them here when the user switches tabs.
type document struct {
	path  string // absolute file path; empty if the project has no location
	scene *scene.Scene
	sel   *scene.Node

	originX, originY float64
	zoom             int
	camMoved         bool
	treeScroll       int

	saved []byte // serialized form as last saved/loaded
	dirty bool
}

// Colors of document types (tabs, project browser).
var (
	colDocScene  = colScene
	colDocObject = color.RGBA{0x4e, 0xc9, 0xb0, 0xff}
	colDocMap    = color.RGBA{0xd7, 0xba, 0x7d, 0xff}
)

func docColor(t scene.DocType) color.RGBA {
	switch t {
	case scene.DocObject:
		return colDocObject
	case scene.DocMap:
		return colDocMap
	}
	return colDocScene
}

// docName is the short name shown on tabs.
func (d *document) name() string {
	if d.path != "" {
		return strings.TrimSuffix(filepath.Base(d.path), projfs.DocExt)
	}
	return d.scene.Root.Name
}

func samePath(a, b string) bool {
	return a != "" && b != "" && filepath.Clean(a) == filepath.Clean(b)
}

// relPath converts an absolute document path to a project-relative one ("" if
// it is outside the project folder).
func (s *Studio) relPath(abs string) string {
	if abs == "" || s.proj.dir == "" {
		return ""
	}
	rel, err := filepath.Rel(s.proj.dir, abs)
	if err != nil || !projfs.ValidRel(filepath.ToSlash(rel)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// stash writes the current document's state back to its record.
func (s *Studio) stash() {
	d := s.docs[s.cur]
	d.path, d.scene, d.sel = s.path, s.scene, s.sel
	d.originX, d.originY, d.zoom, d.camMoved = s.originX, s.originY, s.zoom, s.camMoved
	d.treeScroll, d.saved, d.dirty = s.treeScroll, s.saved, s.dirty
}

// load makes d the current document.
func (s *Studio) load(d *document) {
	s.path, s.scene, s.sel = d.path, d.scene, d.sel
	s.originX, s.originY, s.zoom, s.camMoved = d.originX, d.originY, d.zoom, d.camMoved
	s.treeScroll, s.saved, s.dirty = d.treeScroll, d.saved, d.dirty
	s.dragging, s.panning = nil, false
	s.refCache = nil
	s.title = ""
}

func (s *Studio) activate(i int) {
	if i < 0 || i >= len(s.docs) || i == s.cur {
		return
	}
	s.stash()
	s.cur = i
	s.load(s.docs[i])
}

// openDoc opens a project document (by project-relative path) in a tab, or
// switches to its tab if it is already open.
func (s *Studio) openDoc(rel string) {
	abs := projfs.Resolve(s.proj.dir, rel)
	if abs == "" {
		s.Logf("Cannot open %q: not a valid project path.", rel)
		return
	}
	s.stash()
	for i, d := range s.docs {
		if samePath(d.path, abs) {
			s.activate(i)
			return
		}
	}
	sc, err := project.Load(abs)
	if err != nil {
		s.Logf("Cannot open %s: %v", rel, err)
		return
	}
	data, _ := project.Marshal(sc)
	s.docs = append(s.docs, &document{path: abs, scene: sc, sel: sc.Root, zoom: 2, saved: data})
	s.activate(len(s.docs) - 1)
	s.Logf("Opened %s %s", sc.Type, rel)
}

// closeDoc closes a tab, saving it first if it has unsaved changes. The last
// open document cannot be closed.
func (s *Studio) closeDoc(i int) {
	if len(s.docs) <= 1 || i < 0 || i >= len(s.docs) {
		return
	}
	s.stash()
	d := s.docs[i]
	if d.dirty && d.path != "" {
		if err := s.saveDoc(d); err != nil {
			s.Logf("Not closed: %s has unsaved changes and saving failed: %v", d.name(), err)
			return
		}
		s.Logf("Saved %s before closing.", d.name())
	}
	s.docs = append(s.docs[:i], s.docs[i+1:]...)
	switch {
	case i == s.cur:
		s.cur = min(i, len(s.docs)-1)
		s.load(s.docs[s.cur])
	case i < s.cur:
		s.cur--
	}
	s.refCache = nil
}

// saveDoc writes a document to its file. A map whose content sticks out of its
// chunks is refused, because such a file could not be loaded again.
func (s *Studio) saveDoc(d *document) error {
	if d.path == "" {
		return errors.New("this document has no file")
	}
	if msg := mapProblem(d.scene); msg != "" {
		return errors.New(msg)
	}
	data, err := project.Marshal(d.scene)
	if err != nil {
		return err
	}
	if err := project.Save(d.path, data); err != nil {
		return err
	}
	d.saved, d.dirty = data, false
	s.refCache = nil
	return nil
}

// mapProblem describes the first thing that makes a map unsaveable, or "".
func mapProblem(sc *scene.Scene) string {
	if sc.Type != scene.DocMap || !sc.Optimization {
		return "" // free-form maps have no chunk rules
	}
	for _, c := range sc.Root.Children {
		if c.Kind != scene.KindChunk {
			return fmt.Sprintf("a map can only contain chunks, but %q is not one", c.Name)
		}
		if msg := scene.ChunkOverflow(c, sc.ChunkSize()); msg != "" {
			return msg + " (each chunk is " + fmt.Sprint(scene.ChunkTiles) + "×" + fmt.Sprint(scene.ChunkTiles) + " tiles)"
		}
	}
	return ""
}

// newDocFile creates a document file in the standard folder for its type and
// opens it. It returns an error message suitable for showing to the user.
func (s *Studio) newDocFile(typ scene.DocType, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("Enter a name.")
	}
	if len([]rune(name)) > 64 || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("The name is too long or has strange characters.")
	}
	if s.proj.dir == "" {
		return errors.New("This project has no folder, so documents cannot be created.")
	}
	dir := map[scene.DocType]string{scene.DocObject: projfs.ObjectsDir, scene.DocScene: projfs.ScenesDir, scene.DocMap: projfs.MapsDir}[typ]
	rel := dir + "/" + safeFileName(name) + projfs.DocExt
	abs := projfs.Resolve(s.proj.dir, rel)
	if abs == "" {
		return errors.New("Bad name.")
	}
	if _, err := os.Lstat(abs); err == nil {
		return fmt.Errorf("%s already exists.", rel)
	}
	sc := scene.New(name)
	sc.Type = typ
	if typ == scene.DocMap {
		sc.Optimization = true // new maps are optimized: cut into chunks
		c := sc.Add(nil, scene.KindChunk, "Chunk 0,0")
		c.X, c.Y = sc.ChunkOrigin(c)
	}
	data, err := project.Marshal(sc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := project.Save(abs, data); err != nil {
		return err
	}
	s.refreshProject()
	s.openDoc(rel)
	return nil
}

// ---- instances: placed copies of object documents ----

// refScene returns the scene behind an object/map document: the live version if
// it is open in a tab, otherwise a cached copy loaded from disk. nil if it
// cannot be loaded.
func (s *Studio) refScene(rel string) *scene.Scene {
	abs := projfs.Resolve(s.proj.dir, rel)
	if abs == "" {
		return nil
	}
	if samePath(s.path, abs) {
		return s.scene
	}
	for i, d := range s.docs {
		if i != s.cur && samePath(d.path, abs) {
			return d.scene
		}
	}
	if sc, ok := s.refCache[rel]; ok {
		return sc
	}
	if s.refCache == nil {
		s.refCache = map[string]*scene.Scene{}
	}
	sc, err := project.Load(abs)
	if err != nil {
		sc = nil
	}
	s.refCache[rel] = sc
	return sc
}

// reaches reports whether object `from` (transitively) contains an instance of target.
func (s *Studio) reaches(from, target string, depth int) bool {
	if depth > 16 {
		return true // too deep: treat as a cycle
	}
	sc := s.refScene(from)
	if sc == nil {
		return false
	}
	found := false
	sc.Root.Walk(func(n *scene.Node, _ int) {
		if found || n.Kind != scene.KindInstance {
			return
		}
		found = n.Ref == target || s.reaches(n.Ref, target, depth+1)
	})
	return found
}

// canInstance reports whether the current document may contain an instance of ref.
func (s *Studio) canInstance(ref string) bool {
	cur := s.relPath(s.path)
	if cur == "" {
		return true
	}
	return ref != cur && !s.reaches(ref, cur, 0)
}

// listObjects returns the project's object documents (under game/objects).
func (s *Studio) listObjects() []string {
	var out []string
	for _, f := range projfs.Files(s.proj.top, projfs.KindObject) {
		out = append(out, f.Path)
	}
	return out
}

func objectLabel(rel string) string {
	return strings.TrimSuffix(strings.TrimPrefix(rel, projfs.ObjectsDir+"/"), projfs.DocExt)
}

func baseNoExt(rel string) string { return strings.TrimSuffix(path.Base(rel), projfs.DocExt) }

// ---- flattening: everything that is drawn, in draw order ----

// flat is one drawn thing in world coordinates.
type flat struct {
	n      *scene.Node
	owner  *scene.Node // what a click selects; nil for read-only content
	x, y   int
	w, h   int
	inside bool        // part of an instance or of the scene's map
	chunk  *scene.Node // enclosing chunk, when editing a map
	bad    bool        // sprite or text sticks out of its chunk
}

type walker struct {
	s         *Studio
	blueprint bool
	out       []flat
	budget    int
}

const (
	maxFlatItems     = 20000
	maxInstanceDepth = 6
)

func (s *Studio) flatten(blueprint bool) []flat {
	w := &walker{s: s, blueprint: blueprint, budget: maxFlatItems}
	if s.scene.Type == scene.DocScene && s.scene.Map != "" {
		if m := s.refScene(s.scene.Map); m != nil {
			for _, c := range m.Root.Children {
				w.node(c, 0, 0, nil, true, nil, 1, []string{s.scene.Map})
			}
		}
	}
	w.node(s.scene.Root, 0, 0, nil, false, nil, 0, nil)
	return w.out
}

func (w *walker) node(n *scene.Node, ox, oy int, owner *scene.Node, inside bool, chunk *scene.Node, depth int, stack []string) {
	if w.budget <= 0 {
		return
	}
	w.budget--
	x, y := ox+n.X, oy+n.Y
	own := owner
	if own == nil && !inside {
		own = n
	}
	f := flat{n: n, owner: own, x: x, y: y, inside: inside, chunk: chunk}
	switch {
	case n.HasBox() || n.Kind == scene.KindChunk:
		f.w, f.h = n.W, n.H
	case n.Kind == scene.KindInstance:
		bx, by, bw, bh := w.s.instanceBounds(n.Ref, depth, stack)
		f.x, f.y, f.w, f.h = x+bx, y+by, bw, bh
	case w.blueprint:
		f.w, f.h = blueprintNodeSize, blueprintNodeSize
	}
	if chunk != nil && !inside && n.HasBox() && w.s.scene.Optimization {
		size := w.s.scene.ChunkSize()
		lx, ly := x-chunk.X, y-chunk.Y
		f.bad = lx < 0 || ly < 0 || lx+n.W > size || ly+n.H > size
	}
	w.out = append(w.out, f)

	switch n.Kind {
	case scene.KindInstance:
		if depth >= maxInstanceDepth || containsStr(stack, n.Ref) {
			return
		}
		if rs := w.s.refScene(n.Ref); rs != nil {
			ow := own
			for _, c := range rs.Root.Children {
				w.node(c, x, y, ow, true, chunk, depth+1, append(stack[:len(stack):len(stack)], n.Ref))
			}
		}
	default:
		ch := chunk
		if n.Kind == scene.KindChunk && !inside {
			ch = n
		}
		for _, c := range n.Children {
			w.node(c, x, y, owner, inside, ch, depth, stack)
		}
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// instanceBounds returns the bounding box (relative to the instance position)
// of an object's content, or a small default box if it has none.
func (s *Studio) instanceBounds(ref string, depth int, stack []string) (x, y, w, h int) {
	const def = 32
	if depth >= maxInstanceDepth || containsStr(stack, ref) {
		return 0, 0, def, def
	}
	rs := s.refScene(ref)
	if rs == nil {
		return 0, 0, def, def
	}
	wk := &walker{s: s, blueprint: false, budget: 2000}
	st := append(stack[:len(stack):len(stack)], ref)
	for _, c := range rs.Root.Children {
		wk.node(c, 0, 0, nil, true, nil, depth+1, st)
	}
	minX, minY, maxX, maxY := 1<<30, 1<<30, -(1 << 30), -(1 << 30)
	for _, f := range wk.out {
		if f.w <= 0 || f.h <= 0 {
			continue
		}
		minX, minY = min(minX, f.x), min(minY, f.y)
		maxX, maxY = max(maxX, f.x+f.w), max(maxY, f.y+f.h)
	}
	if maxX <= minX || maxY <= minY {
		return 0, 0, def, def
	}
	return minX, minY, maxX - minX, maxY - minY
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// cutIntoChunks moves the loose top-level objects of a map into chunks, so
// that the map can be optimized. An object that would cross a chunk border
// cannot be placed: nothing is changed then, and the names of the offending
// objects are returned. It also returns how many objects were moved.
func (s *Studio) cutIntoChunks() (moved int, problems []string) {
	sc := s.scene
	size := sc.ChunkSize()
	type plan struct {
		n      *scene.Node
		cx, cy int
	}
	var plans []plan
	for _, n := range sc.Root.Children {
		if n.Kind == scene.KindChunk {
			continue
		}
		w := &walker{s: s, budget: 5000}
		w.node(n, 0, 0, nil, false, nil, 0, nil)
		minX, minY, maxX, maxY := n.X, n.Y, n.X+1, n.Y+1
		first := true
		for _, f := range w.out {
			if f.w <= 0 || f.h <= 0 {
				continue
			}
			if first {
				minX, minY, maxX, maxY, first = f.x, f.y, f.x+f.w, f.y+f.h, false
				continue
			}
			minX, minY = min(minX, f.x), min(minY, f.y)
			maxX, maxY = max(maxX, f.x+f.w), max(maxY, f.y+f.h)
		}
		cx, cy := floorDiv(minX, size), floorDiv(minY, size)
		if floorDiv(maxX-1, size) != cx || floorDiv(maxY-1, size) != cy || cx < -4096 || cx > 4096 || cy < -4096 || cy > 4096 {
			problems = append(problems, n.Name)
			continue
		}
		plans = append(plans, plan{n, cx, cy})
	}
	if len(problems) > 0 {
		return 0, problems
	}
	chunks := map[[2]int]*scene.Node{}
	for _, c := range sc.Root.Children {
		if c.Kind == scene.KindChunk {
			chunks[[2]int{c.CX, c.CY}] = c
		}
	}
	for _, p := range plans {
		key := [2]int{p.cx, p.cy}
		c := chunks[key]
		if c == nil {
			c = sc.Add(nil, scene.KindChunk, "Chunk "+strconv.Itoa(p.cx)+","+strconv.Itoa(p.cy))
			c.CX, c.CY = p.cx, p.cy
			c.X, c.Y = sc.ChunkOrigin(c)
			chunks[key] = c
		}
		sc.Reparent(p.n, c)
		p.n.X -= c.X
		p.n.Y -= c.Y
		moved++
	}
	return moved, nil
}
