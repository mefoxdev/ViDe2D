// Package studio is the ViDe-studio shell: scene tree, viewport, inspector, console.
package studio

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/vide2d/vide-studio/internal/assets"
	"github.com/vide2d/vide-studio/internal/dock"
	"github.com/vide2d/vide-studio/internal/project"
	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	toolbarH = 40
	rowH     = 24

	// Placeholder game screen size until project settings exist.
	gameW, gameH = 320, 180
)

var palette = []color.RGBA{
	{0xe5, 0x4b, 0x4b, 0xff}, {0xf2, 0xa5, 0x3c, 0xff}, {0xf0, 0xdc, 0x4a, 0xff},
	{0x5c, 0xc9, 0x6b, 0xff}, {0x4f, 0x9d, 0xff, 0xff}, {0x9b, 0x6b, 0xff, 0xff},
	{0xe0, 0x6b, 0xc4, 0xff}, {0xe2, 0xe5, 0xec, 0xff},
}

// ViewMode selects how the viewport shows the scene.
type ViewMode int

const (
	// Mode2D shows sprites as they will look in the game.
	Mode2D ViewMode = iota
	// ModeBlueprint shows every object as a labelled box with its size.
	ModeBlueprint
)

// blueprintNodeSize is the box size used for objects without a size of their own.
const blueprintNodeSize = 32

var templateColor = color.RGBA{0xff, 0x9f, 0x43, 0xff}

// Studio implements ebiten.Game.
type Studio struct {
	ui    *ui.Context
	scene *scene.Scene
	sel   *scene.Node
	log   []string

	w, h int

	// viewport camera: screen = origin + world*zoom
	originX, originY float64
	zoom             int
	camMoved         bool // true once the user pans/zooms; until then the view stays centered
	panning          bool
	dragging         *scene.Node
	grabX, grabY     int

	treeScroll int
	mode       ViewMode
	dock       *dock.Dock
	tex        *textures
	code       code
	proj       projState
	vp         image.Rectangle // viewport content rect (below the tabs) from the last frame

	docs     []*document // open documents (tabs); the current one lives in the fields of Studio
	cur      int
	refCache map[string]*scene.Scene // object/map documents loaded for instances
	menu     objMenu
	dlg      newDlg

	game player // the running game, if any

	closeWarnAt int // tick of the last "unsaved changes" warning on close; 0 if none

	path  string // project file; empty disables saving
	saved []byte // serialized scene as last saved/loaded
	dirty bool
	title string
	tick  int
}

// New creates a studio. If path exists it is loaded, otherwise a small demo
// scene is created and path is where Save writes. An unreadable file is never
// overwritten: saving is disabled until the problem is fixed.
func New(path string) *Studio {
	if path != "" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	s := &Studio{ui: ui.New(), zoom: 2, path: path}
	s.dock = dock.New(loadLayout(), panelTitles)
	s.proj.dir = projDirFor(path)
	s.tex = newTextures(s.proj.dir)
	loaded := false
	if path != "" {
		sc, err := s.loadMain(path)
		switch {
		case err == nil:
			s.scene, loaded = sc, true
			s.Logf("Opened %s", path)
			if msg := s.nestedProblem(); msg != "" {
				s.Logf("Warning: %s", msg)
			}
		case errors.Is(err, os.ErrNotExist):
			s.Logf("New project: %s (Ctrl+S to save)", path)
		default:
			s.Logf("Cannot open %s: %v", path, err)
			s.Logf("Saving is disabled so the file is not overwritten.")
			s.path = ""
		}
	}
	if !loaded {
		s.scene = demoScene()
	}
	s.sel = s.scene.Root
	if !loaded && len(s.scene.Root.Children) > 0 {
		s.sel = s.scene.Root.Children[0]
	}
	if data, err := project.Marshal(s.scene); err == nil && loaded {
		s.saved = data
	}
	s.docs = []*document{{}}
	s.stash()
	s.refreshProject()
	s.Logf("Middle mouse / Space+drag: pan, wheel: zoom, drag objects to move, F: center view, Tab: 2D/Blueprint.")
	return s
}

func demoScene() *scene.Scene {
	sc := scene.New("Main")
	hero := sc.Add(nil, scene.KindSprite, "Hero")
	hero.X, hero.Y, hero.Color = 40, 60, palette[4]
	coin := sc.Add(nil, scene.KindSprite, "Coin")
	coin.X, coin.Y, coin.W, coin.H, coin.Color = 200, 90, 8, 8, palette[2]
	return sc
}

func (s *Studio) save() {
	if s.path == "" {
		s.Logf("Saving is disabled.")
		return
	}
	s.stash()
	d := s.docs[s.cur]
	if err := s.saveDoc(d); err != nil {
		s.Logf("Save failed: %v", err)
		return
	}
	s.saved, s.dirty = d.saved, false
	if rel := s.relPath(s.path); rel != "" {
		s.Logf("Saved %s", rel)
	} else {
		s.Logf("Saved %s", s.path)
	}
}

// loadMain loads the document the studio was given: through the project
// folder (links refused, size limited like the runtime's). A missing file
// reports os.ErrNotExist.
func (s *Studio) loadMain(file string) (*scene.Scene, error) {
	rel, err := projfs.RelTo(s.proj.dir, file)
	if err != nil {
		return nil, err
	}
	return s.loadDoc(rel)
}

