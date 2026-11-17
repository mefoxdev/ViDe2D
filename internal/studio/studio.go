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

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/vide2d/vide-studio/internal/dock"
	"github.com/vide2d/vide-studio/internal/project"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	toolbarH = 40
	rowH     = 24
	gridStep = 16

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
	s := &Studio{ui: ui.New(), zoom: 2, path: path}
	s.dock = dock.New(loadLayout(), panelTitles)
	loaded := false
	if path != "" {
		sc, err := project.Load(path)
		switch {
		case err == nil:
			s.scene, loaded = sc, true
			s.Logf("Opened %s", path)
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
	data, err := project.Marshal(s.scene)
	if err == nil {
		err = project.Save(s.path, data)
	}
	if err != nil {
		s.Logf("Save failed: %v", err)
		return
	}
	s.saved = data
	s.Logf("Saved %s", s.path)
}

func (s *Studio) revert() {
	sc, err := project.Load(s.path)
	if err != nil {
		s.Logf("Revert failed: %v", err)
		return
	}
	s.scene, s.sel, s.dragging = sc, sc.Root, nil
	s.saved, _ = project.Marshal(sc)
	s.Logf("Reverted to %s", s.path)
}

func (s *Studio) updateDirty() {
	if s.tick++; s.tick%20 != 0 {
		return
	}
	data, err := project.Marshal(s.scene)
	s.dirty = err != nil || !bytes.Equal(data, s.saved)
	title := "ViDe-studio"
	if s.path != "" {
		title += " — " + s.path
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
	toolbar := image.Rect(0, 0, s.w, toolbarH)
	rects := s.dock.Update(u, image.Rect(0, toolbarH, s.w, s.h))

	if r, ok := rects[panelViewport]; ok {
		s.viewport(r)
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
	s.dock.DrawOverlay(u)
	if s.dock.Changed() {
		if err := saveLayout(s.dock.Root); err != nil {
			s.Logf("Layout not saved: %v", err)
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) && !u.Focused() {
		s.mode = 1 - s.mode
	}
	if ebiten.IsKeyPressed(ebiten.KeyControl) && inpututil.IsKeyJustPressed(ebiten.KeyS) && !u.Focused() {
		s.save()
	}
	s.updateDirty()
	return nil
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
	parent := s.sel
	if parent != nil && parent.Kind != scene.KindNode {
		parent = parent.Parent
	}
	if btn("+ Node", 70, true) {
		s.sel = s.scene.Add(parent, scene.KindNode, "Node")
		s.Logf("Added %s", s.sel.Name)
	}
	if btn("+ Sprite", 80, true) {
		n := s.scene.Add(parent, scene.KindSprite, "Sprite")
		n.Color = palette[n.ID%len(palette)]
		n.X, n.Y = 16, 16
		s.sel = n
		s.Logf("Added %s", n.Name)
	}
	if btn("Delete", 70, s.sel != nil && s.sel.Parent != nil) {
		name := s.sel.Name
		p := s.sel.Parent
		s.scene.Remove(s.sel)
		s.sel = p
		s.Logf("Deleted %s", name)
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
		s.Logf("Play: the ViDe2D runtime is not connected yet.")
	}
	u.Text(image.Rect(s.w-260, r.Min.Y, s.w-8, r.Max.Y), "scene: "+s.scene.Root.Name+map[bool]string{true: " *", false: ""}[s.dirty], u.Theme.TextDim)
}

func (s *Studio) treePanel(r image.Rectangle) {
	u := s.ui
	c := r
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
		if n.Kind == scene.KindSprite {
			icon = "■"
		}
		label := fmt.Sprintf("%s %s", icon, n.Name)
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
	y += 6
	step := 1
	if u.Shift {
		step = 8
	}
	s.stepper(n.ID, line(), "X", &n.X, step)
	s.stepper(n.ID, line(), "Y", &n.Y, step)
	if n.Kind == scene.KindSprite {
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
			s.dragging = hit
			px, py := hit.WorldPos()
			s.grabX, s.grabY = wx-px, wy-py
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
			s.dragging.X = wx - s.grabX - pwx
			s.dragging.Y = wy - s.grabY - pwy
		} else {
			s.dragging = nil
		}
	}

	// snapshot for drawing
	var items []item
	z := s.zoom
	s.scene.Root.Walk(func(n *scene.Node, _ int) {
		x, y := n.WorldPos()
		w, h := s.boxSize(n)
		items = append(items, item{
			r: image.Rect(int(s.originX)+x*z, int(s.originY)+y*z, int(s.originX)+(x+w)*z, int(s.originY)+(y+h)*z),
			n: n, selected: n == s.sel,
		})
	})
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

// boxSize is the world-space size used for hit-testing and drawing n in the
// current mode. Plain nodes have no size in 2D mode.
func (s *Studio) boxSize(n *scene.Node) (w, h int) {
	switch {
	case n.Kind == scene.KindSprite:
		return n.W, n.H
	case s.mode == ModeBlueprint:
		return blueprintNodeSize, blueprintNodeSize
	}
	return 0, 0
}

func (s *Studio) hit(wx, wy int) *scene.Node {
	var found *scene.Node
	s.scene.Root.Walk(func(n *scene.Node, _ int) {
		w, h := s.boxSize(n)
		x, y := n.WorldPos()
		if wx >= x && wx < x+w && wy >= y && wy < y+h {
			found = n // later in draw order = on top
		}
	})
	return found
}

func (s *Studio) drawViewport(d *ebiten.Image, r image.Rectangle, ox, oy, z int, items []item) {
	t := s.ui.Theme
	ui.FillRect(d, r, color.RGBA{0x16, 0x17, 0x1c, 0xff})
	// game screen area
	game := image.Rect(ox, oy, ox+gameW*z, oy+gameH*z)
	ui.FillRect(d, game, color.RGBA{0x20, 0x22, 0x2a, 0xff})
	// grid
	step := gridStep * z
	for x := ox - ((ox-r.Min.X)/step+1)*step; x < r.Max.X; x += step {
		ui.FillRect(d, image.Rect(x, r.Min.Y, x+1, r.Max.Y), t.Grid)
	}
	for y := oy - ((oy-r.Min.Y)/step+1)*step; y < r.Max.Y; y += step {
		ui.FillRect(d, image.Rect(r.Min.X, y, r.Max.X, y+1), t.Grid)
	}
	ui.StrokeRect(d, game, color.RGBA{0x4f, 0x9d, 0xff, 0x90})
	s.ui.DrawText(d, fmt.Sprintf("game %dx%d", gameW, gameH), float64(game.Min.X+4), float64(game.Min.Y-20), t.TextDim)

	for _, it := range items {
		if it.n.Kind == scene.KindSprite {
			ui.FillRect(d, it.r, it.n.Color)
			if it.selected {
				ui.StrokeRect(d, it.r.Inset(-2), t.Accent)
			}
			s.ui.DrawText(d, it.n.Name, float64(it.r.Min.X), float64(it.r.Min.Y-18), t.TextDim)
		} else {
			p := it.r.Min
			ui.FillRect(d, image.Rect(p.X-5, p.Y, p.X+6, p.Y+1), t.TextDim)
			ui.FillRect(d, image.Rect(p.X, p.Y-5, p.X+1, p.Y+6), t.TextDim)
			if it.selected {
				ui.StrokeRect(d, image.Rect(p.X-7, p.Y-7, p.X+8, p.Y+8), t.Accent)
			}
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
	step := gridStep * z
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
	ui.StrokeRect(d, game, bpDim)
	u.DrawText(d, fmt.Sprintf("game %dx%d", gameW, gameH), float64(game.Min.X+4), float64(game.Min.Y-20), bpDim)

	for _, it := range items {
		box := it.r
		if box.Dx() < 1 || box.Dy() < 1 || !box.Overlaps(r) {
			continue
		}
		ui.FillRect(d, box, bpFill)
		ui.StrokeRect(d, box, bpLine)
		if it.selected {
			ui.StrokeRect(d, box.Inset(-1), bpSel)
			ui.StrokeRect(d, box, bpSel)
		}
		size := "node"
		if it.n.Kind == scene.KindSprite {
			size = fmt.Sprintf("%d×%d", it.n.W, it.n.H)
		}
		lines := []struct {
			text string
			col  color.RGBA
		}{{it.n.Name, bpText}, {fmt.Sprintf("#%d", it.n.ID), bpDim}, {size, bpDim}}

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
)

var (
	allPanels   = []dock.PanelID{panelScene, panelViewport, panelInspector, panelConsole}
	panelTitles = map[dock.PanelID]string{
		panelScene: "Scene", panelViewport: "Viewport", panelInspector: "Inspector", panelConsole: "Console",
	}
)

func defaultLayout() *dock.Node {
	return &dock.Node{Dir: dock.SplitV, Ratio: 0.78,
		A: &dock.Node{Dir: dock.SplitH, Ratio: 0.19, A: dock.Leaf(panelScene),
			B: &dock.Node{Dir: dock.SplitH, Ratio: 0.8, A: dock.Leaf(panelViewport), B: dock.Leaf(panelInspector)}},
		B: dock.Leaf(panelConsole)}
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
