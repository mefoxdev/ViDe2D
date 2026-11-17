package studio

import (
	"image"
	"testing"

	"github.com/vide2d/vide-studio/internal/scene"
)

func TestFitRect(t *testing.T) {
	box := image.Rect(0, 0, 64, 64)
	cases := []struct {
		w, h int
		mode scene.ImageMode
		want image.Rectangle
	}{
		{16, 16, scene.ImagePixel, image.Rect(0, 0, 64, 64)},   // 4x
		{20, 20, scene.ImagePixel, image.Rect(2, 2, 62, 62)},   // 3x only, never fractional
		{16, 16, scene.ImageNormal, image.Rect(0, 0, 64, 64)},  // fills
		{20, 20, scene.ImageNormal, image.Rect(0, 0, 64, 64)},  // smooth fit
		{128, 64, scene.ImagePixel, image.Rect(0, 16, 64, 48)}, // downscale keeps aspect
	}
	for _, c := range cases {
		if got := fitRect(box, c.w, c.h, c.mode); got != c.want {
			t.Errorf("%dx%d %s: got %v want %v", c.w, c.h, c.mode, got, c.want)
		}
	}
}