func (s *Studio) revert() {
	sc, err := s.loadMain(s.path)
	if err != nil {
		s.Logf("Revert failed: %v", err)
		return
	}
	s.scene, s.sel, s.dragging = sc, sc.Root, nil
	s.saved, _ = project.Marshal(sc)
	s.dirty = false
	s.refCache = nil
	s.Logf("Reverted to %s", s.path)
}

// computeDirty compares the current document with what was last saved.
func (s *Studio) computeDirty() {
	data, err := project.Marshal(s.scene)
	s.dirty = err != nil || !bytes.Equal(data, s.saved)
}

func (s *Studio) updateDirty() {
	if s.tick++; s.tick%20 != 0 {
		return
	}
	s.computeDirty()
	title := "ViDe-studio"
	if s.path != "" {
		title += " — " + filepath.Base(s.path)
	}
	if s.dirty {
		title += " *"
	}
	if title != s.title {
		s.title = title
		ebiten.SetWindowTitle(title)
	}
}

// Logf appends a line to the console.
func (s *Studio) Logf(format string, a ...any) {
	s.log = append(s.log, fmt.Sprintf(format, a...))
	if len(s.log) > 200 {
		s.log = s.log[len(s.log)-200:]
	}
}

// Layout makes the logical screen equal to the window size.
func (s *Studio) Layout(w, h int) (int, int) { s.w, s.h = w, h; return w, h }

// Draw replays the UI built in Update.
func (s *Studio) Draw(dst *ebiten.Image) { s.ui.Render(dst) }

// Update builds the UI for this frame and handles interaction.
func (s *Studio) Update() error {
	u := s.ui
	u.Begin()
	if ebiten.IsWindowBeingClosed() && s.confirmClose() {
		return ebiten.Termination
	}
	// While a menu or dialog is open the panels must not see clicks or scrolling.
	modal := s.modalOpen()
	pressed, wheel := u.Pressed, u.Wheel
	if modal {
		u.Pressed, u.Wheel = false, 0
		s.code.focus, u.External = false, false
	}

	toolbar := image.Rect(0, 0, s.w, toolbarH)
	rects := s.dock.Update(u, image.Rect(0, toolbarH, s.w, s.h))

	s.vp = image.Rectangle{}
	if r, ok := rects[panelViewport]; ok {
		body := s.docTabs(r)
		s.vp = body
		s.viewport(body)
	}
	s.toolbar(toolbar)
	if r, ok := rects[panelScene]; ok {
		s.treePanel(r)
	}
	if r, ok := rects[panelInspector]; ok {
		s.inspector(r)
	}
	if r, ok := rects[panelConsole]; ok {
		s.console(r)
	}
	if r, ok := rects[panelCode]; ok {
		s.codePanel(r)
	} else {
		s.code.focus = false
		u.External = false
	}
	if r, ok := rects[panelProject]; ok {
		s.projPanel(r)
	}
	s.projDrag()
	s.dock.DrawOverlay(u)
	if s.dock.Changed() {
		if err := saveLayout(s.dock.Root); err != nil {
			s.Logf("Layout not saved: %v", err)
		}
	}
	if !modal {
		if inpututil.IsKeyJustPressed(ebiten.KeyTab) && !u.Focused() {
			s.mode = 1 - s.mode
		}
		if ebiten.IsKeyPressed(ebiten.KeyControl) && inpututil.IsKeyJustPressed(ebiten.KeyS) && !u.Focused() {
			s.save()
		}
	}
	u.Pressed, u.Wheel = pressed, wheel
	s.modalUI()
	s.updateDirty()
	s.pollPlay()
	return nil
}

// confirmClose decides whether the window may close. The first request with
// unsaved changes is refused with a warning; asking again within ten seconds
// discards them.
func (s *Studio) confirmClose() bool {
	s.computeDirty()
	s.stash()
	var names []string
	for _, d := range s.docs {
		if d.dirty {
			names = append(names, d.name())
		}
	}
	if s.code.dirty() {
		names = append(names, "script "+s.code.rel)
	}
	if len(names) == 0 || (s.closeWarnAt != 0 && s.tick-s.closeWarnAt < 600) {
		return true
	}
	s.closeWarnAt = s.tick
	s.Logf("Unsaved changes: %s. Save them, or close the window again to discard.", strings.Join(names, ", "))
	return false
}

func (s *Studio) toolbar(r image.Rectangle) {
	u := s.ui
	u.Fill(r, u.Theme.PanelHead)
	x := r.Min.X + 8
	btn := func(label string, w int, enabled bool) bool {
		b := image.Rect(x, r.Min.Y+6, x+w, r.Max.Y-6)
		x += w + 6
		return u.Button(b, label, enabled)
	}
	x += 20
	for _, m := range []struct {
		label string
		mode  ViewMode
	}{{"2D", Mode2D}, {"Blueprint", ModeBlueprint}} {
		w := 70
		if m.mode == ModeBlueprint {
			w = 90
		}
		b := image.Rect(x, r.Min.Y+6, x+w, r.Max.Y-6)
		x += w + 6
		if u.Toggle(b, m.label, s.mode == m.mode) {
			s.mode = m.mode
		}
	}
	x += 14
	for _, m := range []struct {
		label string
		mode  scene.ImageMode
		w     int
	}{{"Pixel", scene.ImagePixel, 64}, {"Normal", scene.ImageNormal, 74}} {
		b := image.Rect(x, r.Min.Y+6, x+m.w, r.Max.Y-6)
		x += m.w + 6
		if u.Toggle(b, m.label, s.scene.ImageMode == m.mode) {
			s.scene.ImageMode = m.mode
			s.Logf("Images: %s", m.mode)
		}
	}
	x += 14
	if btn("Layout", 70, true) {
		s.dock.Root = defaultLayout()
		if err := saveLayout(s.dock.Root); err != nil {
			s.Logf("Layout not saved: %v", err)
		}
		s.Logf("Layout reset. Drag panel tabs to re-dock, drag the gaps between panels to resize.")
	}
	x += 14
	if btn("Save", 70, s.path != "") {
		s.save()
	}
	if btn("Revert", 70, s.path != "") {
		s.revert()
	}
	x += 20
	if btn("Play", 70, true) {
		s.play()
	}
	if s.w < 1300 {
		return
	}
	u.Text(image.Rect(s.w-260, r.Min.Y, s.w-8, r.Max.Y), "scene: "+s.scene.Root.Name+map[bool]string{true: " *", false: ""}[s.dirty], u.Theme.TextDim)
}

