package client

import (
	"bytes"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"
)

const (
	rowH    = 44
	margin  = 16
	headerH = 56
)

// Shell is the library screen: a list of games; Enter, a click or a tap starts
// one. While an in-process game runs it owns the window; Ctrl+Q, or holding
// three fingers on a touch screen, returns to the list.
type Shell struct {
	Launcher Launcher
	// Dir is the games folder; it may be set after construction (mobile).
	Dir string

	entries  []Entry
	scanned  string
	sel      int
	top      int // first visible row
	status   string
	running  ebiten.Game
	touchers map[ebiten.TouchID]int // touch id -> frames held
	face     *text.GoTextFaceSource
	h        int // window height from the last Layout
}

// NewShell makes the shell for a games folder.
func NewShell(dir string, l Launcher) *Shell {
	face, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		panic(err) // the embedded font is a build-time constant
	}
	return &Shell{Dir: dir, Launcher: l, face: face, touchers: map[ebiten.TouchID]int{}}
}

// Rescan reloads the list from the games folder.
func (s *Shell) Rescan() {
	s.entries = Scan(s.Dir)
	s.scanned = s.Dir
	s.sel, s.top = 0, 0
	if len(s.entries) == 0 {
		s.status = "No games in " + s.Dir
	} else {
		s.status = ""
	}
}

// Update implements ebiten.Game.
func (s *Shell) Update() error {
	if s.running != nil {
		if s.leaveRequested() {
			s.running = nil
			return nil
		}
		if err := s.running.Update(); err != nil {
			s.running = nil // Termination or a game error: back to the list
			if err != ebiten.Termination {
				s.status = err.Error()
			}
		}
		return nil
	}
	if s.scanned != s.Dir {
		s.Rescan()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF5) {
		s.Rescan()
	}
	n := len(s.entries)
	if n > 0 {
		if inpututil.IsKeyJustPressed(ebiten.KeyDown) {
			s.sel = (s.sel + 1) % n
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyUp) {
			s.sel = (s.sel + n - 1) % n
		}
		_, wy := ebiten.Wheel()
		if wy != 0 {
			s.top = clamp(s.top-int(wy), 0, max(0, n-1))
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
			s.launch(s.sel)
		}
		if _, y, ok := pointerPress(); ok {
			if i := s.top + (y-headerH)/rowH; y >= headerH && i < n {
				s.sel = i
				s.launch(i)
			}
		}
	}
	s.keepVisible()
	return nil
}

func pointerPress() (x, y int, ok bool) {
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		x, y = ebiten.CursorPosition()
		return x, y, true
	}
	for _, id := range inpututil.AppendJustPressedTouchIDs(nil) {
		x, y = ebiten.TouchPosition(id)
		return x, y, true
	}
	return 0, 0, false
}

func (s *Shell) leaveRequested() bool {
	if ebiten.IsKeyPressed(ebiten.KeyControl) && ebiten.IsKeyPressed(ebiten.KeyQ) {
		return true
	}
	ids := ebiten.AppendTouchIDs(nil)
	held := map[ebiten.TouchID]int{}
	for _, id := range ids {
		held[id] = s.touchers[id] + 1
	}
	s.touchers = held
	if len(ids) < 3 {
		return false
	}
	for _, id := range ids {
		if held[id] < 60 {
			return false
		}
	}
	return true
}

func (s *Shell) launch(i int) {
	g, err := s.Launcher.Launch(s.entries[i])
	if err != nil {
		s.status = err.Error()
		return
	}
	s.status = ""
	s.running = g // nil for an external process: the list stays
}

func (s *Shell) visibleRows() int { return max(1, (s.h-headerH)/rowH) }

func (s *Shell) keepVisible() {
	vis := s.visibleRows()
	if s.sel < s.top {
		s.top = s.sel
	} else if s.sel >= s.top+vis {
		s.top = s.sel - vis + 1
	}
}

// Draw implements ebiten.Game.
func (s *Shell) Draw(screen *ebiten.Image) {
	if s.running != nil {
		s.running.Draw(screen)
		return
	}
	screen.Fill(color.RGBA{0x1b, 0x1d, 0x23, 0xff})
	s.text(screen, "ViDe2D", 22, margin, 14, color.RGBA{0xe8, 0xea, 0xf0, 0xff})
	for i := s.top; i < len(s.entries); i++ {
		y := headerH + (i-s.top)*rowH
		if y > screen.Bounds().Dy() {
			break
		}
		e := s.entries[i]
		fg := color.RGBA{0xe8, 0xea, 0xf0, 0xff}
		if i == s.sel {
			bar(screen, 0, y, screen.Bounds().Dx(), rowH, color.RGBA{0x2f, 0x4a, 0x7a, 0xff})
		}
		label := e.Name
		if e.Err != "" {
			label += "  (cannot open)"
			fg = color.RGBA{0xd0, 0x7a, 0x7a, 0xff}
		}
		s.text(screen, label, 18, margin, y+11, fg)
	}
	hint := s.status
	if hint == "" {
		hint = "Up/Down, Enter or tap: play.  F5: rescan.  In a game: Ctrl+Q or 3 fingers: back."
	}
	s.text(screen, hint, 13, margin, screen.Bounds().Dy()-24, color.RGBA{0x9a, 0xa0, 0xb0, 0xff})
}

func bar(dst *ebiten.Image, x, y, w, h int, c color.Color) {
	dst.SubImage(image.Rect(x, y, x+w, y+h)).(*ebiten.Image).Fill(c)
}

func (s *Shell) text(dst *ebiten.Image, str string, size float64, x, y int, c color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(float64(x), float64(y))
	op.ColorScale.ScaleWithColor(c)
	text.Draw(dst, str, &text.GoTextFace{Source: s.face, Size: size}, op)
}

// Layout implements ebiten.Game.
func (s *Shell) Layout(w, h int) (int, int) {
	if s.running != nil {
		return s.running.Layout(w, h)
	}
	s.h = h
	return w, h
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }
