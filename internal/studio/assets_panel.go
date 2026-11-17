package studio

import (
	"fmt"
	"image"
	"path"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/assets"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	tileW, tileH = 84, 104
	tilePreview  = 64
)

type assetState struct {
	dir     string // assets folder; empty if the project has no location
	list    []assets.Info
	sel     string
	scroll  int
	press   string // asset pressed in the browser (candidate for dragging)
	pressAt image.Point
	drag    bool
}

func (s *Studio) refreshAssets() {
	s.tex.reset()
	list, err := assets.Scan(s.assets.dir)
	if err != nil {
		s.Logf("Asset scan failed: %v", err)
	}
	s.assets.list = list
	if s.assets.sel != "" && !hasAsset(list, s.assets.sel) {
		s.assets.sel = ""
	}
}

func hasAsset(list []assets.Info, p string) bool {
	for _, a := range list {
		if a.Path == p {
			return true
		}
	}
	return false
}

// setTexture assigns an asset to a sprite and resizes it to the image.
func (s *Studio) setTexture(n *scene.Node, p string) bool {
	if n == nil || n.Kind != scene.KindSprite {
		return false
	}
	img := s.tex.get(p)
	if img == nil {
		s.Logf("Cannot use %s: %v", p, s.tex.failed[p])
		return false
	}
	b := img.Bounds()
	n.Texture, n.W, n.H = p, b.Dx(), b.Dy()
	s.Logf("%s: texture %s (%d×%d)", n.Name, p, n.W, n.H)
	return true
}

func (s *Studio) assetsPanel(r image.Rectangle) {
	u := s.ui
	a := &s.assets
	btn := func(x int, label string, enabled bool) bool {
		return u.Button(image.Rect(x, r.Min.Y+4, x+80, r.Min.Y+28), label, enabled)
	}
	x := r.Min.X + 6
	if btn(x, "Refresh", true) {
		s.refreshAssets()
		s.Logf("Assets: %d image(s) in %s", len(a.list), a.dir)
	}
	canAssign := a.sel != "" && s.sel != nil && s.sel.Kind == scene.KindSprite
	if btn(x+86, "Assign", canAssign) {
		s.setTexture(s.sel, a.sel)
	}
	hint := fmt.Sprintf("%d image(s) • drag onto the viewport", len(a.list))
	u.Text(image.Rect(x+180, r.Min.Y+4, r.Max.X, r.Min.Y+28), hint, u.Theme.TextDim)

	c := image.Rect(r.Min.X, r.Min.Y+32, r.Max.X, r.Max.Y)
	if len(a.list) == 0 {
		msg := "No images yet. Put .png files into: " + a.dir
		if a.dir == "" {
			msg = "This project has no folder, so there is no assets folder."
		}
		u.Text(image.Rect(c.Min.X+8, c.Min.Y+4, c.Max.X, c.Min.Y+4+ui.LineHeight), msg, u.Theme.TextDim)
		return
	}

	cols := max(1, (c.Dx()-8)/tileW)
	rows := (len(a.list) + cols - 1) / cols
	if u.Hover(c) && u.Wheel != 0 {
		a.scroll -= int(u.Wheel * 40)
	}
	a.scroll = min(max(a.scroll, 0), max(0, rows*tileH+8-c.Dy()))

	mode := s.scene.ImageMode
	for i, info := range a.list {
		org := image.Pt(c.Min.X+8+(i%cols)*tileW, c.Min.Y+4+(i/cols)*tileH-a.scroll)
		tr := image.Rectangle{Min: org, Max: org.Add(image.Pt(tileW-4, tileH-4))}
		vis := tr.Intersect(c)
		if vis.Empty() {
			continue
		}
		hover := u.Hover(vis)
		if hover && u.Pressed {
			a.sel, a.press, a.pressAt = info.Path, info.Path, image.Pt(u.MX, u.MY)
		}
		selected := info.Path == a.sel
		img := s.tex.get(info.Path)
		name := u.FitText(strings.TrimSuffix(path.Base(info.Path), path.Ext(info.Path)), float64(tileW-12))
		size := fmt.Sprintf("%d×%d", info.W, info.H)
		u.Emit(func(d *ebiten.Image) {
			sub := d.SubImage(c).(*ebiten.Image)
			switch {
			case selected:
				ui.FillRect(sub, tr, u.Theme.Button)
				ui.StrokeRect(sub, tr, u.Theme.Accent)
			case hover:
				ui.FillRect(sub, tr, u.Theme.PanelHead)
			}
			box := image.Rect(tr.Min.X+6, tr.Min.Y+4, tr.Min.X+6+tilePreview, tr.Min.Y+4+tilePreview)
			checker(sub, box)
			if img != nil {
				drawImage(sub, img, fitRect(box, info.W, info.H, mode), mode)
			}
			u.DrawText(sub, name, float64(tr.Min.X+6), float64(box.Max.Y+2), u.Theme.Text)
			u.DrawText(sub, size, float64(tr.Min.X+6), float64(box.Max.Y+16), u.Theme.TextDim)
		})
	}
}

// assetDrag tracks dragging a tile out of the browser and drops it on the viewport.
func (s *Studio) assetDrag() {
	u, a := s.ui, &s.assets
	if a.press == "" {
		return
	}
	if !u.Down {
		if a.drag {
			s.dropAsset(a.press, u.MX, u.MY)
		}
		a.press, a.drag = "", false
		return
	}
	if !a.drag && abs(u.MX-a.pressAt.X)+abs(u.MY-a.pressAt.Y) > 6 {
		a.drag = true
	}
	if !a.drag {
		return
	}
	img, mode := s.tex.get(a.press), s.scene.ImageMode
	mx, my := u.MX, u.MY
	over := image.Pt(mx, my).In(s.vp)
	u.Emit(func(d *ebiten.Image) {
		if over {
			ui.StrokeRect(d, s.vp, u.Theme.Accent)
		}
		if img != nil {
			b := image.Rect(mx+10, my+10, mx+10+48, my+10+48)
			drawImage(d, img, fitRect(b, img.Bounds().Dx(), img.Bounds().Dy(), mode), mode)
		}
	})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// dropAsset assigns the asset to the sprite under the cursor, or creates a
// new sprite there.
func (s *Studio) dropAsset(p string, mx, my int) {
	if !image.Pt(mx, my).In(s.vp) {
		return
	}
	wx, wy := s.toWorld(mx, my)
	if n := s.hit(wx, wy); n != nil && n.Kind == scene.KindSprite {
		if s.setTexture(n, p) {
			s.sel = n
		}
		return
	}
	name := strings.TrimSuffix(path.Base(p), path.Ext(p))
	n := s.scene.Add(nil, scene.KindSprite, name)
	if !s.setTexture(n, p) {
		s.scene.Remove(n)
		return
	}
	n.X, n.Y = wx-n.W/2, wy-n.H/2
	s.sel = n
}

func assetsDirFor(project string) string {
	if project == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(project), "assets")
}