func (s *Studio) treePanel(r image.Rectangle) {
	u := s.ui
	foot := s.objectFooter(r)
	c := image.Rect(r.Min.X, r.Min.Y, r.Max.X, foot.Min.Y)
	if u.Hover(c) && u.Wheel != 0 {
		s.treeScroll -= int(u.Wheel * 3 * rowH / 2)
	}
	total := 0
	s.scene.Root.Walk(func(*scene.Node, int) { total++ })
	maxScroll := max(0, total*rowH-c.Dy()+8)
	s.treeScroll = min(max(s.treeScroll, 0), maxScroll)

	y := c.Min.Y + 4 - s.treeScroll
	clip := c
	s.scene.Root.Walk(func(n *scene.Node, depth int) {
		row := image.Rect(c.Min.X, y, c.Max.X, y+rowH)
		y += rowH
		if row.Max.Y < c.Min.Y || row.Min.Y > c.Max.Y {
			return
		}
		vis := row.Intersect(clip)
		if u.Pressed && u.Hover(vis) {
			s.sel = n
		}
		if n == s.sel {
			u.Fill(vis, color.RGBA{0x2f, 0x4a, 0x6e, 0xff})
		} else if u.Hover(vis) {
			u.Fill(vis, u.Theme.PanelHead)
		}
		icon := "◦"
		switch n.Kind {
		case scene.KindSprite:
			icon = "■"
		case scene.KindText:
			icon = "T"
		case scene.KindInstance:
			icon = "●"
		case scene.KindChunk:
			icon = "□"
		}
		label := fmt.Sprintf("%s %s%s", icon, n.Name, marks(n))
		if n.Kind == scene.KindInstance {
			label += "  → " + baseNoExt(n.Ref)
		}
		tr := image.Rect(row.Min.X+8+depth*16, row.Min.Y, row.Max.X, row.Max.Y)
		s.clippedText(clip, tr, label, u.Theme.Text)
	})
}

// clippedText draws text only inside clip.
func (s *Studio) clippedText(clip, r image.Rectangle, text string, col color.RGBA) {
	u := s.ui
	u.Emit(func(d *ebiten.Image) {
		sub := d.SubImage(clip).(*ebiten.Image)
		u.DrawText(sub, text, float64(r.Min.X), float64(r.Min.Y+(r.Dy()-ui.FontSize)/2-1), col)
	})
}

