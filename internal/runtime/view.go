package runtime

import (
	"math"

	"github.com/vide2d/vide-studio/internal/scene"
)

// Placement says where the view image lands in the window.
type Placement struct {
	Scale float64 // factor applied to the view image; whole in pixel mode
	X, Y  float64 // top-left of the scaled view in the window
	W, H  float64 // size of the scaled view; the rest of the window is bars
}

// Fit places a viewW x viewH view into a winW x winH window.
//
// Pixel mode uses the largest whole scale that fits (at least 1) and a
// whole-pixel offset, so every game pixel is a square block of window pixels.
// Normal mode scales smoothly by the largest factor that keeps the aspect ratio.
// Either way the view is centered and the leftover is letterbox bars.
func Fit(winW, winH, viewW, viewH int, mode scene.ImageMode) Placement {
	if winW < 1 || winH < 1 || viewW < 1 || viewH < 1 {
		return Placement{Scale: 1}
	}
	var s float64
	if mode == scene.ImageNormal {
		s = min(float64(winW)/float64(viewW), float64(winH)/float64(viewH))
	} else {
		s = float64(max(1, min(winW/viewW, winH/viewH)))
	}
	p := Placement{Scale: s, W: float64(viewW) * s, H: float64(viewH) * s}
	p.X, p.Y = float64(winW)-p.W, float64(winH)-p.H
	if mode == scene.ImageNormal {
		p.X, p.Y = p.X/2, p.Y/2
	} else {
		p.X, p.Y = math.Floor(p.X/2), math.Floor(p.Y/2) // whole pixels; may be negative if cropped
	}
	return p
}

// ToView converts a point of the window (device pixels) to view coordinates:
// pixels of the game view with (0, 0) at its top-left corner. The point may lie
// outside the view (on a letterbox bar), then the result is outside
// [0, viewW) x [0, viewH).
func (p Placement) ToView(winX, winY float64) (x, y float64) {
	if p.Scale <= 0 {
		return 0, 0
	}
	return float64(winX-p.X) / p.Scale, float64(winY-p.Y) / p.Scale
}
