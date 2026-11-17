package studio

import (
	"fmt"
	"image"
	"image/color"
	"path"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	projRowH     = 22
	projPreviewW = 150 // preview thumbnail box
)

// Colors by what a file is good for in a game.
var (
	colCode  = color.RGBA{0xc5, 0x86, 0xc0, 0xff}
	colImage = color.RGBA{0x5c, 0xc9, 0x6b, 0xff}
	colSound = color.RGBA{0xe5, 0xa2, 0x4b, 0xff}
	colScene = color.RGBA{0x4f, 0x9d, 0xff, 0xff}
	colDir   = color.RGBA{0xae, 0xb6, 0xc7, 0xff}
	colOther = color.RGBA{0x6b, 0x71, 0x80, 0xff}
)

func kindColor(k projfs.Kind) color.RGBA {
	switch k {
	case projfs.KindCode:
		return colCode
	case projfs.KindImage:
		return colImage
	case projfs.KindSound:
		return colSound
	case projfs.KindScene:
		return colScene
	case projfs.KindDir:
		return colDir
	}
	return colOther
}

var projChips = []struct {
	label string
	kind  projfs.Kind
	w     int
}{
	{"Code", projfs.KindCode, 58},
	{"Images", projfs.KindImage, 70},
	{"Sounds", projfs.KindSound, 70},
	{"Scenes", projfs.KindScene, 70},
}

type projState struct {
	dir       string // project folder; empty if the project has no location
	top       []*projfs.Entry
	truncated bool
	collapsed map[string]bool
	filter    map[projfs.Kind]bool
	sel       string // project-relative path of the selected entry
	selKind   projfs.Kind
	scroll    int

	press   string // entry pressed in the list (candidate for dragging)
	pressK  projfs.Kind
	pressAt image.Point
	drag    bool

	frame     int
	lastClick int
	lastPath  string
}

func (s *Studio) refreshProject() {
	s.tex.reset()
	p := &s.proj
	p.top, p.truncated = projfs.Scan(p.dir)
	if p.collapsed == nil {
		p.collapsed = map[string]bool{}
	}
	if p.filter == nil {
		p.filter = map[projfs.Kind]bool{}
	}
	if p.sel != "" && !projHas(p.top, p.sel) {
		p.sel = ""
	}
}

func projHas(top []*projfs.Entry, rel string) bool {
	found := false
	projfs.Walk(top, func(e *projfs.Entry) { found = found || e.Path == rel })
	return found
}

// visibleRows flattens the tree for display, honoring folder collapsing and
// the kind filter.
func (s *Studio) visibleRows() []*projfs.Entry {
	p := &s.proj
	active := false
	for _, on := range p.filter {
		active = active || on
	}
	var matches func(e *projfs.Entry) bool
	matches = func(e *projfs.Entry) bool {
		if e.Kind != projfs.KindDir {
			return !active || p.filter[e.Kind]
		}
		if !active {
			return true
		}
		for _, c := range e.Children {
			if matches(c) {
				return true
			}
		}
		return false
	}
	var rows []*projfs.Entry
	var walk func(list []*projfs.Entry)
	walk = func(list []*projfs.Entry) {
		for _, e := range list {
			if !matches(e) {
				continue
			}
			rows = append(rows, e)
			if e.Kind == projfs.KindDir && !p.collapsed[e.Path] {
				walk(e.Children)
			}
		}
	}
	walk(p.top)
	return rows
}

// resourceNames lists project resources of the given kinds as the names
// scripts may use: the file name when it is unique, the full path otherwise.
// With both set, every file contributes its full path as well as its file name.
func (s *Studio) resourceNames(both bool, kinds ...projfs.Kind) []string {
	files := projfs.Files(s.proj.top, kinds...)
	count := map[string]int{}
	for _, f := range files {
		count[path.Base(f.Path)]++
	}
	out := []string{}
	for _, f := range files {
		base := path.Base(f.Path)
		switch {
		case both:
			out = append(out, f.Path)
			if count[base] == 1 && base != f.Path {
				out = append(out, base)
			}
		case count[base] == 1:
			out = append(out, base)
		default:
			out = append(out, f.Path)
		}
	}
	return out
}

// sceneNames lists scene names: scene files without the ".vide.json" suffix.
func (s *Studio) sceneNames() []string {
	out := []string{}
	for _, f := range projfs.Files(s.proj.top, projfs.KindScene) {
		out = append(out, strings.TrimSuffix(path.Base(f.Path), ".vide.json"))
	}
	return out
}

// setTexture assigns an image to a sprite and resizes the sprite to the image.
func (s *Studio) setTexture(n *scene.Node, rel string) bool {
	if n == nil || n.Kind != scene.KindSprite {
		return false
	}
	img := s.tex.get(rel)
	if img == nil {
		s.Logf("Cannot use %s: %v", rel, s.tex.failed[rel])
		return false
	}
	b := img.Bounds()
	n.Texture, n.W, n.H = rel, b.Dx(), b.Dy()
	s.Logf("%s: texture %s (%d×%d)", n.Name, rel, n.W, n.H)
	return true
}

func (s *Studio) setScript(n *scene.Node, rel string) bool {
	if n == nil || !s.scriptExists(rel) {
		return false
	}
	n.Script = rel
	s.Logf("%s: script %s", n.Name, rel)
	return true
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func (s *Studio) projPanel(r image.Rectangle) {
	u := s.ui
	p := &s.proj
	p.frame++

	// top bar: actions and kind filters (the chips double as a color legend)
	x := r.Min.X + 6
	by := r.Min.Y + 4
	if u.Button(image.Rect(x, by, x+76, by+24), "Refresh", true) {
		s.refreshProject()
		s.Logf("Project: %d usable file(s) in %s", len(projfs.Files(p.top)), p.dir)
	}
	x += 82
	canAssign := false
	switch p.selKind {
	case projfs.KindImage:
		canAssign = s.sel != nil && s.sel.Kind == scene.KindSprite
	case projfs.KindCode:
		canAssign = s.sel != nil
	}
	if u.Button(image.Rect(x, by, x+70, by+24), "Assign", canAssign && p.sel != "") {
		if p.selKind == projfs.KindImage {
			s.setTexture(s.sel, p.sel)
		} else {
			s.setScript(s.sel, p.sel)
		}
	}
	x += 82
	for _, c := range projChips {
		b := image.Rect(x, by, x+c.w, by+24)
		x += c.w + 4
		col, on, hover := kindColor(c.kind), p.filter[c.kind], u.Hover(b)
		if hover && u.Pressed {
			p.filter[c.kind] = !on
			on = !on
		}
		label := c.label
		u.Emit(func(d *ebiten.Image) {
			fill := u.Theme.Button
			if on {
				fill = color.RGBA{col.R / 3, col.G / 3, col.B / 3, 0xff}
			} else if hover {
				fill = u.Theme.ButtonHover
			}
			ui.FillRect(d, b, fill)
			ui.StrokeRect(d, b, col)
			u.DrawText(d, label, float64(b.Min.X)+(float64(b.Dx())-u.TextWidth(label))/2, float64(b.Min.Y+(b.Dy()-ui.FontSize)/2-1), col)
		})
	}
	if p.truncated {
		u.Text(image.Rect(x+6, by, r.Max.X, by+24), "list cut: too many files", u.Theme.TextDim)
	}

	body := image.Rect(r.Min.X, r.Min.Y+32, r.Max.X, r.Max.Y)
	if p.dir == "" {
		u.Text(image.Rect(body.Min.X+8, body.Min.Y+4, body.Max.X, body.Min.Y+4+ui.LineHeight), "This project has no folder yet — there are no files to show.", u.Theme.TextDim)
		return
	}
	split := body.Min.X + min(body.Dx()*62/100, body.Dx()-projPreviewW-24)
	list := image.Rect(body.Min.X, body.Min.Y, max(split, body.Min.X+120), body.Max.Y)
	prev := image.Rect(list.Max.X+6, body.Min.Y, body.Max.X, body.Max.Y)

	rows := s.visibleRows()
	if len(p.top) == 0 {
		u.Text(image.Rect(list.Min.X+8, list.Min.Y+4, list.Max.X, list.Min.Y+4+ui.LineHeight), "The project folder is empty.", u.Theme.TextDim)
	}
	if u.Hover(list) && u.Wheel != 0 {
		p.scroll -= int(u.Wheel * projRowH * 2)
	}
	p.scroll = min(max(p.scroll, 0), max(0, len(rows)*projRowH+8-list.Dy()))

	for i, e := range rows {
		row := image.Rect(list.Min.X, list.Min.Y+4+i*projRowH-p.scroll, list.Max.X, list.Min.Y+4+(i+1)*projRowH-p.scroll)
		vis := row.Intersect(list)
		if vis.Empty() {
			continue
		}
		hover := u.Hover(vis)
		if hover && u.Pressed {
			s.projClick(e)
		}
		selected := e.Path == p.sel
		col := kindColor(e.Kind)
		indent := 8 + e.Depth*14
		name := e.Name
		var arrow string
		if e.Kind == projfs.KindDir {
			arrow = "▼"
			if p.collapsed[e.Path] {
				arrow = "►"
			}
		}
		size := ""
		if e.Kind != projfs.KindDir {
			size = humanSize(e.Size)
		}
		rowC := row
		u.Emit(func(d *ebiten.Image) {
			sub := d.SubImage(list).(*ebiten.Image)
			switch {
			case selected:
				ui.FillRect(sub, rowC, color.RGBA{0x2f, 0x4a, 0x6e, 0xff})
			case hover:
				ui.FillRect(sub, rowC, u.Theme.PanelHead)
			}
			ty := float64(rowC.Min.Y + (rowC.Dy()-ui.FontSize)/2 - 1)
			tx := float64(rowC.Min.X + indent)
			if arrow != "" {
				u.DrawText(sub, arrow, tx, ty, col)
			} else if e.Kind.Usable() {
				ui.FillRect(sub, image.Rect(int(tx)+2, rowC.Min.Y+8, int(tx)+9, rowC.Min.Y+15), col)
			}
			u.DrawText(sub, name, tx+16, ty, col)
			if size != "" {
				u.DrawText(sub, size, float64(rowC.Max.X)-8-u.TextWidth(size), ty, u.Theme.TextDim)
			}
		})
	}

	s.projPreview(prev)
}

func (s *Studio) projClick(e *projfs.Entry) {
	p := &s.proj
	double := p.frame-p.lastClick < 25 && p.lastPath == e.Path // double-click (within ~0.4 s)
	p.lastClick, p.lastPath = p.frame, e.Path
	p.sel, p.selKind = e.Path, e.Kind
	switch {
	case e.Kind == projfs.KindDir:
		p.collapsed[e.Path] = !p.collapsed[e.Path] // a single click toggles a folder
	case double && e.Kind == projfs.KindCode:
		s.openInCode(e.Path)
	case double && e.Kind == projfs.KindScene:
		s.Logf("Opening other scenes is not supported yet: %s", e.Path)
	default:
		if e.Kind == projfs.KindImage || e.Kind == projfs.KindCode {
			p.press, p.pressK, p.pressAt = e.Path, e.Kind, image.Pt(s.ui.MX, s.ui.MY)
		}
	}
}

// projPreview shows details of the selected file; images get a thumbnail.
func (s *Studio) projPreview(r image.Rectangle) {
	u, p := s.ui, &s.proj
	u.Fill(r, color.RGBA{0x1f, 0x21, 0x28, 0xff})
	if p.sel == "" {
		u.Text(image.Rect(r.Min.X+8, r.Min.Y+4, r.Max.X, r.Min.Y+4+ui.LineHeight), "Select a file", u.Theme.TextDim)
		return
	}
	col := kindColor(p.selKind)
	line := func(i int) image.Rectangle {
		return image.Rect(r.Min.X+8, r.Min.Y+4+i*ui.LineHeight, r.Max.X-4, r.Min.Y+4+(i+1)*ui.LineHeight)
	}
	u.Text(line(0), u.FitText(path.Base(p.sel), float64(r.Dx()-16)), col)
	u.Text(line(1), u.FitText(p.sel, float64(r.Dx()-16)), u.Theme.TextDim)
	label := map[projfs.Kind]string{
		projfs.KindCode: "Code (TrueDSL)", projfs.KindImage: "Image", projfs.KindSound: "Sound",
		projfs.KindScene: "Scene", projfs.KindDir: "Folder", projfs.KindOther: "Not used by the game",
	}[p.selKind]
	u.Text(line(2), label, col)
	switch p.selKind {
	case projfs.KindImage:
		img := s.tex.get(p.sel)
		if img == nil {
			u.Text(line(3), "cannot be loaded", color.RGBA{0xff, 0x6b, 0x6b, 0xff})
			return
		}
		b := img.Bounds()
		u.Text(line(3), fmt.Sprintf("%d×%d", b.Dx(), b.Dy()), u.Theme.TextDim)
		box := image.Rect(r.Min.X+8, line(4).Min.Y+4, r.Min.X+8+min(projPreviewW, r.Dx()-16), r.Max.Y-4)
		if box.Dy() > 8 {
			mode := s.scene.ImageMode
			u.Emit(func(d *ebiten.Image) {
				sub := d.SubImage(box).(*ebiten.Image)
				checker(sub, box)
				drawImage(sub, img, fitRect(box, b.Dx(), b.Dy(), mode), mode)
			})
		}
	case projfs.KindCode:
		u.Text(line(3), "double-click to open in VCode", u.Theme.TextDim)
	case projfs.KindScene:
		name := strings.TrimSuffix(path.Base(p.sel), ".vide.json")
		u.Text(line(3), "use: go to scene \""+name+"\"", u.Theme.TextDim)
	case projfs.KindSound:
		u.Text(line(3), "use: play sound \""+path.Base(p.sel)+"\"", u.Theme.TextDim)
	}
}

// projDrag tracks dragging an image or script out of the list and drops it on the viewport.
func (s *Studio) projDrag() {
	u, p := s.ui, &s.proj
	if p.press == "" {
		return
	}
	if !u.Down {
		if p.drag {
			s.dropFile(p.press, p.pressK, u.MX, u.MY)
		}
		p.press, p.drag = "", false
		return
	}
	if !p.drag && abs(u.MX-p.pressAt.X)+abs(u.MY-p.pressAt.Y) > 6 {
		p.drag = true
	}
	if !p.drag {
		return
	}
	mx, my := u.MX, u.MY
	over := image.Pt(mx, my).In(s.vp)
	col := kindColor(p.pressK)
	label := path.Base(p.press)
	var img *ebiten.Image
	mode := s.scene.ImageMode
	if p.pressK == projfs.KindImage {
		img = s.tex.get(p.press)
	}
	u.Emit(func(d *ebiten.Image) {
		if over {
			ui.StrokeRect(d, s.vp, col)
		}
		if img != nil {
			b := image.Rect(mx+10, my+10, mx+10+48, my+10+48)
			drawImage(d, img, fitRect(b, img.Bounds().Dx(), img.Bounds().Dy(), mode), mode)
			return
		}
		g := image.Rect(mx+10, my+10, mx+10+int(u.TextWidth(label))+16, my+10+22)
		ui.FillRect(d, g, u.Theme.PanelHead)
		ui.StrokeRect(d, g, col)
		u.DrawText(d, label, float64(g.Min.X+8), float64(g.Min.Y+3), col)
	})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// dropFile applies a dropped file to the object under the cursor: an image
// becomes a sprite's texture (or a new sprite if the spot is empty), a script
// becomes the object's script.
func (s *Studio) dropFile(rel string, kind projfs.Kind, mx, my int) {
	if !image.Pt(mx, my).In(s.vp) {
		return
	}
	wx, wy := s.toWorld(mx, my)
	hit := s.hit(wx, wy)
	switch kind {
	case projfs.KindCode:
		if hit != nil && s.setScript(hit, rel) {
			s.sel = hit
		}
	case projfs.KindImage:
		if hit != nil && hit.Kind == scene.KindSprite {
			if s.setTexture(hit, rel) {
				s.sel = hit
			}
			return
		}
		name := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		n := s.scene.Add(nil, scene.KindSprite, name)
		if !s.setTexture(n, rel) {
			s.scene.Remove(n)
			return
		}
		n.X, n.Y = wx-n.W/2, wy-n.H/2
		s.sel = n
	}
}

func projDirFor(project string) string {
	if project == "" {
		return ""
	}
	return filepath.Dir(project)
}
