package dock

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	splitW   = 5  // splitter thickness
	minPane  = 90 // minimum pane size when dragging a splitter
	dragDist = 6  // pixels before a tab press becomes a drag
)

// Dock lays out panels, handles tab clicks, splitter drags and drag-and-drop
// re-docking. Call Update first each frame (it draws the frames), then draw
// panel contents into the returned rectangles, then DrawOverlay.
type Dock struct {
	Root   *Node
	Titles map[PanelID]string

	dragSplit  *Node
	splitBox   image.Rectangle // parent bounds of dragSplit
	pressPanel PanelID
	pressPos   image.Point
	dragPanel  PanelID // set while a tab is being dragged
	changed    bool

	leaves []leafRect
	over   struct {
		rect image.Rectangle
		ok   bool
	}
}

type leafRect struct {
	n *Node
	r image.Rectangle
}

type splitter struct {
	n      *Node
	bar    image.Rectangle
	bounds image.Rectangle
}

// New creates a dock from a layout tree.
func New(root *Node, titles map[PanelID]string) *Dock { return &Dock{Root: root, Titles: titles} }

// Changed reports (once) that the layout was modified by the user.
func (d *Dock) Changed() bool { c := d.changed; d.changed = false; return c }

func (d *Dock) compute(n *Node, r image.Rectangle, leaves *[]leafRect, splits *[]splitter) {
	if n.IsLeaf() {
		*leaves = append(*leaves, leafRect{n, r})
		return
	}
	var a, bar, b image.Rectangle
	if n.Dir == SplitH {
		w := int(float64(r.Dx()-splitW) * n.Ratio)
		a = image.Rect(r.Min.X, r.Min.Y, r.Min.X+w, r.Max.Y)
		bar = image.Rect(a.Max.X, r.Min.Y, a.Max.X+splitW, r.Max.Y)
		b = image.Rect(bar.Max.X, r.Min.Y, r.Max.X, r.Max.Y)
	} else {
		h := int(float64(r.Dy()-splitW) * n.Ratio)
		a = image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+h)
		bar = image.Rect(r.Min.X, a.Max.Y, r.Max.X, a.Max.Y+splitW)
		b = image.Rect(r.Min.X, bar.Max.Y, r.Max.X, r.Max.Y)
	}
	if splits != nil {
		*splits = append(*splits, splitter{n, bar, r})
	}
	d.compute(n.A, a, leaves, splits)
	d.compute(n.B, b, leaves, splits)
}

func (d *Dock) tabRect(u *ui.Context, leaf image.Rectangle, tabs []PanelID, i int) image.Rectangle {
	x := leaf.Min.X + 2
	for k := 0; k < i; k++ {
		x += int(u.TextWidth(d.Titles[tabs[k]])) + 24
	}
	return image.Rect(x, leaf.Min.Y+2, x+int(u.TextWidth(d.Titles[tabs[i]]))+24, leaf.Min.Y+headerH)
}

// Update handles input and records the frames. It returns the content
// rectangle of every visible (active) panel.
func (d *Dock) Update(u *ui.Context, bounds image.Rectangle) map[PanelID]image.Rectangle {
	var leaves []leafRect
	var splits []splitter
	d.compute(d.Root, bounds, &leaves, &splits)
	p := image.Pt(u.MX, u.MY)

	// splitters
	if d.dragPanel == "" && d.pressPanel == "" {
		if d.dragSplit == nil && u.Pressed {
			for _, s := range splits {
				if p.In(s.bar) {
					d.dragSplit, d.splitBox = s.n, s.bounds
				}
			}
		}
		if d.dragSplit != nil {
			if u.Down {
				d.resize(p)
			} else {
				d.dragSplit, d.changed = nil, true
			}
		}
	}

	// tabs: click to activate, drag to re-dock
	if d.dragSplit == nil {
		if u.Pressed {
			for _, l := range leaves {
				for i, id := range l.n.Panels {
					if p.In(d.tabRect(u, l.r, l.n.Panels, i)) {
						l.n.Active, d.pressPanel, d.pressPos = i, id, p
						d.changed = true
					}
				}
			}
		}
		if d.pressPanel != "" {
			switch {
			case !u.Down:
				if d.dragPanel != "" {
					d.drop(p, leaves)
				}
				d.pressPanel, d.dragPanel = "", ""
			case d.dragPanel == "" && abs(p.X-d.pressPos.X)+abs(p.Y-d.pressPos.Y) > dragDist:
				d.dragPanel = d.pressPanel
			}
		}
	}

	// recompute: resizing or re-docking may have changed the layout
	leaves, splits = leaves[:0], splits[:0]
	d.compute(d.Root, bounds, &leaves, &splits)
	d.leaves = leaves

	rects := map[PanelID]image.Rectangle{}
	for _, l := range leaves {
		d.drawFrame(u, l)
		if len(l.n.Panels) > 0 {
			content := image.Rect(l.r.Min.X+1, l.r.Min.Y+headerH, l.r.Max.X-1, l.r.Max.Y-1)
			rects[l.n.Panels[min(l.n.Active, len(l.n.Panels)-1)]] = content
		}
	}
	for _, s := range splits {
		col := u.Theme.Border
		if s.n == d.dragSplit || (d.dragSplit == nil && d.dragPanel == "" && p.In(s.bar)) {
			col = u.Theme.Accent
		}
		u.Fill(s.bar, col)
	}

	d.over.ok = false
	if d.dragPanel != "" {
		for _, l := range d.leaves {
			if z := ZoneAt(l.r, p); z != ZoneNone {
				d.over.rect, d.over.ok = ZoneRect(l.r, z), true
			}
		}
	}
	return rects
}

