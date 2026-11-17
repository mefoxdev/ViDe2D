// Package vm executes TrueDSL scripts: a compiler from the checked AST
// (package tdsl) to bytecode and a stack virtual machine.
//
// The package knows nothing about the game runtime or Ebitengine. It talks to
// the world only through the small Host interface.
//
// # Determinism
//
// The result of a script depends only on the script, the sequence of host
// answers (keys, properties) and the PRNG seed, never on the CPU:
//
//   - numbers are float64; every arithmetic result is wrapped in an explicit
//     float64() conversion, which forbids the Go compiler from fusing a
//     multiply and an add into one FMA instruction (arm64 does that, amd64
//     does not);
//   - only +, -, *, / and the exactly specified math.Floor, Ceil, Round, Abs,
//     Sqrt and Mod are used (all of them are correctly rounded or exact);
//     sin and cos are our own (SinDeg, CosDeg): exact reduction of the angle
//     and a Taylor polynomial of + - * only, because math.Sin and math.Cos
//     differ between platforms;
//   - random numbers come from PCG32 (see PCG), seeded by Host.Seed;
//   - the VM never reads the wall clock; time is a count of simulation steps
//     (Engine.BeginFrame counts them; "wait" and "slide" are measured in them).
//
// # Semantics decided here (the reference leaves them open)
//
//   - "x per second" is x / tickRate, where tickRate is Host.TickRate.
//   - "when every N seconds" fires every round(N * tickRate) steps (at least
//     1), counted from the step after the object started. N is evaluated once.
//   - "%" is the floored remainder: the result has the sign of the divisor
//     (-1 % 3 is 2).
//   - round() rounds halves away from zero (2.5 is 3, -2.5 is -3).
//   - Numbers are shown in text as integers when they are whole, otherwise with
//     12 significant digits (0.1 * 3 shows as 0.3).
//   - Lists are shared by reference: "var b = a" makes b the same list.
//   - "for each i in a to b" counts a, a+1, ... while i <= b; the loop
//     variable is a copy, assigning to it does not change the loop.
//   - "repeat n times" runs floor(n) times.
//
// # The world
//
// The world is reached through Host: object properties, movement, and the
// queries and actions of the stage with collisions (Find, All, Count,
// Distance, Touching, Overlapping, MoveAvoiding, Spawn, Mouse, Clicked). The
// geometry itself (boxes, touching rules, broadphase) belongs to the host;
// the VM only fixes the contract:
//
//   - a frame runs the phases of Phase in order, then Engine.Deliver hands out
//     the queued messages; the host applies spawns and destroys after that;
//   - "when touch" / "when stop touching" keep, per instance and handler, the
//     objects touched in the previous step and fire for the difference, in the
//     order the host lists them (scene-tree order); an object that is gone
//     does not produce "stop touching";
//   - every world query reports the number of boxes it looked at, and the VM
//     charges one step per four, so queries are paid for in proportion to
//     their size;
//   - messages are delivered in send order, each to every instance that has a
//     matching "when message" in the order the runtime gives; messages sent
//     during delivery are queued for the next frame; at most
//     Limits.SendsPerFrame sends per frame and Limits.Backlog waiting;
//   - "shared" variables live in the Engine, are created once by
//     RegisterProgram (first declarer wins) and are looked up by name;
//     a plain name that is not a variable of the script is read as shared
//     first, then as the object of that name (nothing if there is none);
//   - the variables of another object (Hero.hp) are read through the
//     instances the Engine knows (Engine.InstanceOf); a host that removes an
//     object must call Engine.Detach;
//   - "use" is linked at compile time (CompileSourceLibs): only the "to"
//     commands of the library are compiled into the program; errors inside
//     them quote the library file.
//
// Everything of reference.md is implemented. TD499 (CodeUnsupported) is left
// for syntax tree nodes the compiler does not know, which only a bug can cause.
//
// # Suspendable execution
//
// A handler run is a Task with its own value stack and its own heap-allocated
// call frames. TrueDSL calls never use Go recursion, so a Task can stop in the
// middle of a handler (Status Suspended) and be resumed later. "wait" and
// "slide" do so:
//
//   - the task stays in Instance.waiting (the handler is busy, so it never
//     runs inside itself); PhaseWake resumes it when Engine.tick reaches its
//     wake step. "wait N seconds" is round(N * tickRate) steps, at least 1.
//     "slide" sets the position on every step from the step count alone (a
//     run that was late catches up) and puts the object exactly on the target
//     in the last step, then the handler goes on in that same step;
//   - the step limit of a handler counts again after every wait; the frame
//     budget limits resumed runs like any other;
//   - handlers that run every frame ("when update", "when key ... held"),
//     start-up code and Instance.Call cannot suspend: TD104 at run time;
//   - limits: Limits.WaitingPerObj suspended runs per instance and
//     Limits.Waiting in the engine (TD402);
//   - Engine.Detach (the object is gone) cancels the object's waiting runs;
//     Instance.Snapshot keeps them (copied, times relative) and Restore brings
//     them back, so a chunk can be unloaded and loaded again. A restored run
//     goes on the remaining number of steps after the step in which Restore
//     is called.
//
// # Sound, scenes, memory
//
// These are the Services part of Host (PlaySound, PlayMusic, StopMusic,
// GoToScene, RestartScene, Remember, Recall). The VM only checks the types of
// the arguments (and that a remembered value is a number, text, yes/no or a
// list of them); everything else, including the errors TD304, TD305 and TD402,
// is the host's. Engine.ExportShared and ImportShared carry the "shared"
// variables from one scene to the next (objects become nothing).
package vm
