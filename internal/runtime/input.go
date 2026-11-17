package runtime

// Input is the keyboard state the scripts see (reference.md section 5: the
// keys left, right, up, down, space, enter, escape, shift, ctrl, a..z and
// other keys by their quoted name such as "1").
//
// Pressed and Released are edges: true in the first simulation step after the
// key went down or up, then false again. Advance is called by the world after
// every step to consume the edges, so an edge is seen by exactly one step even
// when the display runs faster or slower than the simulation.
type Input interface {
	Held(key string) bool
	Pressed(key string) bool
	Released(key string) bool
	Advance()
}

// InputState is a plain, headless Input. The Ebitengine front end feeds it
// from the real keyboard; tests feed it by hand.
type InputState struct {
	held, pressed, released map[string]bool

	mx, my                   float64 // cursor, in view coordinates
	mHeld, mPressed, mReleas bool
	mSeen                    bool // the button state has been reported at least once
}

// NewInputState returns an input with nothing pressed.
func NewInputState() *InputState {
	return &InputState{held: map[string]bool{}, pressed: map[string]bool{}, released: map[string]bool{}}
}

// Press reports that key went down (calling it for a held key does nothing).
func (s *InputState) Press(key string) {
	if !s.held[key] {
		s.held[key] = true
		s.pressed[key] = true
	}
}

// Release reports that key went up.
func (s *InputState) Release(key string) {
	if s.held[key] {
		s.held[key] = false
		s.released[key] = true
	}
}

// Held reports whether the key is down right now.
func (s *InputState) Held(key string) bool { return s.held[key] }

// Pressed reports whether the key went down since the last step.
func (s *InputState) Pressed(key string) bool { return s.pressed[key] }

// Released reports whether the key went up since the last step.
func (s *InputState) Released(key string) bool { return s.released[key] }

// Advance forgets the edges; held keys and buttons stay held.
func (s *InputState) Advance() {
	clear(s.pressed)
	clear(s.released)
	s.mPressed, s.mReleas = false, false
}

// SetMouse moves the cursor to (x, y) in view coordinates.
func (s *InputState) SetMouse(x, y float64) { s.mx, s.my = x, y }

// SetMouseHeld reports the state of the main button from a poll of the real
// device. The first report only sets the initial state: a button that is
// already down when the game starts (the click on the Play button that
// launched it) is not a click in the game.
func (s *InputState) SetMouseHeld(held bool) {
	if !s.mSeen {
		s.mSeen = true
		s.mHeld = held
		return
	}
	if held {
		s.PressMouse()
	} else {
		s.ReleaseMouse()
	}
}

// PressMouse reports that the main button went down (calling it while the
// button is held does nothing).
func (s *InputState) PressMouse() {
	if !s.mHeld {
		s.mHeld, s.mPressed = true, true
	}
}

// ReleaseMouse reports that the main button went up.
func (s *InputState) ReleaseMouse() {
	if s.mHeld {
		s.mHeld, s.mReleas = false, true
	}
}

// Mouse implements MouseInput.
func (s *InputState) Mouse() (x, y float64, held bool) { return s.mx, s.my, s.mHeld }

// MousePressed implements MouseInput.
func (s *InputState) MousePressed() bool { return s.mPressed }

// MouseReleased reports whether the button went up since the last step.
func (s *InputState) MouseReleased() bool { return s.mReleas }
