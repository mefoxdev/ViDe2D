package studio

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/assets"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/ui"
)

// textures lazily decodes assets into GPU images. Failed loads are remembered
// until reset so that a broken file is not re-read every frame.
type textures struct {
	root   string
	cache  map[string]*ebiten.Image
	failed map[string]error
}

func newTextures(root string) *textures {
	return &textures{root: root, cache: map[string]*ebiten.Image{}, failed: map[string]error{}}
}

func (t *textures) reset() {
	t.cache, t.failed = map[string]*ebiten.Image{}, map[string]error{}
}

// get returns the image for rel, or nil if it cannot be loaded.
func (t *textures) get(rel string) *ebiten.Image {
	if img, ok := t.cache[rel]; ok {
		return img
	}
	if _, bad := t.failed[rel]; bad {
		return nil
	}
	src, err := assets.Decode(t.root, rel)
	if err != nil {
		t.failed[rel] = err
		return nil
	}
	img := ebiten.NewImageFromImage(src)
	t.cache[rel] = img
	return img
}

// drawImage draws img into the rectangle dst using the project's image mode.
// Pixel mode samples with nearest-neighbour; normal mode filters linearly.
func drawImage(d *ebiten.Image, img *ebiten.Image, dst image.Rectangle, mode scene.ImageMode) {
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 || dst.Empty() {
		return
	}
	op := &ebiten.DrawImageOptions{Filter: ebiten.FilterNearest}
	if mode == scene.ImageNormal {
		op.Filter = ebiten.FilterLinear
	}
	op.GeoM.Scale(float64(dst.Dx())/float64(b.Dx()), float64(dst.Dy())/float64(b.Dy()))
	op.GeoM.Translate(float64(dst.Min.X), float64(dst.Min.Y))
	d.DrawImage(img, op)
}

// fitRect returns the largest centered rectangle of the image's aspect ratio
// inside box. In pixel mode, upscaling uses whole-number factors only.
func fitRect(box image.Rectangle, w, h int, mode scene.ImageMode) image.Rectangle {
	if w < 1 || h < 1 {
		return image.Rectangle{}
	}
	var tw, th int
	if mode == scene.ImagePixel && w <= box.Dx() && h <= box.Dy() {
		k := min(box.Dx()/w, box.Dy()/h)
		tw, th = w*k, h*k
	} else {
		sc := min(float64(box.Dx())/float64(w), float64(box.Dy())/float64(h))
		tw, th = max(int(float64(w)*sc), 1), max(int(float64(h)*sc), 1)
	}
	x := box.Min.X + (box.Dx()-tw)/2
	y := box.Min.Y + (box.Dy()-th)/2
	return image.Rect(x, y, x+tw, y+th)
}

// checker draws a transparency checkerboard into r.
func checker(d *ebiten.Image, r image.Rectangle) {
	a, b := color.RGBA{0x2a, 0x2d, 0x35, 0xff}, color.RGBA{0x34, 0x38, 0x42, 0xff}
	const cs = 8
	for y := r.Min.Y; y < r.Max.Y; y += cs {
		for x := r.Min.X; x < r.Max.X; x += cs {
			c := a
			if ((x-r.Min.X)/cs+(y-r.Min.Y)/cs)%2 == 1 {
				c = b
			}
			ui.FillRect(d, image.Rect(x, y, min(x+cs, r.Max.X), min(y+cs, r.Max.Y)), c)
		}
	}
}
