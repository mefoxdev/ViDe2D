// Package ui is a tiny immediate-mode UI layer on top of Ebitengine.
//
// Widgets are evaluated in Update (they return interaction results) and
// record draw commands that are replayed in Draw.
package ui

import (
	"bytes"
	"image"
	"image/color"
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/goregular"
)

// Theme holds the editor colors.
type Theme struct {
	Bg, Panel, PanelHead, Border, Text, TextDim, Accent, Button, ButtonHover, Grid color.RGBA
}

// DarkTheme is the default theme.
func DarkTheme() Theme {
	return Theme{
		Bg:          color.RGBA{0x1b, 0x1d, 0x23, 0xff},
		Panel:       color.RGBA{0x25, 0x28, 0x30, 0xff},
		PanelHead:   color.RGBA{0x2e, 0x32, 0x3c, 0xff},
		Border:      color.RGBA{0x12, 0x13, 0x17, 0xff},
		Text:        color.RGBA{0xe2, 0xe5, 0xec, 0xff},
		TextDim:     color.RGBA{0x8b, 0x92, 0xa3, 0xff},
		Accent:      color.RGBA{0x4f, 0x9d, 0xff, 0xff},
		Button:      color.RGBA{0x37, 0x3c, 0x48, 0xff},
		ButtonHover: color.RGBA{0x46, 0x4c, 0x5b, 0xff},
		Grid:        color.RGBA{0x2a, 0x2d, 0x35, 0xff},
	}
}

const (
	FontSize   = 14
	LineHeight = 22
)

// Context carries input state, the theme and the recorded draw commands.
type Context struct {
	Theme Theme
	face  *text.GoTextFace

	MX, MY, DX, DY            int
	Down, Pressed             bool // left button
	Released                  bool
	MiddleDown, MiddlePressed bool
	Wheel                     float64
	Shift, Space              bool

	cmds []func(dst *ebiten.Image)

	// text field state
	focus, nextFocus, nextBuf string
	focusSeen                 bool
	buf                       []rune
	caret, frame              int
}

// New creates a Context with the built-in font.
func New() *Context {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		log.Fatal(err)
	}
	return &Context{Theme: DarkTheme(), face: &text.GoTextFace{Source: src, Size: FontSize}}
}

// Begin polls input and clears the command list. Call at the start of Update.
func (c *Context) Begin() {
	x, y := ebiten.CursorPosition()
	c.DX, c.DY = x-c.MX, y-c.MY
	c.MX, c.MY = x, y
	c.Down = ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	c.Pressed = inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	c.Released = inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft)
	c.MiddleDown = ebiten.IsMouseButtonPressed(ebiten.MouseButtonMiddle)
	c.MiddlePressed = inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonMiddle)
	_, c.Wheel = ebiten.Wheel()
	c.Shift = ebiten.IsKeyPressed(ebiten.KeyShift)
	c.frame++
	if c.focus != "" && !c.focusSeen {
		c.focus = "" // focused field was not drawn: drop the edit
	}
	c.focusSeen = false
	if c.nextFocus != "" {
		c.focus, c.buf = c.nextFocus, []rune(c.nextBuf)
		c.caret, c.frame, c.nextFocus = len(c.buf), 0, ""
		c.focusSeen = true
	}
	c.Space = c.focus == "" && ebiten.IsKeyPressed(ebiten.KeySpace)
	c.cmds = c.cmds[:0]
}

// Render replays recorded commands. Call from Draw.
func (c *Context) Render(dst *ebiten.Image) {
	dst.Fill(c.Theme.Bg)
	for _, f := range c.cmds {
		f(dst)
	}
}

// Emit records a custom draw command.
func (c *Context) Emit(f func(dst *ebiten.Image)) { c.cmds = append(c.cmds, f) }

// Hover reports whether the cursor is inside r.
func (c *Context) Hover(r image.Rectangle) bool { return image.Pt(c.MX, c.MY).In(r) }

// Fill draws a filled rectangle.
func (c *Context) Fill(r image.Rectangle, col color.RGBA) {
	c.Emit(func(d *ebiten.Image) { FillRect(d, r, col) })
}

// FillRect draws a filled rectangle onto dst.
func FillRect(dst *ebiten.Image, r image.Rectangle, col color.RGBA) {
	vector.FillRect(dst, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), col, false)
}

// StrokeRect draws a 1px rectangle outline onto dst.
func StrokeRect(dst *ebiten.Image, r image.Rectangle, col color.RGBA) {
	vector.StrokeRect(dst, float32(r.Min.X)+.5, float32(r.Min.Y)+.5, float32(r.Dx())-1, float32(r.Dy())-1, 1, col, false)
}

// DrawText draws s with its top-left corner at (x, y).
func (c *Context) DrawText(dst *ebiten.Image, s string, x, y float64, col color.RGBA) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(col)
	text.Draw(dst, s, c.face, op)
}

// TextWidth returns the pixel width of s.
func (c *Context) TextWidth(s string) float64 {
	w, _ := text.Measure(s, c.face, 0)
	return w
}

// Text records a text label inside a line of LineHeight at r.
func (c *Context) Text(r image.Rectangle, s string, col color.RGBA) {
	c.Emit(func(d *ebiten.Image) {
		c.DrawText(d, s, float64(r.Min.X), float64(r.Min.Y+(r.Dy()-FontSize)/2-1), col)
	})
}

// Panel draws a titled panel and returns its content rectangle.
func (c *Context) Panel(r image.Rectangle, title string) image.Rectangle {
	head := image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+LineHeight+2)
	c.Emit(func(d *ebiten.Image) {
		FillRect(d, r, c.Theme.Panel)
		FillRect(d, head, c.Theme.PanelHead)
		StrokeRect(d, r, c.Theme.Border)
	})
	c.Text(head.Inset(8).Intersect(head), title, c.Theme.TextDim)
	return image.Rect(r.Min.X+1, head.Max.Y, r.Max.X-1, r.Max.Y-1)
}

// Button draws a button and reports whether it was clicked this frame.
func (c *Context) Button(r image.Rectangle, label string, enabled bool) bool {
	hover := enabled && c.Hover(r)
	col, tcol := c.Theme.Button, c.Theme.Text
	if hover {
		col = c.Theme.ButtonHover
	}
	if !enabled {
		tcol = c.Theme.TextDim
	}
	c.Emit(func(d *ebiten.Image) {
		FillRect(d, r, col)
		StrokeRect(d, r, c.Theme.Border)
		c.DrawText(d, label, float64(r.Min.X)+(float64(r.Dx())-c.TextWidth(label))/2, float64(r.Min.Y+(r.Dy()-FontSize)/2-1), tcol)
	})
	return hover && c.Pressed
}
