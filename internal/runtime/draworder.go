package runtime

import (
	"cmp"
	"math"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/scene"
)

// Drawing is presentation only: nothing here touches the simulation, and
// none of it has to be deterministic.

// DrawList returns the objects to draw (visible sprites and texts) in the
// order they are drawn: by layer, lowest first, so a higher layer is on top.
// Objects with the same layer keep the scene-tree order of World.Each (map
// content, then the scene; parents before children), so the one lower in the
// panel is drawn over the one above it. The layer is global: it does not
// depend on groups (reference.md, section 4). buf is reused to avoid garbage.
func (w *World) DrawList(buf []*Entity) []*Entity {
	buf = buf[:0]
	ordered := true // the layers already do not decrease: no sorting needed
	w.Each(func(e *Entity) {
		if !e.Visible || (e.Kind != scene.KindSprite && e.Kind != scene.KindText) {
			return
		}
		if n := len(buf); n > 0 && e.Layer < buf[n-1].Layer {
			ordered = false
		}
		buf = append(buf, e)
	})
	if !ordered {
		sortByLayer(buf)
	}
	return buf
}

// sortByLayer orders by layer, keeping the given order inside a layer (a
// stable sort). A NaN layer (scripts cannot make one) counts as the lowest.
func sortByLayer(list []*Entity) {
	slices.SortStableFunc(list, func(a, b *Entity) int { return cmp.Compare(a.Layer, b.Layer) })
}

// drawGeo places an image of size (srcW, srcH) so that it fills the box of an
// object of size (w, h) whose top-left corner is at (x, y), turned by angle
// degrees (clockwise on the screen) around the center of the box. The
// rotation does not change which pixels the unrotated box covers.
func drawGeo(srcW, srcH, w, h, x, y, angle float64) ebiten.GeoM {
	return drawGeoFlip(srcW, srcH, w, h, x, y, angle, false)
}

// drawGeoFlip is drawGeo that can also mirror the image horizontally inside
// the box (before the rotation).
func drawGeoFlip(srcW, srcH, w, h, x, y, angle float64, flip bool) ebiten.GeoM {
	var g ebiten.GeoM
	g.Scale(w/srcW, h/srcH)
	if flip {
		g.Scale(-1, 1)
		g.Translate(w, 0)
	}
	if a := math.Mod(angle, 360); a != 0 && !math.IsNaN(a) {
		g.Translate(-w/2, -h/2)
		g.Rotate(a * math.Pi / 180)
		g.Translate(w/2, h/2)
	}
	g.Translate(x, y)
	return g
}
