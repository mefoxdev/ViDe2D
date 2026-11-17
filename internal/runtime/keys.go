package runtime

import (
	"strconv"

	"github.com/hajimehoshi/ebiten/v2"
)

// keyTable maps the key names of TrueDSL (reference.md section 5) to
// Ebitengine keys: the named keys, the letters a..z, and for everything else
// the quoted names such as "1" or "f5".
var keyTable = func() map[string]ebiten.Key {
	m := map[string]ebiten.Key{
		"left": ebiten.KeyArrowLeft, "right": ebiten.KeyArrowRight, "up": ebiten.KeyArrowUp, "down": ebiten.KeyArrowDown,
		"space": ebiten.KeySpace, "enter": ebiten.KeyEnter, "escape": ebiten.KeyEscape,
		"shift": ebiten.KeyShift, "ctrl": ebiten.KeyControl, "alt": ebiten.KeyAlt,
		"tab": ebiten.KeyTab, "backspace": ebiten.KeyBackspace,
	}
	for c := 'a'; c <= 'z'; c++ {
		m[string(c)] = ebiten.KeyA + ebiten.Key(c-'a')
	}
	for d := 0; d <= 9; d++ {
		m[strconv.Itoa(d)] = ebiten.Key0 + ebiten.Key(d)
	}
	for f := 1; f <= 12; f++ {
		m["f"+strconv.Itoa(f)] = ebiten.KeyF1 + ebiten.Key(f-1)
	}
	return m
}()

// KnownKey reports whether name is a key name scripts can use.
func KnownKey(name string) bool {
	_, ok := keyTable[name]
	return ok
}

// pollKeys copies the state of the real keyboard into in. Called once per
// display frame, before the simulation steps of that frame.
func pollKeys(in *InputState) {
	for name, key := range keyTable {
		if ebiten.IsKeyPressed(key) {
			in.Press(name)
		} else {
			in.Release(name)
		}
	}
}

// pollMouse copies the state of the real mouse (or the first finger on a touch
// screen) into in. The cursor is given in window pixels; p says where the view
// lies in the window, so the scripts see view coordinates.
func pollMouse(in *InputState, p Placement) {
	x, y := ebiten.CursorPosition()
	held := ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	if ids := ebiten.AppendTouchIDs(nil); len(ids) > 0 {
		x, y = ebiten.TouchPosition(ids[0])
		held = true
	}
	in.SetMouse(p.ToView(float64(x), float64(y)))
	in.SetMouseHeld(held)
}
