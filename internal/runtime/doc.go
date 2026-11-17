// Package runtime is the ViDe2D game runtime core: it loads a project, builds
// the world of entities, and advances it with a fixed-timestep simulation.
//
// Everything except Game (game.go) is headless: no window, no wall clock, no
// GPU. The simulation advances only by whole steps (World.Step); the clock that
// decides how many steps to run is a separate, pure accumulator (Clock), so
// tests drive both with fake time.
//
// Game code is untrusted. All files are read through checked project-relative
// paths: no "..", no absolute paths, no symbolic links, size limits.
package runtime