func (s *Studio) inspector(r image.Rectangle) {
	u := s.ui
	c := r
	n := s.sel
	if n == nil {
		u.Text(image.Rect(c.Min.X+8, c.Min.Y+4, c.Max.X, c.Min.Y+4+ui.LineHeight), "Nothing selected", u.Theme.TextDim)
		return
	}
	y := c.Min.Y + 6
	line := func() image.Rectangle {
		r := image.Rect(c.Min.X+8, y, c.Max.X-8, y+ui.LineHeight)
		y += ui.LineHeight + 4
		return r
	}
	if name, ok := u.Field(fmt.Sprintf("name%d", n.ID), line(), n.Name, false); ok && s.scene.Rename(n, name) {
		s.Logf("Renamed to %s", n.Name)
	}
	u.Text(line(), "Type: "+string(n.Kind), u.Theme.TextDim)
	if n == s.scene.Root {
		s.docSettings(n, line)
	}
	y += 6
	step := 1
	if u.Shift {
		step = 8
	}
	switch n.Kind {
	case scene.KindChunk:
		cx, cy := n.CX, n.CY
		s.stepper(n.ID, line(), "Chunk X", &cx, 1)
		s.stepper(n.ID, line(), "Chunk Y", &cy, 1)
		if cx != n.CX || cy != n.CY {
			s.moveChunk(n, cx, cy)
		}
		u.Text(line(), fmt.Sprintf("%d×%d tiles (%d×%d px)", scene.ChunkTiles, scene.ChunkTiles, n.W, n.H), u.Theme.TextDim)
	default:
		s.stepper(n.ID, line(), "X", &n.X, step)
		s.stepper(n.ID, line(), "Y", &n.Y, step)
	}
	if n.Kind == scene.KindInstance {
		rr := line()
		u.Text(image.Rect(rr.Min.X, rr.Min.Y, rr.Min.X+60, rr.Max.Y), "Object", u.Theme.TextDim)
		label, col := objectLabel(n.Ref), colDocObject
		if s.refScene(n.Ref) == nil {
			label, col = "missing: "+n.Ref, color.RGBA{0xff, 0x6b, 0x6b, 0xff}
		}
		ob := image.Rect(rr.Max.X-56, rr.Min.Y, rr.Max.X, rr.Max.Y)
		u.Text(image.Rect(rr.Min.X+62, rr.Min.Y, ob.Min.X-4, rr.Max.Y), u.FitText(label, float64(ob.Min.X-rr.Min.X-70)), col)
		if u.Button(ob, "Open", s.refScene(n.Ref) != nil) {
			s.openDoc(n.Ref)
		}
	}
	if n.Kind == scene.KindText {
		fr := line()
		u.Text(image.Rect(fr.Min.X, fr.Min.Y, fr.Min.X+60, fr.Max.Y), "Text", u.Theme.TextDim)
		if v, ok := u.Field(fmt.Sprintf("text%d", n.ID), image.Rect(fr.Min.X+62, fr.Min.Y, fr.Max.X, fr.Max.Y), n.Text, false); ok && len([]rune(v)) <= 10000 {
			n.Text = v
		}
		s.stepper(n.ID, line(), "Font", &n.FontSize, 1)
		n.FontSize = min(max(n.FontSize, 1), 256)
	}
	if n.HasBox() {
		s.stepper(n.ID, line(), "Width", &n.W, step)
		s.stepper(n.ID, line(), "Height", &n.H, step)
		n.W, n.H = max(n.W, 1), max(n.H, 1)
		cr := line()
		u.Text(image.Rect(cr.Min.X, cr.Min.Y, cr.Min.X+60, cr.Max.Y), "Color", u.Theme.TextDim)
		sw := image.Rect(cr.Max.X-78, cr.Min.Y, cr.Max.X, cr.Max.Y)
		u.Fill(sw, n.Color)
		if u.Hover(sw) && u.Pressed {
			for i, p := range palette {
				if p == n.Color {
					n.Color = palette[(i+1)%len(palette)]
					break
				}
			}
		}
	}
	if n.Kind == scene.KindSprite {
		tr := line()
		u.Text(image.Rect(tr.Min.X, tr.Min.Y, tr.Min.X+60, tr.Max.Y), "Texture", u.Theme.TextDim)
		val, col := "none", u.Theme.TextDim
		if n.Texture != "" {
			val, col = n.Texture, u.Theme.Text
			if s.tex.get(n.Texture) == nil {
				val, col = "missing: "+n.Texture, color.RGBA{0xff, 0x6b, 0x6b, 0xff}
			}
			if u.Button(image.Rect(tr.Max.X-24, tr.Min.Y, tr.Max.X, tr.Max.Y), "×", true) {
				n.Texture = ""
			}
		}
		u.Text(image.Rect(tr.Min.X+62, tr.Min.Y, tr.Max.X-28, tr.Max.Y), u.FitText(val, float64(tr.Dx()-92)), col)
	}

	// scripting
	y += 6
	tg := line()
	u.Text(image.Rect(tg.Min.X, tg.Min.Y, tg.Min.X+60, tg.Max.Y), "Tag", u.Theme.TextDim)
	if v, ok := u.Field(fmt.Sprintf("tag%d", n.ID), image.Rect(tg.Min.X+62, tg.Min.Y, tg.Max.X, tg.Max.Y), n.Tag, false); ok {
		if v = strings.TrimSpace(v); validTag(v) {
			n.Tag = v
		} else {
			s.Logf("Tags can have at most 64 characters and no control characters.")
		}
	}
	sr := line()
	u.Text(image.Rect(sr.Min.X, sr.Min.Y, sr.Min.X+60, sr.Max.Y), "Script", u.Theme.TextDim)
	if v, ok := u.Field(fmt.Sprintf("script%d", n.ID), image.Rect(sr.Min.X+62, sr.Min.Y, sr.Max.X, sr.Max.Y), n.Script, false); ok {
		if v = strings.TrimSpace(v); v == "" || assets.ValidRelPath(v, scriptExt) {
			n.Script = v
		} else {
			s.Logf("Script path must be like \"hero.tdsl\" or \"enemies/slime.tdsl\" (inside the scripts folder).")
		}
	}
	br := line()
	bx := br.Max.X
	sbtn := func(label string, w int, enabled bool) bool {
		bx -= w
		b := image.Rect(bx, br.Min.Y, bx+w, br.Max.Y)
		bx -= 6
		return u.Button(b, label, enabled)
	}
	if sbtn("Clear", 56, n.Script != "") {
		n.Script = ""
	}
	if sbtn("Open", 56, n.Script != "" && s.scriptExists(n.Script)) {
		s.openInCode(n.Script)
	}
	if sbtn("New", 56, n.Script == "") {
		if err := s.newScript(n); err != nil {
			s.Logf("Cannot create script: %v", err)
		} else {
			s.Logf("Created scripts/%s for %s", n.Script, n.Name)
		}
	}
	if n.Script != "" && !s.scriptExists(n.Script) {
		u.Text(image.Rect(br.Min.X, br.Min.Y, bx, br.Max.Y), "missing file", color.RGBA{0xff, 0x6b, 0x6b, 0xff})
	}
	if n.Parent != nil {
		tr := line()
		u.Text(image.Rect(tr.Min.X, tr.Min.Y, tr.Min.X+70, tr.Max.Y), "Template", u.Theme.TextDim)
		label := "no"
		if n.Template {
			label = "yes"
		}
		if u.Toggle(image.Rect(tr.Max.X-56, tr.Min.Y, tr.Max.X, tr.Max.Y), label, n.Template) {
			n.Template = !n.Template
		}
	}
	y += 6
	u.Text(line(), "Shift: step 8 • Enter: apply", u.Theme.TextDim)
}