func (d *Dock) resize(p image.Point) {
	n, b := d.dragSplit, d.splitBox
	lo, size, pos := b.Min.X, b.Dx(), p.X
	if n.Dir == SplitV {
		lo, size, pos = b.Min.Y, b.Dy(), p.Y
	}
	avail := float64(size - splitW)
	if avail <= 0 {
		return
	}
	r := (float64(pos-lo) - splitW/2) / avail
	m := float64(minPane) / avail
	if m > .5 {
		m = .5
	}
	n.Ratio = min(max(r, m), 1-m)
}

func (d *Dock) drop(p image.Point, leaves []leafRect) {
	for _, l := range leaves {
		z := ZoneAt(l.r, p)
		if z == ZoneNone {
			continue
		}
		var ref PanelID
		for _, id := range l.n.Panels {
			if id != d.dragPanel {
				ref = id
				break
			}
		}
		if ref != "" && d.Root.Dock(d.dragPanel, ref, z) {
			d.changed = true
		}
		return
	}
}

func (d *Dock) drawFrame(u *ui.Context, l leafRect) {
	hdr := image.Rect(l.r.Min.X, l.r.Min.Y, l.r.Max.X, l.r.Min.Y+headerH)
	u.Emit(func(dst *ebiten.Image) {
		ui.FillRect(dst, l.r, u.Theme.Panel)
		ui.FillRect(dst, hdr, u.Theme.Border)
		ui.StrokeRect(dst, l.r, u.Theme.Border)
	})
	for i, id := range l.n.Panels {
		tr := d.tabRect(u, l.r, l.n.Panels, i)
		active := i == l.n.Active
		col := u.Theme.TextDim
		if active {
			col = u.Theme.Text
		}
		title := d.Titles[id]
		u.Emit(func(dst *ebiten.Image) {
			if active {
				ui.FillRect(dst, tr, u.Theme.PanelHead)
				ui.FillRect(dst, image.Rect(tr.Min.X, tr.Min.Y, tr.Max.X, tr.Min.Y+2), u.Theme.Accent)
			}
			u.DrawText(dst, title, float64(tr.Min.X+12), float64(tr.Min.Y+(tr.Dy()-ui.FontSize)/2), col)
		})
	}
}

// DrawOverlay records the drop highlight and drag ghost. Call it last.
func (d *Dock) DrawOverlay(u *ui.Context) {
	if d.dragPanel == "" {
		return
	}
	title := d.Titles[d.dragPanel]
	over, ok := d.over.rect, d.over.ok
	mx, my := u.MX, u.MY
	u.Emit(func(dst *ebiten.Image) {
		if ok {
			ui.FillRect(dst, over, color.RGBA{0x18, 0x30, 0x50, 0x50})
			ui.StrokeRect(dst, over, u.Theme.Accent)
		}
		g := image.Rect(mx+12, my+12, mx+12+int(u.TextWidth(title))+20, my+12+24)
		ui.FillRect(dst, g, u.Theme.PanelHead)
		ui.StrokeRect(dst, g, u.Theme.Accent)
		u.DrawText(dst, title, float64(g.Min.X+10), float64(g.Min.Y+4), u.Theme.Text)
	})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
