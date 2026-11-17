package runtime

import (
	"bytes"
	"image/color"
	"log"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/vide2d/vide-studio/internal/scene"
)

// Game is the Ebitengine front end: it feeds real elapsed time to the
// simulation in Update and draws the world in Draw. It holds no game rules.
type Game struct {
	Sim    *Sim
	images *Images
	view   *ebiten.Image // offscreen, ViewW x ViewH
	tex    map[string]*ebiten.Image
	face   *text.GoTextFaceSource
	faces  map[int]*text.GoTextFace
	last   time.Time
	scale  float64 // device scale factor, for Layout
	scrW   int     // size of the window in layout pixels, as of the last Layout
	scrH   int
}

// NewGame prepares a game for a loaded world.
func NewGame(w *World, src *Source) *Game {
	face, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		log.Fatal(err)
	}
	return &Game{
		Sim: NewSim(w), images: NewImages(src), tex: map[string]*ebiten.Image{},
		face: face, faces: map[int]*text.GoTextFace{}, scale: 1,
	}
}

// Run opens the window and runs the game until it is closed.
func (g *Game) Run() error {
	w := g.Sim.World
	k := max(1, min(1280/w.ViewW, 720/w.ViewH))
	ebiten.SetWindowTitle(w.Name + " - ViDe2D")
	ebiten.SetWindowSize(w.ViewW*k, w.ViewH*k)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetTPS(ebiten.SyncWithFPS) // Update once per frame; the Clock makes the steps fixed
	err := ebiten.RunGame(g)
	// the game is over: save its memory and silence the sounds
	if ferr := g.Sim.World.Flush(); ferr != nil {
		log.Printf("memory of the game was not saved: %v", ferr)
	}
	g.Sim.World.StopSound()
	return err
}

// Update runs the due simulation steps. Wall-clock time is read only here, at
// the edge, and handed to the Clock as a duration.
func (g *Game) Update() error {
	// Escape belongs to the scripts; Ctrl+Q (or closing the window) quits.
	if ebiten.IsKeyPressed(ebiten.KeyControl) && ebiten.IsKeyPressed(ebiten.KeyQ) {
		return ebiten.Termination
	}
	if in, ok := g.Sim.World.Input.(*InputState); ok {
		pollKeys(in)
		w := g.Sim.World
		pollMouse(in, Fit(g.scrW, g.scrH, w.ViewW, w.ViewH, w.ImageMode))
	}
	now := time.Now()
	dt := time.Duration(0)
	if !g.last.IsZero() {
		dt = now.Sub(g.last)
	}
	g.last = now
	_, err := g.Sim.Update(dt)
	return err
}

// Layout gives the window's size in device pixels, so that the integer scale
// of pixel mode is a whole number of real screen pixels.
func (g *Game) Layout(w, h int) (int, int) { g.scrW, g.scrH = w, h; return w, h }

// LayoutF is Layout in device-independent units; see Layout.
func (g *Game) LayoutF(w, h float64) (float64, float64) {
	if s := ebiten.Monitor().DeviceScaleFactor(); s > 0 {
		g.scale = s
	}
	sw, sh := math.Ceil(w*g.scale), math.Ceil(h*g.scale)
	g.scrW, g.scrH = int(sw), int(sh)
	return sw, sh
}

// Draw renders the world into the view image and scales it into the window.
func (g *Game) Draw(screen *ebiten.Image) {
	w := g.Sim.World
	if g.view == nil || g.view.Bounds().Dx() != w.ViewW || g.view.Bounds().Dy() != w.ViewH {
		// the first frame, or a new scene with another view size: the window keeps its size
		if g.view != nil {
			g.view.Deallocate()
		}
		g.view = ebiten.NewImage(w.ViewW, w.ViewH)
	}
	g.view.Fill(color.RGBA{0x10, 0x11, 0x14, 0xff})
	g.drawWorld(g.view, g.Sim.Clock.Alpha())

	screen.Fill(color.Black) // letterbox bars
	b := screen.Bounds()
	p := Fit(b.Dx(), b.Dy(), w.ViewW, w.ViewH, w.ImageMode)
	op := &ebiten.DrawImageOptions{Filter: ebiten.FilterNearest}
	if w.ImageMode == scene.ImageNormal {
		op.Filter = ebiten.FilterLinear
	}
	op.GeoM.Scale(p.Scale, p.Scale)
	op.GeoM.Translate(p.X, p.Y)
	screen.DrawImage(g.view, op)
}

func (g *Game) drawWorld(dst *ebiten.Image, alpha float64) {
	w := g.Sim.World
	pixel := w.ImageMode != scene.ImageNormal
	filter := ebiten.FilterLinear
	if pixel {
		filter = ebiten.FilterNearest
	}
	w.Each(func(e *Entity) {
		if !e.Visible {
			return
		}
		x, y := e.Lerp(alpha)
		x, y = x-float64(w.CamX), y-float64(w.CamY)
		if pixel {
			x, y = math.Round(x), math.Round(y) // whole-pixel placement
		}
		switch e.Kind {
		case scene.KindSprite:
			g.drawSprite(dst, e, x, y, filter)
		case scene.KindText:
			g.drawText(dst, e, x, y)
		}
	})
}

func (g *Game) drawSprite(dst *ebiten.Image, e *Entity, x, y float64, filter ebiten.Filter) {
	if e.W <= 0 || e.H <= 0 {
		return
	}
	col := e.Color
	if e.Texture != "" {
		if img := g.texture(e.Texture); img != nil {
			b := img.Bounds()
			op := &ebiten.DrawImageOptions{Filter: filter}
			op.GeoM.Scale(float64(e.W)/float64(b.Dx()), float64(e.H)/float64(b.Dy()))
			op.GeoM.Translate(x, y)
			dst.DrawImage(img, op)
			return
		}
		col = color.RGBA{0xff, 0x00, 0xff, 0xff} // missing texture
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(e.W, e.H) // a plain rectangle looks the same mirrored
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(col)
	dst.DrawImage(pixelImage(), op)
}

var white *ebiten.Image

func pixelImage() *ebiten.Image {
	if white == nil {
		white = ebiten.NewImage(1, 1)
		white.Fill(color.White)
	}
	return white
}

func (g *Game) drawText(dst *ebiten.Image, e *Entity, x, y float64) {
	size := min(max(e.FontSize, 1), 256) // the cache key is the clamped size
	f := g.faces[size]
	if f == nil {
		f = &text.GoTextFace{Source: g.face, Size: float64(size)}
		g.faces[size] = f
	}
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(e.Color)
	text.Draw(dst, e.Text, f, op)
}

// texture returns the GPU image for rel, or nil if it cannot be loaded.
func (g *Game) texture(rel string) *ebiten.Image {
	if t, ok := g.tex[rel]; ok {
		return t
	}
	var t *ebiten.Image
	if img, err := g.images.Get(rel); err != nil {
		log.Printf("texture %s: %v", rel, err)
	} else {
		t = ebiten.NewImageFromImage(img)
	}
	g.tex[rel] = t
	return t
}
