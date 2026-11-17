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
//     there is no sin or cos, because their results differ between platforms;
//   - random numbers come from PCG32 (see PCG), seeded by Host.Seed;
//   - the VM never reads the wall clock; time is a count of simulation steps.
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
// # Suspendable execution
//
// A handler run is a Task with its own value stack and its own heap-allocated
// call frames. TrueDSL calls never use Go recursion, so a Task can stop in the
// middle of a handler (Status Suspended) and be resumed later; "wait" will use
// this in the next stage.
package vm
