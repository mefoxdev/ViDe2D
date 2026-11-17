package studio

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	tabsH     = 26
	footBtnH  = 24
	menuRowH  = 24
	menuW     = 280
	dlgW      = 400
	dlgH      = 230
	newDocID  = "newdoc-name"
	maxChunks = 4096
)

type objMenu struct {
	open   bool
	fresh  bool // opened this frame: ignore the click that opened it
	anchor image.Rectangle
}

type newDlg struct {
	open  bool
	fresh bool
	typ   scene.DocType
	name  string
	err   string
}

// ---- the Object panel footer: add and delete buttons ----

type footBtn struct {
	label   string
	enabled bool
	act     func(anchor image.Rectangle)
}

// addParent returns the node new objects are added to.
func (s *Studio) addParent() (*scene.Node, bool) {
	p := s.sel
	if p == nil {
		p = s.scene.Root
	}
	for p != s.scene.Root && p.Kind != scene.KindNode && p.Kind != scene.KindChunk {
		p = p.Parent
	}
	if s.scene.Type == scene.DocMap && p == s.scene.Root {
		s.Logf("In a map, objects live inside chunks: select a chunk first (or add one with + Chunk).")
		return nil, false
	}
	return p, true
}

func (s *Studio) addNode(kind scene.Kind, name string) {
	p, ok := s.addParent()
	if !ok {
		return
	}
	n := s.scene.Add(p, kind, name)
	n.X, n.Y = 16, 16
	if kind == scene.KindSprite {
		n.Color = palette[n.ID%len(palette)]
	}
	s.sel = n
	s.Logf("Added %s", n.Name)
}

func (s *Studio) addInstance(ref string) {
	if !s.canInstance(ref) {
		s.Logf("Cannot add %s here: an object cannot contain itself.", objectLabel(ref))
		return
	}
	p, ok := s.addParent()
	if !ok {
		return
	}
	n := s.scene.Add(p, scene.KindInstance, baseNoExt(ref))
	n.Ref, n.X, n.Y = ref, 16, 16
	s.sel = n
	s.Logf("Added %s", n.Name)
}

// addChunk adds a map chunk at the first free spot (row by row).
func (s *Studio) addChunk() {
	taken := map[[2]int]bool{}
	for _, c := range s.scene.Root.Children {
		taken[[2]int{c.CX, c.CY}] = true
	}
	if len(taken) >= maxChunks {
		s.Logf("A map can have at most %d chunks.", maxChunks)
		return
	}
	for i := 0; ; i++ {
		cx, cy := i%16, i/16
		if taken[[2]int{cx, cy}] {
			continue
		}
		c := s.scene.Add(nil, scene.KindChunk, "Chunk "+strconv.Itoa(cx)+","+strconv.Itoa(cy))
		c.CX, c.CY = cx, cy
		c.X, c.Y = s.scene.ChunkOrigin(c)
		s.sel = c
		s.Logf("Added %s (%d×%d tiles)", c.Name, scene.ChunkTiles, scene.ChunkTiles)
		return
	}
}

// objectFooter draws the buttons at the bottom of the Object panel and returns
// the footer rectangle.
func (s *Studio) objectFooter(r image.Rectangle) image.Rectangle {
	u := s.ui
	var btns []footBtn
	if s.scene.Type == scene.DocMap {
		btns = append(btns, footBtn{"+ Chunk", true, func(image.Rectangle) { s.addChunk() }})
	}
	btns = append(btns,
		footBtn{"+ Object", true, func(a image.Rectangle) { s.menu = objMenu{open: true, fresh: true, anchor: a} }},
		footBtn{"+ Node", true, func(image.Rectangle) { s.addNode(scene.KindNode, "Node") }},
		footBtn{"+ Sprite", true, func(image.Rectangle) { s.addNode(scene.KindSprite, "Sprite") }},
		footBtn{"+ Text", true, func(image.Rectangle) { s.addNode(scene.KindText, "Text") }},
		footBtn{"Delete", s.sel != nil && s.sel.Parent != nil, func(image.Rectangle) {
			name, p := s.sel.Name, s.sel.Parent
			s.scene.Remove(s.sel)
			s.sel = p
			s.Logf("Deleted %s", name)
		}},
	)
	// flow layout with wrapping
	type placed struct {
		b footBtn
		r image.Rectangle
	}
	var ps []placed
	x, row := r.Min.X+6, 0
	for _, b := range btns {
		w := int(u.TextWidth(b.label)) + 22
		if x+w > r.Max.X-6 && x > r.Min.X+6 {
			x, row = r.Min.X+6, row+1
		}
		ps = append(ps, placed{b, image.Rect(x, row*(footBtnH+6), x+w, row*(footBtnH+6)+footBtnH)})
		x += w + 6
	}
	rows := row + 1
	foot := image.Rect(r.Min.X, r.Max.Y-(rows*(footBtnH+6)+8), r.Max.X, r.Max.Y)
	u.Fill(foot, u.Theme.PanelHead)
	u.Fill(image.Rect(foot.Min.X, foot.Min.Y, foot.Max.X, foot.Min.Y+1), u.Theme.Border)
	for _, p := range ps {
		br := p.r.Add(image.Pt(0, foot.Min.Y+6))
		if u.Button(br, p.b.label, p.b.enabled) {
			p.b.act(br)
		}
	}
	return foot
}

// ---- document tabs above the viewport ----

// docTabs draws the tabs of the open documents and returns the area below them.
func (s *Studio) docTabs(r image.Rectangle) image.Rectangle {
	u := s.ui
	strip := image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+tabsH)
	u.Fill(strip, u.Theme.Border)
	x := r.Min.X + 4
	for i, d := range s.docs {
		sc, dirty, name := d.scene, d.dirty, d.name()
		if i == s.cur {
			sc, dirty = s.scene, s.dirty
		}
		label := name
		if dirty {
			label += " *"
		}
		w := int(u.TextWidth(label)) + 52
		tr := image.Rect(x, strip.Min.Y+3, min(x+w, strip.Max.X-4), strip.Max.Y)
		x += w + 3
		if tr.Dx() < 60 {
			break
		}
		closeR := image.Rect(tr.Max.X-20, tr.Min.Y, tr.Max.X, tr.Max.Y)
		hover := u.Hover(tr)
		if hover && u.Pressed {
			switch {
			case u.Hover(closeR) && len(s.docs) > 1:
				s.closeDoc(i)
				return s.docTabs(r) // the list changed: redraw from scratch
			case i != s.cur:
				s.activate(i)
			}
		}
		active := i == s.cur
		col := docColor(sc.Type)
		u.Emit(func(dst *ebiten.Image) {
			fill := u.Theme.Panel
			if active {
				fill = u.Theme.PanelHead
			} else if hover {
				fill = color.RGBA{0x28, 0x2b, 0x33, 0xff}
			}
			ui.FillRect(dst, tr, fill)
			if active {
				ui.FillRect(dst, image.Rect(tr.Min.X, tr.Min.Y, tr.Max.X, tr.Min.Y+2), col)
			}
			ui.FillRect(dst, image.Rect(tr.Min.X+8, tr.Min.Y+9, tr.Min.X+15, tr.Min.Y+16), col)
			tc := u.Theme.TextDim
			if active {
				tc = u.Theme.Text
			}
			u.DrawText(dst, label, float64(tr.Min.X+22), float64(tr.Min.Y+(tr.Dy()-ui.FontSize)/2-1), tc)
			if len(s.docs) > 1 {
				xc := u.Theme.TextDim
				if u.Hover(closeR) {
					xc = u.Theme.Text
				}
				u.DrawText(dst, "×", float64(closeR.Min.X+5), float64(closeR.Min.Y+(closeR.Dy()-ui.FontSize)/2-1), xc)
			}
		})
	}
	// what the active document is, at the right
	desc := map[scene.DocType]string{scene.DocScene: "scene", scene.DocObject: "object", scene.DocMap: "map"}[s.scene.Type]
	u.Text(image.Rect(strip.Max.X-90, strip.Min.Y, strip.Max.X-8, strip.Max.Y), desc, docColor(s.scene.Type))
	return image.Rect(r.Min.X, strip.Max.Y, r.Max.X, r.Max.Y)
}

