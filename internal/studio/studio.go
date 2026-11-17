// Package studio is the ViDe-studio shell: scene tree, viewport, inspector, console.
package studio

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	toolbarH = 40
	leftW    = 240
	rightW   = 270
	bottomH  = 150
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
}

// New creates a studio with a small demo scene.
func New() *Studio {
	s := &Studio{ui: ui.New(), scene: scene.New("Main"), zoom: 2}
	hero := s.scene.Add(nil, scene.KindSprite, "Hero")
	hero.X, hero.Y, hero.Color = 40, 60, palette[4]
	coin := s.scene.Add(nil, scene.KindSprite, "Coin")
	coin.X, coin.Y, coin.W, coin.H, coin.Color = 200, 90, 8, 8, palette[2]
	s.sel = hero
	s.Logf("ViDe-studio started. Middle mouse / Space+drag: pan, wheel: zoom, drag objects to move, F: center view.")
	return s
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
	full := image.Rect(0, 0, s.w, s.h)
	toolbar := image.Rect(0, 0, s.w, toolbarH)
	left := image.Rect(0, toolbarH, leftW, s.h-bottomH)
	right := image.Rect(s.w-rightW, toolbarH, s.w, s.h-bottomH)
	bottom := image.Rect(0, s.h-bottomH, s.w, s.h)
	view := image.Rect(leftW, toolbarH, s.w-rightW, s.h-bottomH).Intersect(full)

	s.viewport(view)
	s.toolbar(toolbar)
	s.treePanel(left)
	s.inspector(right)
	s.console(bottom)
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
	if btn("Play", 70, true) {
		s.Logf("Play: the ViDe2D runtime is not connected yet.")
	}
	u.Text(image.Rect(s.w-260, r.Min.Y, s.w-8, r.Max.Y), "ViDe-studio  •  scene: "+s.scene.Root.Name, u.Theme.TextDim)
}

func (s *Studio) treePanel(r image.Rectangle) {
	u := s.ui
	c := u.Panel(r, "Scene")
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
	c := u.Panel(r, "Inspector")
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
	u.Text(line(), n.Name, u.Theme.Text)
	u.Text(line(), "Type: "+string(n.Kind), u.Theme.TextDim)
	y += 6
	step := 1
	if u.Shift {
		step = 8
	}
	s.stepper(line(), "X", &n.X, step)
	s.stepper(line(), "Y", &n.Y, step)
	if n.Kind == scene.KindSprite {
		s.stepper(line(), "Width", &n.W, step)
		s.stepper(line(), "Height", &n.H, step)
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
	u.Text(line(), "Shift: step 8", u.Theme.TextDim)
}

func (s *Studio) stepper(r image.Rectangle, label string, v *int, step int) {
	u := s.ui
	u.Text(image.Rect(r.Min.X, r.Min.Y, r.Min.X+60, r.Max.Y), label, u.Theme.TextDim)
	minus := image.Rect(r.Max.X-120, r.Min.Y, r.Max.X-96, r.Max.Y)
	plus := image.Rect(r.Max.X-24, r.Min.Y, r.Max.X, r.Max.Y)
	if u.Button(minus, "-", true) {
		*v -= step
	}
	if u.Button(plus, "+", true) {
		*v += step
	}
	val := image.Rect(minus.Max.X, r.Min.Y, plus.Min.X, r.Max.Y)
	str := fmt.Sprint(*v)
	u.Emit(func(d *ebiten.Image) {
		u.DrawText(d, str, float64(val.Min.X)+(float64(val.Dx())-u.TextWidth(str))/2, float64(val.Min.Y+(val.Dy()-ui.FontSize)/2-1), u.Theme.Text)
	})
}

func (s *Studio) console(r image.Rectangle) {
	u := s.ui
	c := u.Panel(r, "Console")
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
	if (!s.camMoved || inpututil.IsKeyJustPressed(ebiten.KeyF)) && r.Dx() > 0 {
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
		w, h := n.W, n.H
		if n.Kind != scene.KindSprite {
			w, h = 0, 0
		}
		items = append(items, item{
			r: image.Rect(int(s.originX)+x*z, int(s.originY)+y*z, int(s.originX)+(x+w)*z, int(s.originY)+(y+h)*z),
			n: n, selected: n == s.sel,
		})
	})
	ox, oy := int(s.originX), int(s.originY)
	u.Emit(func(d *ebiten.Image) {
		sub := d.SubImage(r).(*ebiten.Image)
		s.drawViewport(sub, r, ox, oy, z, items)
	})
}

func (s *Studio) hit(wx, wy int) *scene.Node {
	var found *scene.Node
	s.scene.Root.Walk(func(n *scene.Node, _ int) {
		if n.Kind != scene.KindSprite {
			return
		}
		x, y := n.WorldPos()
		if wx >= x && wx < x+n.W && wy >= y && wy < y+n.H {
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