func (s *Studio) stepper(id int, r image.Rectangle, label string, v *int, step int) {
	u := s.ui
	u.Text(image.Rect(r.Min.X, r.Min.Y, r.Min.X+60, r.Max.Y), label, u.Theme.TextDim)
	minus := image.Rect(r.Max.X-120, r.Min.Y, r.Max.X-96, r.Max.Y)
	plus := image.Rect(r.Max.X-24, r.Min.Y, r.Max.X, r.Max.Y)
	val := image.Rect(minus.Max.X+2, r.Min.Y, plus.Min.X-2, r.Max.Y)
	if str, ok := u.Field(fmt.Sprintf("%s%d", label, id), val, fmt.Sprint(*v), true); ok {
		if x, err := strconv.Atoi(str); err == nil {
			*v = x
		}
	}
	if u.Button(minus, "-", true) {
		*v -= step
	}
	if u.Button(plus, "+", true) {
		*v += step
	}
}

func (s *Studio) console(r image.Rectangle) {
	u := s.ui
	c := r
	lines := (c.Dy() - 8) / ui.LineHeight
	start := max(0, len(s.log)-lines)
	for i, l := range s.log[start:] {
		lr := image.Rect(c.Min.X+8, c.Min.Y+4+i*ui.LineHeight, c.Max.X, c.Min.Y+4+(i+1)*ui.LineHeight)
		u.Text(lr, l, u.Theme.Text)
	}
}

// ---- viewport ----

type item struct {
	r        image.Rectangle // screen rect
	n        *scene.Node
	selected bool
	img      *ebiten.Image // sprite texture, if loaded
	missing  bool          // texture set but could not be loaded
	inside   bool          // part of an instance or of the scene's map (read-only)
	bad      bool          // sticks out of its map chunk
}

func (s *Studio) toWorld(x, y int) (int, int) {
	return int(math.Floor((float64(x) - s.originX) / float64(s.zoom))),
		int(math.Floor((float64(y) - s.originY) / float64(s.zoom)))
}

func (s *Studio) viewport(r image.Rectangle) {
	u := s.ui
	if (!s.camMoved || (inpututil.IsKeyJustPressed(ebiten.KeyF) && !u.Focused())) && r.Dx() > 0 {
		s.camMoved = false
		s.originX = float64(r.Min.X) + float64(r.Dx()-gameW*s.zoom)/2
		s.originY = float64(r.Min.Y) + float64(r.Dy()-gameH*s.zoom)/2
	}
	hover := u.Hover(r)

	// zoom around the cursor
	if hover && u.Wheel != 0 {
		nz := min(max(s.zoom+int(math.Copysign(1, u.Wheel)), 1), 16)
		if nz != s.zoom {
			s.camMoved = true
			wx := (float64(u.MX) - s.originX) / float64(s.zoom)
			wy := (float64(u.MY) - s.originY) / float64(s.zoom)
			s.zoom = nz
			s.originX = float64(u.MX) - wx*float64(nz)
			s.originY = float64(u.MY) - wy*float64(nz)
		}
	}
	// pan
	if hover && (u.MiddlePressed || (u.Space && u.Pressed)) {
		s.panning = true
		s.camMoved = true
	}
	if s.panning {
		if u.MiddleDown || (u.Space && u.Down) {
			s.originX += float64(u.DX)
			s.originY += float64(u.DY)
		} else {
			s.panning = false
		}
	}
	// select / move
	wx, wy := s.toWorld(u.MX, u.MY)
	if hover && u.Pressed && !s.panning {
		hit := s.hit(wx, wy)
		if hit != nil {
			s.sel = hit
			if hit.Kind != scene.KindChunk { // chunk positions follow their coordinates
				s.dragging = hit
				px, py := hit.WorldPos()
				s.grabX, s.grabY = wx-px, wy-py
			}
		} else {
			s.sel = nil
		}
	}
	if s.dragging != nil {
		if u.Down {
			pwx, pwy := 0, 0
			if p := s.dragging.Parent; p != nil {
				pwx, pwy = p.WorldPos()
			}
			nx, ny := wx-s.grabX, wy-s.grabY
			if ch := chunkOf(s.dragging); ch != nil && s.dragging.HasBox() && s.scene.Optimization {
				// objects of a map stay inside their chunk
				size := s.scene.ChunkSize()
				nx = min(max(nx, ch.X), ch.X+size-s.dragging.W)
				ny = min(max(ny, ch.Y), ch.Y+size-s.dragging.H)
			}
			s.dragging.X = nx - pwx
			s.dragging.Y = ny - pwy
		} else {
			s.dragging = nil
		}
	}

	// snapshot for drawing
	var items []item
	z := s.zoom
	for _, f := range s.flatten(s.mode == ModeBlueprint) {
		it := item{
			r: image.Rect(int(s.originX)+f.x*z, int(s.originY)+f.y*z, int(s.originX)+(f.x+f.w)*z, int(s.originY)+(f.y+f.h)*z),
			n: f.n, selected: !f.inside && f.n == s.sel, inside: f.inside, bad: f.bad,
		}
		if f.n.Kind == scene.KindSprite && f.n.Texture != "" {
			if it.img = s.tex.get(f.n.Texture); it.img == nil {
				it.missing = true
			}
		}
		items = append(items, it)
	}
	ox, oy, mode := int(s.originX), int(s.originY), s.mode
	u.Emit(func(d *ebiten.Image) {
		sub := d.SubImage(r).(*ebiten.Image)
		if mode == ModeBlueprint {
			s.drawBlueprint(sub, r, ox, oy, z, items)
		} else {
			s.drawViewport(sub, r, ox, oy, z, items)
		}
	})
}

