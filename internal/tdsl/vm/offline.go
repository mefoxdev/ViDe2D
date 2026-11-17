package vm

import (
	"github.com/vide2d/vide-studio/internal/tdsl"
)

// MaxMissedPeriods bounds how many times a "when every" handler runs in one
// offline life tick, however long the object was away.
const MaxMissedPeriods = 10

// offlineRun is the state of a RunOffline call.
type offlineRun struct {
	scale    float64 // "per second" is multiplied by it inside "when update"; 1 elsewhere
	canWrite func(ObjID) bool
	cap      int // MaxSteps of the tick, for the message
}

// OfflineTick describes one life tick for RunOffline.
type OfflineTick struct {
	// Steps is how many simulation steps have passed since the object last
	// lived (at least 1).
	Steps int64
	// MaxSteps bounds the instructions of the whole tick (all its handlers
	// together, enforced while they run; 0 means only the frame budget).
	MaxSteps int
	// CanWrite says which other objects the script may change.
	CanWrite func(ObjID) bool
	// Mail is the messages the object got while it slept, in the order sent.
	Mail []Message
}

// RunOffline is one "life tick" of an object whose chunk is not loaded: the
// simplified step of reference.md section 8a. It runs, in this order:
//
//   - start-up (variables, "when start") if it has not happened yet;
//   - handlers that were waiting ("wait", "slide") whose time has come;
//   - every "when update" handler ONCE, with "per second" multiplied by the
//     steps so that the single run covers the whole period;
//   - every "when every" handler once per period that has elapsed (at most
//     MaxMissedPeriods), keeping the phase of the timer;
//   - the "when message" handlers for the mail.
//
// Nothing else runs (no keys, clicks, touches). While it runs, other objects'
// variables can be read but not written (CanWrite). The whole tick has a step
// budget (MaxSteps) that is enforced during the tick: a handler that runs out
// of it stops with TD401, the rest of the tick is skipped, and the object keeps
// the state it has. The tick never makes the frame report skipped runs.
func (in *Instance) RunOffline(o OfflineTick) (errs []*Error) {
	e := in.eng
	steps := max(o.Steps, 1)
	savedLeft, savedSkipped := e.frameLeft, e.skipped
	if o.MaxSteps > 0 {
		e.frameLeft = min(e.frameLeft, o.MaxSteps)
	}
	start := e.frameLeft
	e.off = &offlineRun{scale: 1, canWrite: o.CanWrite, cap: o.MaxSteps}
	defer func() {
		e.off = nil
		if r := recover(); r != nil {
			errs = append(errs, in.internalError("", r))
		}
		tooMany := e.skipped > savedSkipped
		e.frameLeft = savedLeft - (start - e.frameLeft) // the steps used come off the frame's budget
		e.skipped = savedSkipped
		if tooMany && !hasCode(errs, CodeSteps) {
			errs = append(errs, &Error{Code: CodeSteps, File: in.prog.File,
				Message: "Слишком много шагов в офлайн-тике: часть обработчиков не успела запуститься.",
				Hint:    "Объект, который живёт за кадром (Alive), должен считать мало; состояние объекта сохранено."})
		}
	}()
	errs = append(errs, in.RunPhase(PhaseInit)...)
	if !in.initDone {
		return errs // out of steps: the start-up goes on at the next life tick
	}
	errs = append(errs, in.RunPhase(PhaseStart)...)
	errs = append(errs, in.RunPhase(PhaseWake)...)
	e.off.scale = float64(steps)
	errs = append(errs, in.RunPhase(PhaseUpdate)...)
	e.off.scale = 1

	hs := in.prog.Handlers
	before := in.ticks
	in.ticks += steps
	for i := range hs {
		p := in.periods[i]
		if p <= 0 || hs[i].Event != tdsl.EventEvery {
			continue
		}
		due := in.ticks/p - before/p
		for k := int64(0); k < min(due, MaxMissedPeriods); k++ {
			if err := in.fire(i); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for i := range o.Mail {
		if e.frameLeft <= 0 {
			e.skipped++
			break
		}
		errs = append(errs, in.deliver(&o.Mail[i])...)
	}
	return errs
}

func hasCode(errs []*Error, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}
