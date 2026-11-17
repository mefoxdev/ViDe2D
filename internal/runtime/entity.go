package runtime

import (
	"image/color"

	"github.com/vide2d/vide-studio/internal/scene"
)

// Behavior is the hook for per-entity logic: the script VM implements it
// (script.go), tests use small ones. It is attached through Options.Attach or,
// for scripts, by the world itself.
type Behavior interface {
	// Step runs once per simulation step, after Prev positions were saved.
	Step(w *World, e *Entity)
}

// Phase is one part of the frame order of reference.md section 8, in the
// order the phases run. Touches and messages will follow Timers.
type Phase int

// Phases of a simulation step.
const (
	PhaseStart  Phase = iota // "when start" of objects that have just appeared
	PhaseInput               // "when key ..."
	PhaseUpdate              // "when update"
	PhaseTimers              // "when every ..."
)

// Stateful is a Behavior whose state survives the unloading of its chunk:
// SnapshotState is called when the chunk unloads, RestoreState with that value
// when it loads again.
type Stateful interface {
	SnapshotState() any
	RestoreState(state any)
}

// Phased is a Behavior that takes part in the frame order: World.Step runs
// each phase for all entities (in scene-tree order) before the next phase.
type Phased interface {
	Behavior
	RunPhase(w *World, e *Entity, p Phase)
}

// Entity is one live thing in the world. X and Y are world coordinates in
// pixels (scene.Node.WorldPos semantics: the sum of the offsets of all
// ancestors, instances and chunks included). Scripts see them relative to the
// parent group (reference.md section 4); World.SetPos and the other World
// movement methods keep descendants in step when a parent moves.
type Entity struct {
	ID   int
	Name string
	Kind scene.Kind // Node, Sprite, Text or Instance
	Tags []string
	// Positions are fractional: a script may move an object by 0.5 pixels per
	// step. Pixel mode rounds only when drawing.
	X, Y     float64 // current position
	PrevX    float64 // position at the previous step, for render interpolation
	PrevY    float64
	W, H     float64
	Visible  bool
	Flip     bool    // mirrored horizontally
	Angle    float64 // degrees; stored, not drawn yet
	Layer    float64 // draw order hint; stored, not used yet
	Color    color.RGBA
	Texture  string // project-relative PNG path, "" for a plain color rectangle
	Text     string
	FontSize int
	Script   string // project-relative .tdsl path
	Parent   *Entity
	Children []*Entity // in scene-tree order
	Behavior Behavior
	// Dead is set when the entity was destroyed at the end of a step.
	Dead bool

	pendingDestroy bool
	fresh          bool       // created and not yet through its first step
	key            string     // stable identity of the map node inside its chunk ("" outside chunks)
	chunk          *chunkUnit // the chunk the entity belongs to, nil for scene and static map content
}

// HasTag reports whether the entity carries tag.
func (e *Entity) HasTag(tag string) bool {
	for _, t := range e.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// SetPos moves the entity; the previous position stays for interpolation.
func (e *Entity) SetPos(x, y float64) { e.X, e.Y = x, y }

// Teleport moves the entity without interpolation.
func (e *Entity) Teleport(x, y float64) { e.X, e.Y, e.PrevX, e.PrevY = x, y, x, y }

// Lerp returns the position blended between the previous and current step.
func (e *Entity) Lerp(alpha float64) (x, y float64) {
	return e.PrevX + (e.X-e.PrevX)*alpha, e.PrevY + (e.Y-e.PrevY)*alpha
}
