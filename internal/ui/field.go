package ui

import (
	"image"
	"strings"
	"unicode"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

const fieldPad = 6

// Focused reports whether a text field currently captures the keyboard.
func (c *Context) Focused() bool { return c.focus != "" || c.External }

// FocusField gives keyboard focus to the text field with the given id on the
// next frame, starting with value in its edit buffer.
func (c *Context) FocusField(id, value string) { c.nextFocus, c.nextBuf = id, value }

// KeyRepeat reports a key press with auto-repeat (in ticks).
func KeyRepeat(k ebiten.Key) bool { return repeat(k) }

// repeat reports a key press with auto-repeat (in ticks).
func repeat(k ebiten.Key) bool {
	d := inpututil.KeyPressDuration(k)
	return d == 1 || (d > 30 && d%3 == 0)
}

// Field draws an editable single-line text field and returns the edited text
// plus whether the edit was committed this frame (Enter, or click elsewhere).
// Escape cancels. When numeric is true only digits and a leading '-' are accepted.
// id must be stable and unique; if a focused field is not drawn for a frame it
// loses focus and its pending edit is dropped.
func (c *Context) Field(id string, r image.Rectangle, value string, numeric bool) (string, bool) {
	hover := c.Hover(r)
	focused := c.focus == id
	committed := false
	out := value

	if focused {
		c.focusSeen = true
		if c.Pressed {
			if hover {
				c.caret = c.caretAt(r, c.MX)
			} else {
				out, committed = string(c.buf), true
				c.focus, focused = "", false
			}
		}
	} else if hover && c.Pressed {
		c.nextFocus, c.nextBuf = id, value
	}

	if focused {
		for _, ch := range ebiten.AppendInputChars(nil) {
			if !unicode.IsPrint(ch) || (numeric && !(unicode.IsDigit(ch) || (ch == '-' && c.caret == 0 && !hasMinus(c.buf)))) {
				continue
			}
			c.buf = append(c.buf[:c.caret], append([]rune{ch}, c.buf[c.caret:]...)...)
			c.caret++
		}
		switch {
		case repeat(ebiten.KeyBackspace) && c.caret > 0:
			c.buf = append(c.buf[:c.caret-1], c.buf[c.caret:]...)
			c.caret--
		case repeat(ebiten.KeyDelete) && c.caret < len(c.buf):
			c.buf = append(c.buf[:c.caret], c.buf[c.caret+1:]...)
		case repeat(ebiten.KeyArrowLeft) && c.caret > 0:
			c.caret--
		case repeat(ebiten.KeyArrowRight) && c.caret < len(c.buf):
			c.caret++
		case inpututil.IsKeyJustPressed(ebiten.KeyHome):
			c.caret = 0
		case inpututil.IsKeyJustPressed(ebiten.KeyEnd):
			c.caret = len(c.buf)
		case inpututil.IsKeyJustPressed(ebiten.KeyEnter), inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter):
			out, committed = string(c.buf), true
			c.focus, focused = "", false
		case inpututil.IsKeyJustPressed(ebiten.KeyEscape):
			c.focus, focused = "", false
		}
	}

	shown := value
	caret := 0
	if focused {
		shown, caret = string(c.buf), c.caret
	}
	off := 0.0
	if focused {
		off = c.scrollOff(r, caret)
	}
	blink := focused && (c.frame/30)%2 == 0
	c.Emit(func(d *ebiten.Image) {
		bg, border := c.Theme.Bg, c.Theme.Border
		if focused {
			border = c.Theme.Accent
		} else if hover {
			bg = c.Theme.PanelHead
		}
		FillRect(d, r, bg)
		StrokeRect(d, r, border)
		sub := d.SubImage(r.Inset(1)).(*ebiten.Image)
		x := float64(r.Min.X+fieldPad) - off
		c.DrawText(sub, shown, x, float64(r.Min.Y+(r.Dy()-FontSize)/2-1), c.Theme.Text)
		if blink {
			cx := int(x + c.TextWidth(string([]rune(shown)[:caret])))
			FillRect(sub, image.Rect(cx, r.Min.Y+4, cx+1, r.Max.Y-4), c.Theme.Text)
		}
	})
	return out, committed
}

func hasMinus(b []rune) bool { return strings.ContainsRune(string(b), '-') }

// scrollOff returns the horizontal scroll that keeps the caret visible.
func (c *Context) scrollOff(r image.Rectangle, caret int) float64 {
	cx := c.TextWidth(string(c.buf[:caret]))
	if vis := float64(r.Dx() - 2*fieldPad); cx > vis {
		return cx - vis
	}
	return 0
}

// caretAt maps a mouse x position to a rune index in the focused buffer.
func (c *Context) caretAt(r image.Rectangle, mx int) int {
	target := float64(mx-r.Min.X-fieldPad) + c.scrollOff(r, c.caret)
	best, bestD := 0, 1e18
	for i := 0; i <= len(c.buf); i++ {
		d := c.TextWidth(string(c.buf[:i])) - target
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	return best
}
