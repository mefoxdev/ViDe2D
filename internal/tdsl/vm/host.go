package vm

import (
	"errors"

	"github.com/vide2d/vide-studio/internal/tdsl"
)

// ObjID identifies an object of the world to the VM.
type ObjID int

// Prop is a built-in object property (reference.md, section 4).
type Prop uint8

// Built-in properties.
const (
	PropNone Prop = iota
	PropX
	PropY
	PropWidth
	PropHeight
	PropAngle
	PropVisible
	PropImage
	PropTint
	PropFlip
	PropLayer
	PropTag
	PropName
	PropText
	PropFontSize
)

var propByName = map[string]Prop{
	"x": PropX, "y": PropY, "width": PropWidth, "height": PropHeight, "angle": PropAngle,
	"visible": PropVisible, "image": PropImage, "tint": PropTint, "flip": PropFlip,
	"layer": PropLayer, "tag": PropTag, "name": PropName, "text": PropText, "font_size": PropFontSize,
}

// propKind is the type each property holds.
var propKind = [...]Kind{
	PropX: KNum, PropY: KNum, PropWidth: KNum, PropHeight: KNum, PropAngle: KNum,
	PropVisible: KBool, PropImage: KText, PropTint: KText, PropFlip: KBool,
	PropLayer: KNum, PropTag: KText, PropName: KText, PropText: KText, PropFontSize: KNum,
}

// Starter is an optional part of a Host: it creates (and initializes) the
// script instance of an object that has not run yet, so that another script
// can read and write its variables at once.
type Starter interface {
	EnsureInstance(id ObjID)
}

// ErrGone is returned by a Host when the object no longer exists (TD303).
var ErrGone = errors.New("object is gone")

// HostError is an error a Host can return with its own TD code and message,
// for example TD304 when "image" names a file that does not exist.
type HostError struct {
	Code, Message, Hint string
}

func (e *HostError) Error() string { return e.Message }

// Host is everything a script can touch. The VM validates types before it
// calls SetProp, so a Host may rely on the value kind matching the property.
// Hosts must do their own arithmetic with explicit float64() conversions.
type Host interface {
	// TickRate is the simulation rate in steps per second (60, 90, 120).
	TickRate() int
	// Seed seeds the PRNG shared by all scripts of the engine.
	Seed() uint64
	// GetProp and SetProp read and write a property of an object.
	GetProp(obj ObjID, p Prop) (Value, error)
	SetProp(obj ObjID, p Prop, v Value) error
	// MoveBy shifts the object by (dx, dy) pixels; MoveTo puts it at (x, y).
	MoveBy(obj ObjID, dx, dy float64) error
	MoveTo(obj ObjID, x, y float64) error
	// Destroy removes the object at the end of the frame.
	Destroy(obj ObjID) error
	// Log receives the output of "print".
	Log(text string)
	// Key reports the state of the named key in the current step.
	Key(name string, state tdsl.KeyState) bool

	// World queries. Objects are axis-aligned boxes in world coordinates
	// (an object without size is a point); results are in scene-tree order.
	// "work" is the number of boxes examined: the VM charges it to the step
	// budget (one step per four boxes), which keeps broad queries within limits.

	// Find returns the first live object with the name.
	Find(name string) (ObjID, bool)
	// Exists reports whether the object is alive (a destroyed object stays alive
	// until the end of its frame).
	Exists(obj ObjID) bool
	// All lists the live objects that carry the tag; it fails if there are more than limit.
	All(tag string, limit int) (ids []ObjID, work int, err error)
	// Count is the number of live objects that carry the tag.
	Count(tag string) int
	// Distance is the distance between the centers of two objects.
	Distance(a, b ObjID) (float64, error)
	// Touching reports whether self overlaps an object of the target; with
	// useDir it asks whether something stands flush against self in direction dir.
	Touching(self ObjID, t Target, dir Dir, useDir bool) (yes bool, work int, err error)
	// Overlapping lists the objects of the target that overlap self, in scene
	// order, appending to buf[:0].
	Overlapping(self ObjID, t Target, buf []ObjID) (ids []ObjID, work int, err error)
	// MoveAvoiding moves self by (dx, dy) but stops flush against the first
	// object carrying tag.
	MoveAvoiding(self ObjID, dx, dy float64, tag string) (work int, err error)
	// Spawn creates a copy of the template at world position (x, y). The new
	// object joins the world at the end of the frame. It returns the number of
	// objects created, for the step budget.
	Spawn(template string, x, y float64) (id ObjID, work int, err error)
	// Mouse is the cursor position in world coordinates and whether the button is down.
	Mouse() (x, y float64, held bool)
	// Clicked reports whether the mouse button went down in this step over the object.
	Clicked(self ObjID) bool

	Services
}

// Mailbox is an optional part of a Host: it takes a message sent "to" an
// object that has an instance but is not in the world right now (an alive
// object of an unloaded chunk sleeps), so that it can be handed over later.
// Only plain values (number, text, yes/no) arrive: Post gets a list or object
// as nothing, which the host sees as the zero Value.
type Mailbox interface {
	Post(to ObjID, text string, value Value)
}

// Services is the part of the Host that is not about objects: sound, scenes
// and the memory of the game. Every method is called while a script runs and
// must be cheap or report its work.
type Services interface {
	// PlaySound and PlayMusic start a sound or the (looping) music. name is the
	// text from the script: the host finds the file (HostError TD304 if it
	// cannot). A new "play music" replaces the music that plays.
	PlaySound(name string) error
	PlayMusic(name string) error
	// StopMusic stops the music; it does nothing if none plays.
	StopMusic()
	// GoToScene asks to switch to the scene at the end of the frame. The host
	// checks the name at once (HostError TD305 for an unknown scene).
	GoToScene(name string) error
	// RestartScene asks to start the current scene anew at the end of the frame.
	RestartScene()
	// Remember stores a value in the game's memory (HostError TD402 when the
	// limits are exceeded); work is the number of items looked at, for the
	// step budget. Only numbers, texts, yes/no and lists of them reach it.
	Remember(key string, v Value) (work int, err error)
	// Recall reads the value stored under key (a copy: a list is not shared).
	Recall(key string) (v Value, ok bool, work int)
}

// NoServices implements Services for a host without sound, scenes or memory:
// sounds and scene changes do nothing, nothing is remembered. Embed it in
// small test hosts.
type NoServices struct{}

func (NoServices) PlaySound(string) error              { return nil }
func (NoServices) PlayMusic(string) error              { return nil }
func (NoServices) StopMusic()                          {}
func (NoServices) GoToScene(string) error              { return nil }
func (NoServices) RestartScene()                       {}
func (NoServices) Remember(string, Value) (int, error) { return 0, nil }
func (NoServices) Recall(string) (Value, bool, int)    { return Value{}, false, 0 }

// Target says what a touch query is about: one object (HasObj) or all the
// objects that carry Tag.
type Target struct {
	Tag    string
	Obj    ObjID
	HasObj bool
}

// NoWorld implements the world queries of Host for a host without a world:
// nothing is found, nothing touches. Embed it in small test hosts.
type NoWorld struct{}

func (NoWorld) Find(string) (ObjID, bool)             { return 0, false }
func (NoWorld) Exists(ObjID) bool                     { return false }
func (NoWorld) All(string, int) ([]ObjID, int, error) { return nil, 0, nil }
func (NoWorld) Count(string) int                      { return 0 }
func (NoWorld) Distance(a, b ObjID) (float64, error)  { return 0, ErrGone }
func (NoWorld) Touching(ObjID, Target, Dir, bool) (bool, int, error) {
	return false, 0, nil
}
func (NoWorld) Overlapping(ObjID, Target, []ObjID) ([]ObjID, int, error)  { return nil, 0, nil }
func (NoWorld) MoveAvoiding(ObjID, float64, float64, string) (int, error) { return 0, nil }
func (NoWorld) Spawn(string, float64, float64) (ObjID, int, error) {
	return 0, 0, &HostError{Code: CodeUnsupported, Message: "В этом мире нельзя создавать объекты."}
}
func (NoWorld) Mouse() (float64, float64, bool) { return 0, 0, false }
func (NoWorld) Clicked(ObjID) bool              { return false }

// Limits are the resource limits of docs/truedsl/safety.md.
type Limits struct {
	FrameSteps     int // instructions per frame, whole game
	HandlerSteps   int // instructions per handler run
	CallDepth      int // nested "to" calls
	ListItems      int // items in one list
	TextChars      int // characters in one text
	PrintsPerFrame int // "print" messages per frame, whole game
	PrintChars     int // characters kept from one "print" message
	SendsPerFrame  int // "send" commands per frame, whole game
	Backlog        int // messages waiting for delivery (sent but not yet delivered)
	Objects        int // live objects made by "spawn"
	Waiting        int // suspended handler runs ("wait", "slide") in the whole game
	WaitingPerObj  int // suspended handler runs of one object
	SnapshotTasks  int // suspended runs kept in the saved state of unloaded objects, whole game
	SnapshotItems  int // values (list items included) kept for the suspended runs of one saved object
}

// MaxCoord bounds the numbers a script may write into object properties and
// movement: anything larger (or NaN, or infinite) is rejected.
const MaxCoord = 1e9

// DefaultLimits returns the limits from safety.md.
func DefaultLimits() Limits {
	return Limits{FrameSteps: 200_000, HandlerSteps: 50_000, CallDepth: 64, ListItems: 100_000, TextChars: 10_000, PrintsPerFrame: 1000, PrintChars: 1000,
		SendsPerFrame: 1000, Backlog: 5000, Objects: 5000, Waiting: 10_000, WaitingPerObj: 16,
		SnapshotTasks: 10_000, SnapshotItems: 50_000}
}