// ---- modal UI: the "+ Object" menu and the "new" dialog ----

func (s *Studio) modalOpen() bool { return s.menu.open || s.dlg.open }

// modalUI draws and handles the menu and the dialog. It must run last in a
// frame (so it draws on top) with the real mouse state.
func (s *Studio) modalUI() {
	if s.menu.open {
		s.objectMenu()
	}
	if s.dlg.open {
		s.newDialog()
	}
	s.menu.fresh, s.dlg.fresh = false, false
}

func (s *Studio) objectMenu() {
	u := s.ui
	var refs []string
	for _, ref := range s.listObjects() {
		if s.canInstance(ref) {
			refs = append(refs, ref)
		}
	}
	rows := len(refs) + 1 // the objects and "New object…"
	if len(refs) == 0 {
		rows = 2 // a hint and "New object…"
	}
	h := rows*menuRowH + 8
	a := s.menu.anchor
	box := image.Rect(a.Min.X, a.Min.Y-h-4, a.Min.X+menuW, a.Min.Y-4)
	if box.Min.Y < toolbarH {
		box = image.Rect(a.Min.X, a.Max.Y+4, a.Min.X+menuW, a.Max.Y+4+h)
	}
	if box.Max.X > s.w-4 {
		box = box.Add(image.Pt(s.w-4-box.Max.X, 0))
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || (u.Pressed && !s.menu.fresh && !u.Hover(box)) {
		s.menu.open = false
		return
	}
	u.Emit(func(d *ebiten.Image) {
		ui.FillRect(d, box, color.RGBA{0x2e, 0x32, 0x3c, 0xff})
		ui.StrokeRect(d, box, u.Theme.Accent)
	})
	y := box.Min.Y + 4
	row := func() image.Rectangle {
		r := image.Rect(box.Min.X+2, y, box.Max.X-2, y+menuRowH)
		y += menuRowH
		return r
	}
	if len(refs) == 0 {
		u.Text(row().Inset(6), "No objects yet in "+projfs.ObjectsDir, u.Theme.TextDim)
	}
	for _, ref := range refs {
		r := row()
		hover := u.Hover(r)
		if hover {
			u.Fill(r, u.Theme.ButtonHover)
		}
		u.Text(image.Rect(r.Min.X+8, r.Min.Y, r.Max.X-4, r.Max.Y), u.FitText(objectLabel(ref), float64(r.Dx()-16)), colDocObject)
		if hover && u.Pressed && !s.menu.fresh {
			s.menu.open = false
			s.addInstance(ref)
			return
		}
	}
	r := row()
	hover := u.Hover(r)
	if hover {
		u.Fill(r, u.Theme.ButtonHover)
	}
	u.Fill(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), u.Theme.Border)
	u.Text(image.Rect(r.Min.X+8, r.Min.Y, r.Max.X, r.Max.Y), "New object…", u.Theme.Text)
	if hover && u.Pressed && !s.menu.fresh {
		s.menu.open = false
		s.dlg = newDlg{open: true, fresh: true, typ: scene.DocObject}
		u.FocusField(newDocID, "")
	}
}

var dlgHints = map[scene.DocType]string{
	scene.DocObject: "A building block. It can be imported into objects and scenes.",
	scene.DocScene:  "The finished product. It has a map and is the host (server) of the game. It cannot be imported.",
	scene.DocMap:    "A game map made of chunks of 64×64 tiles that hold objects; chunks load and unload during the game.",
}

