package runtime

import (
	"image/color"

	"github.com/vide2d/vide-studio/internal/scene"
)

// Behavior is the hook for per-entity logic. Stage 1 has none; a script VM will
// implement it later and be attached through Options.Attach.
type Behavior interface {
	// Step runs once per simulation step, after Prev positions were saved.
	Step(w *World, e *Entity)
}

// Entity is one live thing in the world. Positions are world coordinates in
// pixels (scene.Node.WorldPos semantics: the sum of the offsets of all
// ancestors, instances and chunks included).
type Entity struct {
	ID       int
	Name     string
	Kind     scene.Kind // Node, Sprite, Text or Instance
	Tags     []string
	X, Y     int // current position
	PrevX    int // position at the previous step, for render interpolation
	PrevY    int
	W, H     int
	Color    color.RGBA
	Texture  string // project-relative PNG path, "" for a plain color rectangle
	Text     string
	FontSize int
	Script   string // project-relative .tdsl path; stored, not executed yet
	Parent   *Entity
	Behavior Behavior
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
func (e *Entity) SetPos(x, y int) { e.X, e.Y = x, y }

// Teleport moves the entity without interpolation.
func (e *Entity) Teleport(x, y int) { e.X, e.Y, e.PrevX, e.PrevY = x, y, x, y }

// Lerp returns the position blended between the previous and current step.
func (e *Entity) Lerp(alpha float64) (x, y float64) {
	return float64(e.PrevX) + float64(e.X-e.PrevX)*alpha, float64(e.PrevY) + float64(e.Y-e.PrevY)*alpha
}
