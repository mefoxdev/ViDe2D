package runtime

import "image/color"

// ChunkState is everything the world remembers about one map chunk while it
// is unloaded, keyed by the stable identity of the map node (its index path
// inside the chunk). Unloading a chunk writes it, loading the chunk reads it
// back, so a coin that was picked up stays picked up and a script variable
// keeps its value when the player walks away and returns.
//
// The state is frozen at unload and thawed at load, except for the alive
// objects of the chunk: they are simulated while it is unloaded (life.go) and
// their state is written here again just before the load applies it.
type ChunkState struct {
	// Entities holds the saved state of the entities of the chunk while it is
	// unloaded; it is emptied when the chunk loads.
	Entities map[string]*EntityState
	// Destroyed is the set of destroyed nodes. They are never created again.
	Destroyed map[string]bool
}

func newChunkState() ChunkState {
	return ChunkState{Entities: map[string]*EntityState{}, Destroyed: map[string]bool{}}
}

// EntityState is the saved state of one entity.
type EntityState struct {
	X, Y, PrevX, PrevY float64
	W, H               float64
	Visible, Flip      bool
	Angle, Layer       float64
	Color              color.RGBA
	Texture, Text      string
	FontSize           int
	Tags               []string
	// Behavior is what a Stateful behavior returned from SnapshotState (for a
	// script: its variables and timers); nil if it has none.
	Behavior any
}

func snapshotEntity(e *Entity) *EntityState {
	st := &EntityState{
		X: e.X, Y: e.Y, PrevX: e.PrevX, PrevY: e.PrevY, W: e.W, H: e.H, Visible: e.Visible, Flip: e.Flip,
		Angle: e.Angle, Layer: e.Layer, Color: e.Color, Texture: e.Texture, Text: e.Text, FontSize: e.FontSize,
		Tags: append([]string(nil), e.Tags...),
	}
	if b, ok := e.Behavior.(Stateful); ok {
		st.Behavior = b.SnapshotState()
	}
	return st
}

func restoreEntity(e *Entity, st *EntityState) {
	e.X, e.Y, e.PrevX, e.PrevY, e.W, e.H = st.X, st.Y, st.PrevX, st.PrevY, st.W, st.H
	e.Visible, e.Flip, e.Angle, e.Layer, e.Color = st.Visible, st.Flip, st.Angle, st.Layer, st.Color
	e.Texture, e.Text, e.FontSize = st.Texture, st.Text, st.FontSize
	e.Tags = append([]string(nil), st.Tags...)
	e.fresh = false // a continuation, not a new object
	if b, ok := e.Behavior.(Stateful); ok && st.Behavior != nil {
		b.RestoreState(st.Behavior)
	}
}

// save stores the state of ents (the entities of the chunk being unloaded).
func (s *ChunkState) save(ents []*Entity) {
	for _, e := range ents {
		if !e.Dead && e.key != "" {
			s.Entities[e.key] = snapshotEntity(e)
		}
	}
}

// restore applies saved state to freshly created entities, then forgets it.
func (s *ChunkState) restore(ents []*Entity) {
	for _, e := range ents {
		if st := s.Entities[e.key]; st != nil {
			restoreEntity(e, st)
		}
	}
	clear(s.Entities)
}
