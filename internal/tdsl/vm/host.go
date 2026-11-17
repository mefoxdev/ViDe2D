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
}

// Limits are the resource limits of docs/truedsl/safety.md.
type Limits struct {
	FrameSteps     int // instructions per frame, whole game
	HandlerSteps   int // instructions per handler run
	CallDepth      int // nested "to" calls
	ListItems      int // items in one list
	TextChars      int // characters in one text
	PrintsPerFrame int // "print" messages per frame, whole game
	PrintChars     int // characters kept from one "print" message
}

// MaxCoord bounds the numbers a script may write into object properties and
// movement: anything larger (or NaN, or infinite) is rejected.
const MaxCoord = 1e9

// DefaultLimits returns the limits from safety.md.
func DefaultLimits() Limits {
	return Limits{FrameSteps: 200_000, HandlerSteps: 50_000, CallDepth: 64, ListItems: 100_000, TextChars: 10_000, PrintsPerFrame: 1000, PrintChars: 1000}
}