// chunkOf returns the map chunk that contains n, if any.
func chunkOf(n *scene.Node) *scene.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Kind == scene.KindChunk {
			return p
		}
	}
	return nil
}

// hit returns the topmost selectable node at a world position. Content inside
// an instance selects the instance; the scene's map background is not selectable.
func (s *Studio) hit(wx, wy int) *scene.Node {
	var found *scene.Node
	for _, f := range s.flatten(s.mode == ModeBlueprint) {
		if f.owner == nil || f.w <= 0 || f.h <= 0 {
			continue
		}
		if wx >= f.x && wx < f.x+f.w && wy >= f.y && wy < f.y+f.h {
			found = f.owner // later in draw order = on top
		}
	}
	return found
}

// gridSize is the tile size of the current document.
func (s *Studio) gridSize() int { return max(s.scene.TileSize, 4) }

func (s *Studio) drawViewport(d *ebiten.Image, r image.Rectangle, ox, oy, z int, items []item) {
	t := s.ui.Theme
	ui.FillRect(d, r, color.RGBA{0x16, 0x17, 0x1c, 0xff})
	// game screen area (scenes only)
	game := image.Rect(ox, oy, ox+gameW*z, oy+gameH*z)
	if s.scene.Type == scene.DocScene {
		ui.FillRect(d, game, color.RGBA{0x20, 0x22, 0x2a, 0xff})
	}
	// grid
	step := s.gridSize() * z
	for x := ox - ((ox-r.Min.X)/step+1)*step; x < r.Max.X; x += step {
		ui.FillRect(d, image.Rect(x, r.Min.Y, x+1, r.Max.Y), t.Grid)
	}
	for y := oy - ((oy-r.Min.Y)/step+1)*step; y < r.Max.Y; y += step {
		ui.FillRect(d, image.Rect(r.Min.X, y, r.Max.X, y+1), t.Grid)
	}
	if s.scene.Type == scene.DocScene {
		ui.StrokeRect(d, game, color.RGBA{0x4f, 0x9d, 0xff, 0x90})
		s.ui.DrawText(d, fmt.Sprintf("game %dx%d", gameW, gameH), float64(game.Min.X+4), float64(game.Min.Y-20), t.TextDim)
	}

	for _, it := range items {
		switch it.n.Kind {
		case scene.KindChunk:
			ui.StrokeRect(d, it.r, colDocMap)
			ui.StrokeRect(d, it.r.Inset(1), colDocMap)
			if it.selected {
				ui.StrokeRect(d, it.r.Inset(-2), t.Accent)
			}
			if !it.inside {
				s.ui.DrawText(d, it.n.Name, float64(it.r.Min.X+6), float64(it.r.Min.Y+4), colDocMap)
			}
			continue
		case scene.KindInstance:
			ui.StrokeRect(d, it.r, colDocObject)
			if it.selected {
				ui.StrokeRect(d, it.r.Inset(-2), t.Accent)
			}
			if !it.inside {
				s.ui.DrawText(d, it.n.Name+" → "+baseNoExt(it.n.Ref), float64(it.r.Min.X), float64(it.r.Min.Y-18), colDocObject)
			}
			continue
		}
		if it.n.Kind == scene.KindText {
			if clip := it.r.Intersect(r); !clip.Empty() {
				size := float64(it.n.FontSize * z)
				s.ui.DrawTextSize(d.SubImage(clip).(*ebiten.Image), it.n.Text, float64(it.r.Min.X), float64(it.r.Min.Y), size, it.n.Color)
			}
			ui.StrokeRect(d, it.r, color.RGBA{0x4a, 0x4f, 0x5c, 0xff})
			if it.selected {
				ui.StrokeRect(d, it.r.Inset(-2), t.Accent)
			}
			if !it.inside {
				s.ui.DrawText(d, it.n.Name+marks(it.n), float64(it.r.Min.X), float64(it.r.Min.Y-18), t.TextDim)
			}
		} else if it.n.Kind == scene.KindSprite {
			switch {
			case it.img != nil:
				drawImage(d, it.img, it.r, s.scene.ImageMode)
			case it.missing:
				ui.FillRect(d, it.r, color.RGBA{0xff, 0x00, 0xff, 0xff})
			default:
				ui.FillRect(d, it.r, it.n.Color)
			}
			if it.selected {
				ui.StrokeRect(d, it.r.Inset(-2), t.Accent)
			}
			if !it.inside {
				s.ui.DrawText(d, it.n.Name+marks(it.n), float64(it.r.Min.X), float64(it.r.Min.Y-18), t.TextDim)
			}
		} else {
			p := it.r.Min
			ui.FillRect(d, image.Rect(p.X-5, p.Y, p.X+6, p.Y+1), t.TextDim)
			ui.FillRect(d, image.Rect(p.X, p.Y-5, p.X+1, p.Y+6), t.TextDim)
			if it.selected {
				ui.StrokeRect(d, image.Rect(p.X-7, p.Y-7, p.X+8, p.Y+8), t.Accent)
			}
		}
	}
	for _, it := range items {
		if it.n.Template && it.n.HasBox() {
			ui.StrokeRect(d, it.r.Inset(-4), templateColor)
		}
		if it.bad {
			ui.StrokeRect(d, it.r.Inset(-3), color.RGBA{0xff, 0x6b, 0x6b, 0xff})
		}
	}
	s.ui.DrawText(d, fmt.Sprintf("zoom %dx", z), float64(r.Min.X+8), float64(r.Max.Y-22), t.TextDim)
}

var (
	bpBg      = color.RGBA{0x0e, 0x22, 0x38, 0xff}
	bpGrid    = color.RGBA{0x14, 0x32, 0x52, 0xff}
	bpGridMaj = color.RGBA{0x1b, 0x42, 0x6b, 0xff}
	bpFill    = color.RGBA{0x12, 0x36, 0x5c, 0xff}
	bpLine    = color.RGBA{0x5f, 0xb0, 0xff, 0xff}
	bpSel     = color.RGBA{0xff, 0xff, 0xff, 0xff}
	bpText    = color.RGBA{0xcf, 0xe6, 0xff, 0xff}
	bpDim     = color.RGBA{0x7f, 0xa8, 0xd4, 0xff}
)

const bpLineH = 16

// drawBlueprint draws every object as a box: name, id and size inside it.
func (s *Studio) drawBlueprint(d *ebiten.Image, r image.Rectangle, ox, oy, z int, items []item) {
	u := s.ui
	ui.FillRect(d, r, bpBg)
	step := s.gridSize() * z
	// grid; every 4th line is brighter, aligned to the world origin
	i0 := -((ox - r.Min.X) / step) - 1
	for i, x := i0, ox+i0*step; x < r.Max.X; i, x = i+1, x+step {
		col := bpGrid
		if i%4 == 0 {
			col = bpGridMaj
		}
		ui.FillRect(d, image.Rect(x, r.Min.Y, x+1, r.Max.Y), col)
	}
	j0 := -((oy - r.Min.Y) / step) - 1
	for j, y := j0, oy+j0*step; y < r.Max.Y; j, y = j+1, y+step {
		col := bpGrid
		if j%4 == 0 {
			col = bpGridMaj
		}
		ui.FillRect(d, image.Rect(r.Min.X, y, r.Max.X, y+1), col)
	}
	game := image.Rect(ox, oy, ox+gameW*z, oy+gameH*z)
	if s.scene.Type == scene.DocScene {
		ui.StrokeRect(d, game, bpDim)
		u.DrawText(d, fmt.Sprintf("game %dx%d", gameW, gameH), float64(game.Min.X+4), float64(game.Min.Y-20), bpDim)
	}

	for _, it := range items {
		box := it.r
		if box.Dx() < 1 || box.Dy() < 1 || !box.Overlaps(r) {
			continue
		}
		if it.n.Kind == scene.KindChunk {
			ui.StrokeRect(d, box, colDocMap)
			ui.StrokeRect(d, box.Inset(1), colDocMap)
			if it.selected {
				ui.StrokeRect(d, box.Inset(-2), bpSel)
			}
			u.DrawText(d, it.n.Name, float64(box.Min.X+6), float64(box.Min.Y+4), colDocMap)
			continue
		}
		if it.bad {
			ui.StrokeRect(d, box.Inset(-3), color.RGBA{0xff, 0x6b, 0x6b, 0xff})
		}
		ui.FillRect(d, box, bpFill)
		ui.StrokeRect(d, box, bpLine)
		if it.selected {
			ui.StrokeRect(d, box.Inset(-1), bpSel)
			ui.StrokeRect(d, box, bpSel)
		}
		size := "node"
		if it.n.HasBox() {
			size = fmt.Sprintf("%d×%d", it.n.W, it.n.H)
		}
		if it.n.Template {
			ui.StrokeRect(d, box.Inset(-2), templateColor)
		}
		lines := []struct {
			text string
			col  color.RGBA
		}{{it.n.Name + marks(it.n), bpText}, {fmt.Sprintf("#%d", it.n.ID), bpDim}, {size, bpDim}}

		inner := box.Inset(2)
		fit := min(len(lines), max(inner.Dy()/bpLineH, 0))
		if fit == 0 {
			continue
		}
		clip := inner.Intersect(r)
		if clip.Empty() {
			continue
		}
		sub := d.SubImage(clip).(*ebiten.Image)
		y := float64(inner.Min.Y) + float64(inner.Dy()-fit*bpLineH)/2
		for _, l := range lines[:fit] {
			t := u.FitText(l.text, float64(inner.Dx()))
			if t != "" {
				x := float64(inner.Min.X) + (float64(inner.Dx())-u.TextWidth(t))/2
				u.DrawText(sub, t, x, y, l.col)
			}
			y += bpLineH
		}
	}
	u.DrawText(d, fmt.Sprintf("blueprint  zoom %dx", z), float64(r.Min.X+8), float64(r.Max.Y-22), bpDim)
}

// ---- panel layout ----

const (
	panelScene     dock.PanelID = "scene"
	panelViewport  dock.PanelID = "viewport"
	panelInspector dock.PanelID = "inspector"
	panelConsole   dock.PanelID = "console"
	panelProject   dock.PanelID = "project"
	panelCode      dock.PanelID = "vcode"
)

var (
	allPanels   = []dock.PanelID{panelScene, panelViewport, panelInspector, panelConsole, panelProject, panelCode}
	panelTitles = map[dock.PanelID]string{
		panelScene: "Object", panelViewport: "Viewport", panelInspector: "Inspector", panelConsole: "Console", panelProject: "Project", panelCode: "VCode",
	}
)

