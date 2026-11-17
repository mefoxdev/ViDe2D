package runtime

import (
	"fmt"
	"math"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// The world queries and actions of the script host (see vm.Host): collisions,
// lookups, spawning and the mouse. The geometry rules are in collide.go.

func (h *scriptHost) Find(name string) (vm.ObjID, bool) {
	e := h.w.findByName(name)
	h.charge()
	if e != nil {
		return vm.ObjID(e.ID), true
	}
	return 0, false
}

// charge takes the work the world did on the side (rebuilding its index,
// recomputing the boxes of groups) off the step budget of the frame.
func (h *scriptHost) charge() {
	if n := h.w.takeWork(); n > 0 {
		h.w.scripts.eng.Charge(n)
	}
}

func (h *scriptHost) Exists(id vm.ObjID) bool {
	if l := h.w.life; l != nil {
		if e := l.u.sleepByID[int(id)]; e != nil {
			return !e.Dead
		}
	}
	e := h.w.byID[int(id)]
	return e != nil && !e.Dead
}

func (h *scriptHost) All(tag string, limit int) ([]vm.ObjID, int, error) {
	h.w.collEnsure()
	h.charge()
	list := h.w.coll.tags[tag]
	if len(list) > limit {
		return nil, 0, &vm.HostError{Code: vm.CodeTooBig, Message: fmt.Sprintf("Слишком много объектов с меткой «%s»: в списке не больше %d элементов.", tag, limit)}
	}
	out := make([]vm.ObjID, len(list))
	for i, e := range list {
		out[i] = vm.ObjID(e.ID)
	}
	return out, len(list), nil
}

func (h *scriptHost) Count(tag string) int {
	h.w.collEnsure()
	h.charge()
	return len(h.w.coll.tags[tag])
}

func (h *scriptHost) Distance(a, b vm.ObjID) (float64, error) {
	ea, err := h.ent(a)
	if err != nil {
		return 0, err
	}
	eb, err := h.ent(b)
	if err != nil {
		return 0, err
	}
	for _, e := range []*Entity{ea, eb} {
		if e.pending {
			return 0, &vm.HostError{Code: vm.CodeGone, Message: "Объект только что создан командой «spawn» и войдёт в игру в следующем кадре: расстояние до него пока не считается.",
				Hint: "Считайте расстояние в следующем кадре (например, в «when update»)."}
		}
	}
	d := h.w.distanceBetween(ea, eb)
	h.charge()
	return d, nil
}

func (h *scriptHost) Touching(self vm.ObjID, t vm.Target, dir vm.Dir, useDir bool) (bool, int, error) {
	if h.w.life != nil {
		return false, 0, nil // no collisions offline
	}
	e, err := h.ent(self)
	if err != nil {
		return false, 0, err
	}
	yes, work := h.w.touchesAny(e, t, dir, useDir)
	return yes, work + h.w.takeWork(), nil
}

func (h *scriptHost) Overlapping(self vm.ObjID, t vm.Target, buf []vm.ObjID) ([]vm.ObjID, int, error) {
	if h.w.life != nil {
		return buf, 0, nil
	}
	e, err := h.ent(self)
	if err != nil {
		return buf, 0, err
	}
	ents, work := h.w.overlapping(e, t)
	work += h.w.takeWork()
	for _, o := range ents {
		buf = append(buf, vm.ObjID(o.ID))
	}
	return buf, work, nil
}

func (h *scriptHost) MoveAvoiding(self vm.ObjID, dx, dy float64, tag string) (int, error) {
	e, err := h.entW(self)
	if err != nil {
		return 0, err
	}
	if dx == 0 && dy == 0 {
		return 0, nil
	}
	allowed, work := math.Inf(1), 0 // offline there is nothing to avoid: a plain move
	if h.w.life == nil {
		allowed, work = h.w.allowedMove(e, dx, dy, tag)
	}
	work += h.w.takeWork()
	// move the whole distance when nothing is in the way, so that the result is
	// the same number as with a plain "move"
	if dx != 0 && allowed < abs(dx) {
		dx = copySign(allowed, dx)
	}
	if dy != 0 && allowed < abs(dy) {
		dy = copySign(allowed, dy)
	}
	nx, ny := float64(e.X+dx), float64(e.Y+dy)
	if err := checkCoord(nx, ny); err != nil {
		return work, err
	}
	h.w.Place(e, nx, ny, false)
	work += h.w.takeWork()
	return work, nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func copySign(mag, sign float64) float64 {
	if sign < 0 {
		return -mag
	}
	return mag
}

func (h *scriptHost) Spawn(name string, x, y float64) (vm.ObjID, int, error) {
	if h.w.life != nil {
		return 0, 0, errOfflineNoSpawn
	}
	if err := checkCoord(x, y); err != nil {
		return 0, 0, err
	}
	if h.w.spawnDepth >= maxSpawnNesting {
		return 0, 0, &vm.HostError{Code: vm.CodeDeep,
			Message: fmt.Sprintf("Слишком глубокое создание объектов: «spawn» запускается из начальных переменных создаваемых объектов больше %d раз подряд.", maxSpawnNesting),
			Hint:    "Шаблон создаёт копию самого себя при создании. Создавайте новый объект не в «var», а по условию в «when update» или «when start»."}
	}
	ents, err := h.w.spawn(name, x, y, h.w.scripts.eng.Limits.Objects)
	if err != nil {
		return 0, 0, err
	}
	h.w.spawnDepth++
	h.w.scripts.initSpawned(ents)
	h.w.spawnDepth--
	return vm.ObjID(ents[0].ID), len(ents), nil
}

// maxSpawnNesting bounds how deep "spawn" may call itself through the variable
// initializers of the objects it makes.
const maxSpawnNesting = 16

// MouseInput is the mouse part of Input. Positions are in view coordinates:
// pixels of the game view with (0, 0) at its top-left corner. An Input that
// does not implement it has no mouse.
type MouseInput interface {
	// Mouse is the cursor position and whether the main button (or a finger) is down.
	Mouse() (x, y float64, held bool)
	// MousePressed is true in the first step after the button went down.
	MousePressed() bool
}

// MousePos is the cursor position in world coordinates (view plus camera).
func (w *World) MousePos() (x, y float64, held bool) {
	m, ok := w.Input.(MouseInput)
	if !ok {
		return 0, 0, false
	}
	x, y, held = m.Mouse()
	return float64(x + float64(w.CamX)), float64(y + float64(w.CamY)), held
}

func (h *scriptHost) Mouse() (float64, float64, bool) {
	if h.w.life != nil {
		return 0, 0, false
	}
	return h.w.MousePos()
}

// Clicked reports whether the button went down in this step over the object.
func (h *scriptHost) Clicked(self vm.ObjID) bool {
	if h.w.life != nil {
		return false
	}
	m, ok := h.w.Input.(MouseInput)
	if !ok || !m.MousePressed() {
		return false
	}
	e, err := h.ent(self)
	if err != nil {
		return false
	}
	mx, my, _ := h.w.MousePos()
	if e.pending || e.Dead {
		return false
	}
	// the cursor is a point: the same rule as a point touching a box (left and
	// top edges count, right and bottom do not)
	hit := box{mx, my, mx, my}.overlaps(h.w.boxOf(e))
	h.charge()
	return hit
}

// EnsureInstance implements vm.Starter: an object that has not had its first
// step yet gets its script instance (and variables) now, so that other
// scripts can use its variables.
func (h *scriptHost) EnsureInstance(id vm.ObjID) {
	e, _ := h.ent(id)
	if e == nil || e.Dead {
		return
	}
	h.w.scripts.initSpawned([]*Entity{e})
}