func (s *Studio) newDialog() {
	u := s.ui
	d := &s.dlg
	box := image.Rect(s.w/2-dlgW/2, s.h/2-dlgH/2, s.w/2+dlgW/2, s.h/2+dlgH/2)
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		d.open = false
		return
	}
	// dim everything behind
	u.Emit(func(dst *ebiten.Image) {
		ui.FillRect(dst, image.Rect(0, 0, s.w, s.h), color.RGBA{0x00, 0x00, 0x00, 0x60})
		ui.FillRect(dst, box, color.RGBA{0x2a, 0x2d, 0x35, 0xff})
		ui.StrokeRect(dst, box, u.Theme.Accent)
	})
	u.Text(image.Rect(box.Min.X+16, box.Min.Y+8, box.Max.X-16, box.Min.Y+34), "Create new", u.Theme.Text)

	x := box.Min.X + 16
	for _, t := range []struct {
		typ   scene.DocType
		label string
	}{{scene.DocObject, "Object"}, {scene.DocScene, "Scene"}, {scene.DocMap, "Map"}} {
		b := image.Rect(x, box.Min.Y+40, x+90, box.Min.Y+66)
		x += 96
		col, on, hover := docColor(t.typ), d.typ == t.typ, u.Hover(b)
		if hover && u.Pressed && !d.fresh {
			d.typ, d.err = t.typ, ""
			on = true
		}
		label := t.label
		u.Emit(func(dst *ebiten.Image) {
			fill := u.Theme.Button
			if on {
				fill = color.RGBA{col.R / 3, col.G / 3, col.B / 3, 0xff}
			}
			ui.FillRect(dst, b, fill)
			ui.StrokeRect(dst, b, col)
			u.DrawText(dst, label, float64(b.Min.X)+(float64(b.Dx())-u.TextWidth(label))/2, float64(b.Min.Y+(b.Dy()-ui.FontSize)/2-1), col)
		})
	}
	// wrapped hint
	hint := dlgHints[d.typ]
	lines := wrapText(u, hint, float64(box.Dx()-32))
	for i, l := range lines {
		u.Text(image.Rect(box.Min.X+16, box.Min.Y+72+i*ui.LineHeight, box.Max.X-16, box.Min.Y+72+(i+1)*ui.LineHeight), l, u.Theme.TextDim)
	}
	nameY := box.Min.Y + 72 + 3*ui.LineHeight + 4
	u.Text(image.Rect(box.Min.X+16, nameY, box.Min.X+70, nameY+26), "Name", u.Theme.TextDim)
	submit := false
	if v, committed := u.Field(newDocID, image.Rect(box.Min.X+72, nameY, box.Max.X-16, nameY+26), d.name, false); true {
		d.name = v
		if committed && (inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter)) {
			submit = true
		}
	}
	if d.err != "" {
		u.Text(image.Rect(box.Min.X+16, nameY+30, box.Max.X-16, nameY+30+ui.LineHeight), d.err, color.RGBA{0xff, 0x6b, 0x6b, 0xff})
	}
	if u.Button(image.Rect(box.Max.X-196, box.Max.Y-40, box.Max.X-106, box.Max.Y-12), "Create", true) && !d.fresh {
		submit = true
	}
	if u.Button(image.Rect(box.Max.X-100, box.Max.Y-40, box.Max.X-16, box.Max.Y-12), "Cancel", true) && !d.fresh {
		d.open = false
		return
	}
	if submit {
		if err := s.newDocFile(d.typ, d.name); err != nil {
			d.err = err.Error()
			u.FocusField(newDocID, d.name)
			return
		}
		d.open = false
	}
}

// wrapText splits s into lines no wider than maxW (greedy, by words).
func wrapText(u *ui.Context, s string, maxW float64) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		try := w
		if cur != "" {
			try = cur + " " + w
		}
		if cur != "" && u.TextWidth(try) > maxW {
			lines = append(lines, cur)
			cur = w
		} else {
			cur = try
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