func defaultLayout() *dock.Node {
	return &dock.Node{Dir: dock.SplitV, Ratio: 0.66,
		A: &dock.Node{Dir: dock.SplitH, Ratio: 0.19, A: dock.Leaf(panelScene),
			B: &dock.Node{Dir: dock.SplitH, Ratio: 0.8, A: dock.Leaf(panelViewport, panelCode), B: dock.Leaf(panelInspector)}},
		B: &dock.Node{Dir: dock.SplitH, Ratio: 0.62, A: dock.Leaf(panelProject), B: dock.Leaf(panelConsole)}}
}

func layoutPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "vide-studio", "layout.json")
}

// loadLayout returns the saved layout, or the default if it is missing or invalid.
func loadLayout() *dock.Node {
	if p := layoutPath(); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			if n, err := dock.Unmarshal(data, allPanels); err == nil {
				return n
			}
		}
	}
	return defaultLayout()
}

// saveLayout stores the layout in the user config directory.
func saveLayout(n *dock.Node) error {
	p := layoutPath()
	if p == "" {
		return errors.New("no user config directory")
	}
	data, err := dock.Marshal(n)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return project.Save(p, data)
}

// ---- document settings in the inspector (root node) ----

var docDescriptions = map[scene.DocType]string{
	scene.DocScene:  "Scene: the finished product",
	scene.DocObject: "Object: a reusable building block",
	scene.DocMap:    "Map: the world of the game",
}

func (s *Studio) docSettings(n *scene.Node, line func() image.Rectangle) {
	u := s.ui
	u.Text(line(), docDescriptions[s.scene.Type], docColor(s.scene.Type))
	old := s.scene.TileSize
	s.stepper(n.ID, line(), "Tile", &s.scene.TileSize, 1)
	s.scene.TileSize = min(max(s.scene.TileSize, 1), 256)
	if s.scene.TileSize != old {
		for _, c := range s.scene.Root.Children {
			if c.Kind == scene.KindChunk {
				c.W, c.H = s.scene.ChunkSize(), s.scene.ChunkSize()
				c.X, c.Y = s.scene.ChunkOrigin(c)
			}
		}
	}
	if s.scene.Type == scene.DocMap {
		s.mapOptimization(n, line)
	}
	if s.scene.Type != scene.DocScene {
		return
	}
	mr := line()
	u.Text(image.Rect(mr.Min.X, mr.Min.Y, mr.Min.X+60, mr.Max.Y), "Map", u.Theme.TextDim)
	if v, ok := u.Field("scenemap", image.Rect(mr.Min.X+62, mr.Min.Y, mr.Max.X, mr.Max.Y), s.scene.Map, false); ok {
		v = strings.TrimSpace(v)
		if v == "" || (projfs.ValidRel(v) && strings.HasPrefix(v, projfs.MapsDir+"/") && strings.HasSuffix(strings.ToLower(v), projfs.DocExt)) {
			s.scene.Map, s.refCache = v, nil
		} else {
			s.Logf("A map path looks like %s/world%s.", projfs.MapsDir, projfs.DocExt)
		}
	}
	if s.scene.Map != "" && s.refScene(s.scene.Map) == nil {
		u.Text(line(), "map file is missing or invalid", color.RGBA{0xff, 0x6b, 0x6b, 0xff})
	}
	u.Text(line(), "Host: this scene is the server", u.Theme.TextDim)
}

// moveChunk changes a chunk's coordinates if the spot is free and in range.
func (s *Studio) moveChunk(n *scene.Node, cx, cy int) {
	if cx < -4096 || cx > 4096 || cy < -4096 || cy > 4096 {
		return
	}
	for _, c := range s.scene.Root.Children {
		if c != n && c.Kind == scene.KindChunk && c.CX == cx && c.CY == cy {
			s.Logf("There is already a chunk at %d,%d.", cx, cy)
			return
		}
	}
	n.CX, n.CY = cx, cy
	n.X, n.Y = s.scene.ChunkOrigin(n)
}

// mapOptimization shows the map's optimization parameter and its status.
func (s *Studio) mapOptimization(n *scene.Node, line func() image.Rectangle) {
	u := s.ui
	or := line()
	u.Text(image.Rect(or.Min.X, or.Min.Y, or.Min.X+110, or.Max.Y), "Optimization", u.Theme.TextDim)
	label := "no"
	if s.scene.Optimization {
		label = "yes"
	}
	if u.Toggle(image.Rect(or.Max.X-56, or.Min.Y, or.Max.X, or.Max.Y), label, s.scene.Optimization) {
		if s.scene.Optimization {
			s.scene.Optimization = false
			s.Logf("%s is no longer optimized: it may have any layout and loads as one piece.", s.scene.Root.Name)
		} else if moved, bad := s.cutIntoChunks(); len(bad) > 0 {
			s.Logf("Cannot optimize: %s would cross a chunk border (%d×%d tiles). Move or resize %s first.",
				strings.Join(bad, ", "), scene.ChunkTiles, scene.ChunkTiles, map[bool]string{true: "it", false: "them"}[len(bad) == 1])
		} else {
			s.scene.Optimization = true
			s.Logf("%s is optimized: %d object(s) moved into chunks of %d×%d tiles.", s.scene.Root.Name, moved, scene.ChunkTiles, scene.ChunkTiles)
		}
	}
	switch {
	case s.scene.Status() == scene.StatusOptimized:
		u.Text(line(), "Status: "+scene.StatusOptimized, colImage)
	case s.scene.Optimization:
		u.Text(line(), "Not optimized yet: fix the red objects", color.RGBA{0xff, 0x6b, 0x6b, 0xff})
	default:
		u.Text(line(), "Loads as one piece", u.Theme.TextDim)
	}
}
