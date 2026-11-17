package vm

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"

	"github.com/vide2d/vide-studio/internal/tdsl"
)

// Phase is one part of the frame order of reference.md (section 8):
// input, update, timers, touches (messages follow, see Engine.Deliver).
// PhaseInit and PhaseStart come before them for objects that have just
// appeared: first every object sets up its variables, then the "when start"
// handlers run, so a start handler can read the variables of any object.
type Phase uint8

// Phases of a frame, in order.
const (
	PhaseInit Phase = iota
	PhaseStart
	PhaseInput
	PhaseUpdate
	PhaseTimers
	PhaseTouches
)

// Engine is the state shared by all scripts of one game: the host, the
// limits, the random generator and the step budget of the current frame.
type Engine struct {
	Host   Host
	Limits Limits

	rng       *PCG
	tickRate  float64
	frameLeft int
	skipped   int
	prints    int
	free      []*Task

	insts  map[ObjID]*Instance // live instances, for access to the variables of other objects
	shared map[string]Value    // "shared" variables of the whole game
	queue  []Message           // sent, not yet delivered
	sent   int                 // "send" commands in this frame
	pairs  int                 // (message, object) pairs looked at, for the budget
}

// Message is a "send" waiting for delivery.
type Message struct {
	Text  string
	Value Value
	To    ObjID
	HasTo bool

	// broadcast progress (see Deliver)
	started bool
	next    int   // index of the next recipient
	after   ObjID // the recipient served last
}

// NewEngine creates an engine. The tick rate and the random seed are read
// from the host once, here.
func NewEngine(h Host) *Engine {
	e := &Engine{Host: h, Limits: DefaultLimits(), rng: NewPCG(h.Seed()), tickRate: float64(h.TickRate()),
		insts: map[ObjID]*Instance{}, shared: map[string]Value{}}
	e.frameLeft = e.Limits.FrameSteps
	return e
}

// BeginFrame restores the step budget of the frame. Call it once per
// simulation step, before running any phase.
func (e *Engine) BeginFrame() {
	e.frameLeft = e.Limits.FrameSteps
	e.skipped = 0
	e.prints = 0
	e.sent = 0
	e.pairs = 0
}

// Charge takes n steps off the budget of the frame (for work the VM did not
// count itself).
func (e *Engine) Charge(n int) { e.frameLeft -= n }

// Pending is the number of messages waiting for delivery.
func (e *Engine) Pending() int { return len(e.queue) }

// Shared returns a "shared" variable (for tests and tools).
func (e *Engine) Shared(name string) (Value, bool) {
	v, ok := e.shared[name]
	return v, ok
}

// Detach forgets the instance of an object that left the world.
func (e *Engine) Detach(id ObjID) { delete(e.insts, id) }

// InstanceOf returns the live instance of an object, or nil.
func (e *Engine) InstanceOf(id ObjID) *Instance { return e.insts[id] }

// RegisterProgram declares the "shared" variables of a script: each is created
// once, with the value of its initializer, by the first script that declares
// it. Run it when the script is compiled, before any object runs. It has its
// own step budget and never fails: a failing initializer leaves nothing.
func (e *Engine) RegisterProgram(p *Program) (errs []*Error) {
	if len(p.Shared) == 0 {
		return nil
	}
	saved := e.frameLeft
	defer func() { e.frameLeft = saved }()
	for _, d := range p.Shared {
		if _, dup := e.shared[d.Name]; dup {
			continue
		}
		e.frameLeft = e.Limits.HandlerSteps
		in := &Instance{eng: e, prog: p, globals: make([]Value, len(p.Globals))}
		t := e.newTask(in, d.Init, "")
		_, err := t.Run()
		v := t.result
		e.release(t)
		if err != nil {
			errs = append(errs, err)
			v = Value{}
		}
		e.shared[d.Name] = v
	}
	return errs
}

// Deliver hands the queued messages to the objects (the instances in order,
// which must be in scene-tree order and only those that have "when message"
// handlers), in the order they were sent. Messages sent by the handlers that
// run now wait for the next frame. When the frame's step budget runs out the
// undelivered messages stay queued, in order.
func (e *Engine) Deliver(order []*Instance) (errs []*Error) {
	batch := e.queue
	e.queue = nil
	for k := range batch {
		if e.frameLeft <= 0 {
			e.requeue(batch[k:])
			e.skipped++
			return errs
		}
		m := &batch[k]
		if m.HasTo {
			if in := e.instanceOf(m.To); in != nil && e.Host.Exists(m.To) && in.hasMessages() {
				errs = append(errs, in.deliver(m)...)
			}
			continue
		}
		start := m.resume(order)
		for i := start; i < len(order); i++ {
			if e.pairs++; e.pairs&3 == 0 {
				e.frameLeft--
			}
			if e.frameLeft <= 0 {
				// out of steps in the middle of a broadcast: the message waits,
				// to go on from this recipient in the next frame
				m.next, m.after, m.started = i, 0, true
				if i > 0 {
					m.after = order[i-1].self
				}
				e.requeue(batch[k:])
				e.skipped++
				return errs
			}
			errs = append(errs, order[i].deliver(m)...)
		}
	}
	return errs
}

// requeue puts undelivered messages back in front of the ones sent meanwhile.
func (e *Engine) requeue(rest []Message) {
	e.queue = append(rest[:len(rest):len(rest)], e.queue...)
}

// resume is the index in order of the first recipient that has not yet got
// the message: 0 for a new one. The recipients are found again by the object
// that was served last, so objects that came or went in between do not make
// the message skip or repeat anyone.
func (m *Message) resume(order []*Instance) int {
	if !m.started {
		return 0
	}
	if m.after == 0 && m.next == 0 {
		return 0
	}
	if m.next > 0 && m.next <= len(order) && order[m.next-1].self == m.after {
		return m.next
	}
	for i, in := range order {
		if in.self == m.after {
			return i + 1
		}
	}
	return min(m.next, len(order))
}

// Skipped is the number of handler runs that were not started in this frame
// because the frame's step budget was already used up.
func (e *Engine) Skipped() int { return e.skipped }

// StepsLeft is what remains of the frame's step budget.
func (e *Engine) StepsLeft() int { return e.frameLeft }

// Instance is a script running on one object: the program plus the object's
// own variables and timers.
type Instance struct {
	eng     *Engine
	prog    *Program
	self    ObjID
	globals []Value
	periods []int64 // for "every" handlers: steps between runs, 0 if none
	busy    []bool  // a handler never runs inside itself
	waiting []*Task // suspended runs (used by "wait" in a later stage)
	ticks   int64
	touch   [][]ObjID // for touch handlers: the objects touched at the previous step
	touchB  []ObjID

	// Start-up is a small state machine that survives frames whose step
	// budget is empty: nothing is lost, it simply continues next frame.
	initVar   int // next variable initializer to run
	initPer   int // next handler whose period to compute
	initDone  bool
	startIdx  int // next handler to look at for "when start"
	startDone bool
}

// NewInstance attaches program p to object self.
func (e *Engine) NewInstance(p *Program, self ObjID) *Instance {
	in := &Instance{
		eng: e, prog: p, self: self, globals: make([]Value, len(p.Globals)),
		periods: make([]int64, len(p.Handlers)), busy: make([]bool, len(p.Handlers)),
		waiting: make([]*Task, len(p.Handlers)), touch: make([][]ObjID, len(p.Handlers)),
	}
	e.insts[self] = in
	return in
}

// hasMessages reports whether the script has a "when message" handler.
func (in *Instance) hasMessages() bool {
	for i := range in.prog.Handlers {
		if h := &in.prog.Handlers[i]; h.Supported && h.Event == tdsl.EventMessage {
			return true
		}
	}
	return false
}

// HasMessages reports whether the script has a "when message" handler.
func (in *Instance) HasMessages() bool { return in.hasMessages() }

// Self is the object the instance belongs to.
func (in *Instance) Self() ObjID { return in.self }

// Initialize runs the variable initializers now if they have not run yet. The
// runtime calls it for a freshly spawned object so that its variables can be
// read at once.
func (in *Instance) Initialize() (errs []*Error) {
	defer func() {
		if r := recover(); r != nil {
			errs = append(errs, in.internalError("", r))
		}
	}()
	if !in.initDone {
		errs = in.initialize()
	}
	return errs
}

// Program returns the compiled script.
func (in *Instance) Program() *Program { return in.prog }

// Global returns the value of an object variable (for tests and tools).
func (in *Instance) Global(name string) (Value, bool) {
	for i, n := range in.prog.Globals {
		if n == name {
			return in.globals[i], true
		}
	}
	return Value{}, false
}

// InstanceState is a copy of everything an Instance remembers: its variables,
// timers and start-up progress. The runtime keeps it while the object's chunk
// is unloaded and gives it back on reload.
type InstanceState struct {
	prog      *Program
	globals   []Value
	periods   []int64
	ticks     int64
	initVar   int
	initPer   int
	initDone  bool
	startIdx  int
	startDone bool
}

// Snapshot copies the state of the instance. Lists are copied deeply (shared
// sub-lists stay shared inside the copy).
func (in *Instance) Snapshot() *InstanceState {
	memo := map[*List]*List{}
	st := &InstanceState{
		prog: in.prog, globals: make([]Value, len(in.globals)), periods: append([]int64(nil), in.periods...),
		ticks: in.ticks, initVar: in.initVar, initPer: in.initPer, initDone: in.initDone,
		startIdx: in.startIdx, startDone: in.startDone,
	}
	for i, v := range in.globals {
		st.globals[i] = copyValue(v, memo)
	}
	return st
}

// Restore puts a snapshot back. It reports false (and changes nothing) if the
// snapshot belongs to another program.
func (in *Instance) Restore(st *InstanceState) bool {
	if st == nil || st.prog != in.prog {
		return false
	}
	memo := map[*List]*List{}
	for i, v := range st.globals {
		in.globals[i] = copyValue(v, memo)
	}
	copy(in.periods, st.periods)
	in.ticks, in.initVar, in.initPer, in.initDone = st.ticks, st.initVar, st.initPer, st.initDone
	in.startIdx, in.startDone = st.startIdx, st.startDone
	return true
}

func copyValue(v Value, memo map[*List]*List) Value {
	if v.K != KList {
		return v
	}
	if c, ok := memo[v.L]; ok {
		return ListValue(c)
	}
	c := &List{Items: make([]Value, len(v.L.Items))}
	memo[v.L] = c
	for i, it := range v.L.Items {
		c.Items[i] = copyValue(it, memo)
	}
	return ListValue(c)
}

// RunPhase runs the handlers of one phase of the frame, in file order, and
// returns the errors of the runs that failed. A failed run stops only itself;
// a panic inside the VM is turned into an error too.
func (in *Instance) RunPhase(ph Phase) (errs []*Error) {
	defer func() {
		if r := recover(); r != nil {
			errs = append(errs, in.internalError("", r))
		}
	}()
	add := func(e *Error) {
		if e != nil {
			errs = append(errs, e)
		}
	}
	hs := in.prog.Handlers
	switch ph {
	case PhaseInit:
		if !in.initDone {
			errs = append(errs, in.initialize()...)
		}
	case PhaseStart:
		if !in.initDone {
			errs = append(errs, in.initialize()...)
			if !in.initDone {
				return errs // out of steps this frame: go on next frame
			}
		}
		for !in.startDone {
			if in.startIdx >= len(hs) {
				in.startDone = true
				break
			}
			i := in.startIdx
			if hs[i].Supported && hs[i].Event == tdsl.EventStart {
				if in.eng.frameLeft <= 0 {
					return errs // this and the rest of the start handlers wait for the next frame
				}
				in.startIdx++
				add(in.fire(i))
				continue
			}
			in.startIdx++
		}
	case PhaseInput:
		for i := range hs {
			if !hs[i].Supported {
				continue
			}
			switch hs[i].Event {
			case tdsl.EventKey:
				if in.eng.Host.Key(hs[i].Key, hs[i].State) {
					add(in.fire(i))
				}
			case tdsl.EventClick:
				if in.eng.Host.Clicked(in.self) {
					add(in.fire(i))
				}
			}
		}
	case PhaseUpdate:
		for i := range hs {
			if hs[i].Supported && hs[i].Event == tdsl.EventUpdate {
				add(in.fire(i))
			}
		}
	case PhaseTimers:
		in.ticks++
		for i := range hs {
			if p := in.periods[i]; p > 0 && in.ticks%p == 0 {
				add(in.fire(i))
			}
		}
	case PhaseTouches:
		for i := range hs {
			if hs[i].Supported && (hs[i].Event == tdsl.EventTouch || hs[i].Event == tdsl.EventStopTouching) {
				errs = append(errs, in.runTouch(i)...)
			}
		}
	}
	return errs
}

// handlerArg computes the argument of a touch or message handler.
func (in *Instance) handlerArg(i int) (Value, *Error) {
	h := &in.prog.Handlers[i]
	if h.argConst {
		return Text(h.argText), nil
	}
	if h.arg == nil {
		return Value{}, nil
	}
	e := in.eng
	t := e.newTask(in, h.arg, h.Desc)
	_, err := t.Run()
	v := t.result
	e.release(t)
	return v, err
}

// runTouch finds the objects that start and stop touching this one and fires
// the handler i for each of them, in scene-tree order.
func (in *Instance) runTouch(i int) (errs []*Error) {
	e := in.eng
	h := &in.prog.Handlers[i]
	if e.frameLeft <= 0 {
		e.skipped++
		return nil
	}
	tv, err := in.handlerArg(i)
	if err != nil {
		return []*Error{err}
	}
	var tgt Target
	switch tv.K {
	case KText:
		tgt.Tag = tv.S
	case KObj:
		tgt.Obj, tgt.HasObj = tv.Obj(), true
	case KNothing:
	default:
		t := &Task{in: in, desc: h.Desc}
		return []*Error{t.errAt(h.fn.Spans[0], CodeWrongType, "После «touch»: ожидается метка в кавычках или объект, а тут "+kindNom[tv.K]+".", "")}
	}
	var cur []ObjID
	e.frameLeft--
	if tv.K != KNothing {
		ids, work, herr := e.Host.Overlapping(in.self, tgt, in.touchB[:0])
		e.frameLeft -= work >> 2
		if herr != nil {
			t := &Task{in: in, desc: h.Desc}
			f := hostFailure(herr)
			return []*Error{t.errAt(h.fn.Spans[0], f.code, f.msg, f.hint)}
		}
		cur = ids
	}
	prev := in.touch[i]
	in.touch[i] = cur
	in.touchB = prev[:0]
	var fireIDs []ObjID
	if h.Event == tdsl.EventTouch {
		fireIDs = diffIDs(cur, prev, nil)
	} else {
		fireIDs = diffIDs(prev, cur, e.Host.Exists)
	}
	for _, id := range fireIDs {
		if e.frameLeft <= 0 {
			e.skipped++
			break
		}
		if err := in.fireWith(i, Obj(id), Value{}); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// diffIDs returns the elements of a that are not in b, keeping the order of a;
// alive (if not nil) filters out the ones that no longer exist.
func diffIDs(a, b []ObjID, alive func(ObjID) bool) []ObjID {
	if len(a) == 0 {
		return nil
	}
	var set map[ObjID]struct{}
	if len(b) > 8 {
		set = make(map[ObjID]struct{}, len(b))
		for _, x := range b {
			set[x] = struct{}{}
		}
	}
	var out []ObjID
	for _, x := range a {
		found := false
		if set != nil {
			_, found = set[x]
		} else {
			for _, y := range b {
				if x == y {
					found = true
					break
				}
			}
		}
		if !found && (alive == nil || alive(x)) {
			out = append(out, x)
		}
	}
	return out
}

// deliver runs the "when message" handlers that match m.
func (in *Instance) deliver(m *Message) (errs []*Error) {
	defer func() {
		if r := recover(); r != nil {
			errs = append(errs, in.internalError("", r))
		}
	}()
	hs := in.prog.Handlers
	for i := range hs {
		if !hs[i].Supported || hs[i].Event != tdsl.EventMessage {
			continue
		}
		v, err := in.handlerArg(i)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if v.K == KText && v.S == m.Text {
			if err := in.fireWith(i, Value{}, m.Value); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

// initialize runs the variable initializers (each on its own: an error leaves
// that variable at nothing and the others are still set) and computes the
// timer periods. If the frame runs out of steps it stops and the next call
// continues; initDone tells whether it finished.
func (in *Instance) initialize() (errs []*Error) {
	e := in.eng
	for in.initVar < len(in.prog.Inits) {
		fn := in.prog.Inits[in.initVar]
		if fn != nil {
			if e.frameLeft <= 0 {
				return errs
			}
			t := e.newTask(in, fn, "")
			_, err := t.Run()
			e.release(t)
			if err != nil {
				if err.Code == CodeSteps && e.frameLeft <= 0 {
					return errs // cut by the frame budget, not by the script: retry next frame
				}
				errs = append(errs, err)
			}
		}
		in.initVar++
	}
	for in.initPer < len(in.prog.Handlers) {
		i := in.initPer
		h := &in.prog.Handlers[i]
		if h.Event == tdsl.EventEvery && h.period != nil {
			if e.frameLeft <= 0 {
				return errs
			}
			t := e.newTask(in, h.period, h.Desc)
			_, err := t.Run()
			v := t.result
			e.release(t)
			if err != nil && err.Code == CodeSteps && e.frameLeft <= 0 {
				return errs
			}
			if err == nil {
				err = in.setPeriod(i, v)
			}
			if err != nil {
				errs = append(errs, err)
			}
		}
		in.initPer++
	}
	in.initDone = true
	return errs
}

func (in *Instance) setPeriod(i int, v Value) *Error {
	h := &in.prog.Handlers[i]
	if v.K != KNum || !(v.N > 0) || v.N > 1e9 {
		sp := h.fn.Spans[0]
		if len(h.period.Spans) > 0 {
			sp = h.period.Spans[0]
		}
		t := &Task{in: in, desc: h.Desc}
		return t.errAt(sp, CodeWrongType, "Время в «when every»: ожидается положительное число секунд.",
			"Например: when every 2 seconds. Этот таймер выключен.")
	}
	ticks := math.Floor(float64(float64(v.N*in.eng.tickRate) + 0.5))
	in.periods[i] = int64(max(ticks, 1))
	return nil
}

// internalError turns a recovered panic into an Error. A panic is a bug of the
// engine, never of the script, but it must not take the game down.
func (in *Instance) internalError(desc string, r any) *Error {
	e := &Error{Code: CodeInternal, File: in.prog.File, Handler: desc,
		Message: fmt.Sprintf("Внутренняя ошибка движка: %v.", r),
		Hint:    "Это ошибка самого движка, а не вашего скрипта. Сообщите о ней разработчикам."}
	return e
}

// fire runs handler i to completion (or until it suspends).
func (in *Instance) fire(i int) *Error { return in.fireWith(i, Value{}, Value{}) }

// fireWith is fire for a handler that has "other" (touch) or "value" (message).
func (in *Instance) fireWith(i int, other, value Value) (err *Error) {
	if in.busy[i] {
		return nil // a handler is never run inside itself
	}
	e := in.eng
	if e.frameLeft <= 0 {
		e.skipped++
		return nil
	}
	h := &in.prog.Handlers[i]
	in.busy[i] = true
	defer func() {
		if r := recover(); r != nil {
			in.busy[i] = false
			err = in.internalError(h.Desc, r)
		}
	}()
	t := e.newTask(in, h.fn, h.Desc)
	t.other, t.value = other, value
	st, err := t.Run()
	if st == StatusSuspended {
		in.waiting[i] = t // woken by the scheduler of "wait" in a later stage
		return err
	}
	in.busy[i] = false
	e.release(t)
	return err
}

// Call runs the "to" command name with the arguments, as if called from a
// handler, and returns its result. It is meant for tests and tools.
func (in *Instance) Call(name string, args ...Value) (Value, *Error) {
	for _, f := range in.prog.Funcs {
		if f.Name == name {
			t := in.eng.newTask(in, f, "")
			t.stack = append(t.stack[:0], args...)
			for len(t.stack) < f.NLocals {
				t.stack = append(t.stack, Value{})
			}
			_, err := t.Run()
			v := t.result
			in.eng.release(t)
			return v, err
		}
	}
	return Value{}, &Error{Code: CodeUnsupported, File: in.prog.File, Message: "Нет команды «" + name + "»."}
}

// ---- tasks ----

// Status is the outcome of Task.Run.
type Status uint8

// Statuses.
const (
	StatusDone      Status = iota // the run finished (successfully or with an error)
	StatusSuspended               // the run stopped to wait; Run resumes it
)

type frame struct {
	fn   *Func
	pc   int
	base int
}

// Task is one run of a handler. All its state (value stack and call frames)
// lives on the heap, never in Go's stack, so a run can be suspended and
// resumed.
type Task struct {
	eng    *Engine
	in     *Instance
	desc   string
	stack  []Value
	frames []frame
	steps  int // instructions executed by this run so far
	left   int // instructions allowed in the current Run call
	limit  int
	result Value
	other  Value // the second object of a touch handler
	value  Value // what came with a message
}

func (e *Engine) newTask(in *Instance, fn *Func, desc string) *Task {
	var t *Task
	if n := len(e.free); n > 0 {
		t = e.free[n-1]
		e.free = e.free[:n-1]
	} else {
		t = &Task{stack: make([]Value, 0, 64), frames: make([]frame, 0, 8)}
	}
	t.eng, t.in, t.desc = e, in, desc
	t.stack, t.frames, t.steps, t.result = t.stack[:0], t.frames[:0], 0, Value{}
	t.other, t.value = Value{}, Value{}
	t.frames = append(t.frames, frame{fn: fn})
	for i := 0; i < fn.NLocals; i++ {
		t.stack = append(t.stack, Value{})
	}
	return t
}

func (e *Engine) release(t *Task) {
	clear(t.stack)
	t.in = nil
	if len(e.free) < 16 {
		e.free = append(e.free, t)
	}
}

// Run executes the task until it finishes, fails or suspends. A panic inside
// the interpreter is caught and returned as a TD405 error.
func (t *Task) Run() (st Status, err *Error) {
	e := t.eng
	limit := max(min(e.Limits.HandlerSteps-t.steps, e.frameLeft), 0)
	t.left, t.limit = limit, limit
	defer func() {
		if r := recover(); r != nil {
			err = t.in.internalError(t.desc, r)
			if len(t.frames) > 0 {
				if fn := t.frames[0].fn; len(fn.Spans) > 0 {
					sp := fn.Spans[0]
					err.Line, err.Col, err.EndCol = int(sp.Line), int(sp.Col), int(sp.EndCol)
				}
			}
			st = StatusDone
		}
		used := t.limit - max(t.left, 0)
		t.steps += used
		e.frameLeft -= used
		if err != nil && err.Code == CodeSteps {
			if e.frameLeft <= 0 {
				err.Message = fmt.Sprintf("Слишком много шагов за кадр: вся игра вместе делает не больше %d шагов в кадре.", e.Limits.FrameSteps)
				err.Hint = "Возможно, где-то бесконечный цикл. Проверьте «while» и «repeat»."
			} else {
				err.Message = "Слишком много шагов: возможно, бесконечный цикл."
				err.Hint = fmt.Sprintf("Один запуск обработчика делает не больше %d шагов. Проверьте «while» и «repeat».", e.Limits.HandlerSteps)
			}
		}
	}()
	return StatusDone, t.exec()
}

// cost charges n extra units of work for an operation that touches many
// items: one step per 16 items, so a list or text operation is paid for in
// proportion to its size and cannot hide a huge loop inside one instruction.
func (t *Task) cost(n int) { t.left -= n >> 4 }

// work charges n examined boxes of a world query: one step per four.
func (t *Task) work(n int) { t.left -= n >> 2 }

func (t *Task) push(v Value) { t.stack = append(t.stack, v) }

func (t *Task) pop() Value {
	n := len(t.stack) - 1
	v := t.stack[n]
	t.stack[n] = Value{}
	t.stack = t.stack[:n]
	return v
}

// errAt builds an Error at a source place.
func (t *Task) errAt(sp Span, code, msg, hint string) *Error {
	e := &Error{Code: code, Line: int(sp.Line), Col: int(sp.Col), EndCol: int(sp.EndCol), Message: msg, Hint: hint, Handler: t.desc}
	if t.in != nil {
		p := t.in.prog
		e.File = p.File
		if l := int(sp.Line) - 1; l >= 0 && l < len(p.lines) {
			e.SrcLine = strings.TrimSuffix(p.lines[l], "\r")
		}
	}
	return e
}

func fl(code, msg, hint string) *failure { return &failure{code, msg, hint} }

func (t *Task) failAt(fn *Func, pc int, f *failure) *Error {
	var sp Span
	if pc >= 0 && pc < len(fn.Spans) {
		sp = fn.Spans[pc]
	}
	e := t.errAt(sp, f.code, f.msg, f.hint)
	if fn.File != "" {
		e.File, e.SrcLine = fn.File, ""
		if l := int(sp.Line) - 1; l >= 0 && l < len(fn.lines) {
			e.SrcLine = strings.TrimSuffix(fn.lines[l], "\r")
		}
	}
	return e
}

func wrongKind(ctx string, want Kind, got Value) *failure {
	hint := ""
	switch {
	case want == KNum && got.K == KText:
		hint = "Числа пишут без кавычек. Текст в число превращает «number(...)»."
	case want == KText && got.K == KNum:
		hint = "Чтобы получить текст из числа, вставьте его в кавычки: «\"{...}\"»."
	}
	return fl(CodeWrongType, ctx+": ожидается "+kindAcc[want]+", а тут "+kindNom[got.K]+".", hint)
}

func hostFailure(err error) *failure {
	var he *HostError
	if errors.As(err, &he) {
		return fl(he.Code, he.Message, he.Hint)
	}
	if err == ErrGone {
		return fl(CodeGone, "Тут пусто или объект уже удалён.", "Перед обращением проверяйте «exists(...)».")
	}
	return fl(CodeWrongType, err.Error(), "")
}

// exec is the interpreter loop.
func (t *Task) exec() *Error {
	e := t.eng
	in := t.in
	prog := in.prog
	top := t.frames[len(t.frames)-1]
	fn, pc, base := top.fn, top.pc, top.base
	code := fn.Code
	for {
		if t.left <= 0 {
			return t.failAt(fn, min(pc, len(code)-1), fl(CodeSteps, "", ""))
		}
		t.left--
		ins := code[pc]
		pc++
		switch ins.Op {
		case OpNothing:
			t.push(Value{})
		case OpTrue:
			t.push(Bool(true))
		case OpFalse:
			t.push(Bool(false))
		case OpConst:
			t.push(prog.Consts[ins.A])
		case OpDir:
			t.push(DirValue(Dir(ins.A)))
		case OpSelf:
			t.push(Obj(in.self))
		case OpLoadLocal:
			t.push(t.stack[base+int(ins.A)])
		case OpStoreLocal:
			t.stack[base+int(ins.A)] = t.pop()
		case OpLoadGlobal:
			t.push(in.globals[ins.A])
		case OpStoreGlobal:
			in.globals[ins.A] = t.pop()
		case OpPop:
			t.pop()
		case OpDup:
			t.push(t.stack[len(t.stack)-1])
		case OpDup2:
			n := len(t.stack)
			a, b := t.stack[n-2], t.stack[n-1]
			t.push(a)
			t.push(b)

		case OpAdd, OpSub, OpMul, OpDiv, OpMod:
			b, a := t.pop(), t.pop()
			v, f := t.arith(ins.Op, a, b)
			if f != nil {
				return t.failAt(fn, pc-1, f)
			}
			t.push(v)
		case OpNeg:
			a := t.pop()
			if a.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("После минуса", KNum, a))
			}
			t.push(Num(float64(-a.N)))
		case OpPerSecond:
			a := t.pop()
			if a.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("Перед «per second»", KNum, a))
			}
			t.push(Num(float64(a.N / e.tickRate)))
		case OpNot:
			a := t.pop()
			if a.K != KBool {
				return t.failAt(fn, pc-1, condFailure(a))
			}
			t.push(Bool(!a.IsTrue()))
		case OpEq, OpNe, OpLt, OpGt, OpLe, OpGe, OpIn, OpNotIn:
			b, a := t.pop(), t.pop()
			v, f := t.compare(ins.Op, a, b)
			if f != nil {
				return t.failAt(fn, pc-1, f)
			}
			t.push(v)

		case OpJump:
			pc = int(ins.A)
		case OpJumpIfFalse:
			a := t.pop()
			if a.K != KBool {
				return t.failAt(fn, pc-1, condFailure(a))
			}
			if !a.IsTrue() {
				pc = int(ins.A)
			}
		case OpAndJump, OpOrJump:
			a := t.stack[len(t.stack)-1]
			if a.K != KBool {
				return t.failAt(fn, pc-1, condFailure(a))
			}
			if a.IsTrue() == (ins.Op == OpOrJump) {
				pc = int(ins.A)
			} else {
				t.pop()
			}
		case OpCheckBool:
			if a := t.stack[len(t.stack)-1]; a.K != KBool {
				return t.failAt(fn, pc-1, condFailure(a))
			}
		case OpCheckNum:
			if a := t.stack[len(t.stack)-1]; a.K != KNum {
				return t.failAt(fn, pc-1, wrongKind(prog.Consts[ins.A].S, KNum, a))
			}
		case OpCheckList:
			if a := t.stack[len(t.stack)-1]; a.K != KList {
				return t.failAt(fn, pc-1, wrongKind(prog.Consts[ins.A].S, KList, a))
			}

		case OpList:
			n := int(ins.A)
			if n > e.Limits.ListItems {
				return t.failAt(fn, pc-1, tooBig("список", e.Limits.ListItems))
			}
			t.cost(n)
			items := make([]Value, n)
			copy(items, t.stack[len(t.stack)-n:])
			clear(t.stack[len(t.stack)-n:])
			t.stack = t.stack[:len(t.stack)-n]
			t.push(ListValue(&List{Items: items}))
		case OpText:
			n := int(ins.A)
			parts := t.stack[len(t.stack)-n:]
			var b strings.Builder
			limit := 4 * e.Limits.TextChars
			ok := true
			for _, p := range parts {
				s, fit := p.show(false, 0, limit-b.Len())
				if !fit || b.Len()+len(s) > limit {
					ok = false
					break
				}
				b.WriteString(s)
			}
			t.cost(b.Len())
			clear(parts)
			t.stack = t.stack[:len(t.stack)-n]
			if !ok || textTooLong(b.String(), e.Limits.TextChars) {
				return t.failAt(fn, pc-1, tooBig("текст", e.Limits.TextChars))
			}
			t.push(Text(b.String()))
		case OpIndex:
			i, l := t.pop(), t.pop()
			v, f := t.indexGet(l, i, ins.A)
			if f != nil {
				return t.failAt(fn, pc-1, f)
			}
			t.push(v)
		case OpSetIndex:
			v, i, l := t.pop(), t.pop(), t.pop()
			if f := t.indexSet(l, i, v, ins.A); f != nil {
				return t.failAt(fn, pc-1, f)
			}
		case OpListLen:
			l := t.pop()
			t.push(Num(float64(len(l.L.Items))))
		case OpMember:
			v := t.pop()
			r, f := t.member(v, prog.Consts[ins.A].S, Prop(ins.B))
			if f != nil {
				return t.failAt(fn, pc-1, f)
			}
			t.push(r)
		case OpSetMember:
			v, o := t.pop(), t.pop()
			if o.K == KObj {
				if Prop(ins.B) == PropNone {
					if f := t.setObjVar(o.Obj(), prog.Consts[ins.A].S, v); f != nil {
						return t.failAt(fn, pc-1, f)
					}
				} else if f := t.setProp(o, Prop(ins.B), v); f != nil {
					return t.failAt(fn, pc-1, f)
				}
			} else if o.K == KNothing {
				return t.failAt(fn, pc-1, fl(CodeGone, "Тут пусто или объект уже удалён.", "Перед обращением проверяйте «exists(...)»."))
			} else {
				return t.failAt(fn, pc-1, fl(CodeWrongType, "Менять свойства можно только у объекта, а тут "+kindNom[o.K]+".", ""))
			}
		case OpGetProp:
			o := t.pop()
			v, f := t.getProp(o, Prop(ins.A))
			if f != nil {
				return t.failAt(fn, pc-1, f)
			}
			t.push(v)
		case OpSetProp:
			v, o := t.pop(), t.pop()
			if f := t.setProp(o, Prop(ins.A), v); f != nil {
				return t.failAt(fn, pc-1, f)
			}
		case OpKey:
			t.push(Bool(e.Host.Key(prog.Consts[ins.A].S, tdsl.KeyHeld)))

		case OpCall:
			if len(t.frames)-1 >= e.Limits.CallDepth {
				callee := prog.Funcs[ins.A]
				return t.failAt(fn, pc-1, fl(CodeDeep, "Слишком глубокие вызовы: команда «"+callee.Name+"» вызывает сама себя без остановки.",
					fmt.Sprintf("Вложенных вызовов не больше %d. Добавьте условие, при котором команда перестаёт вызывать себя.", e.Limits.CallDepth)))
			}
			callee := prog.Funcs[ins.A]
			t.frames[len(t.frames)-1].pc = pc
			nb := len(t.stack) - int(ins.B)
			for i := int(ins.B); i < callee.NLocals; i++ {
				t.push(Value{})
			}
			t.frames = append(t.frames, frame{fn: callee, base: nb})
			fn, pc, base, code = callee, 0, nb, callee.Code
		case OpReturn:
			res := t.pop()
			clear(t.stack[base:])
			t.stack = t.stack[:base]
			t.frames = t.frames[:len(t.frames)-1]
			if len(t.frames) == 0 {
				t.result = res
				return nil
			}
			t.push(res)
			top := t.frames[len(t.frames)-1]
			fn, pc, base, code = top.fn, top.pc, top.base, top.fn.Code
		case OpBuiltin:
			if f := t.builtin(int(ins.A), int(ins.B)); f != nil {
				return t.failAt(fn, pc-1, f)
			}

		case OpMove:
			by, d := t.pop(), t.pop()
			if d.K != KDir {
				return t.failAt(fn, pc-1, wrongKind("Направление в «move»", KDir, d))
			}
			if by.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("Шаг в «move … by»", KNum, by))
			}
			if f := rangeFailure("Шаг в «move … by»", by.N); f != nil {
				return t.failAt(fn, pc-1, f)
			}
			dx, dy := d.Dir().Delta(by.N)
			if err := e.Host.MoveBy(in.self, dx, dy); err != nil {
				return t.failAt(fn, pc-1, hostFailure(err))
			}
		case OpMoveTo:
			y, x := t.pop(), t.pop()
			if x.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("Координата x в «move to»", KNum, x))
			}
			if y.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("Координата y в «move to»", KNum, y))
			}
			if f := rangeFailure("Координата x в «move to»", x.N); f != nil {
				return t.failAt(fn, pc-1, f)
			}
			if f := rangeFailure("Координата y в «move to»", y.N); f != nil {
				return t.failAt(fn, pc-1, f)
			}
			if err := e.Host.MoveTo(in.self, x.N, y.N); err != nil {
				return t.failAt(fn, pc-1, hostFailure(err))
			}
		case OpDestroy:
			o := t.pop()
			switch o.K {
			case KObj:
				if err := e.Host.Destroy(o.Obj()); err != nil {
					return t.failAt(fn, pc-1, hostFailure(err))
				}
			case KNothing:
				return t.failAt(fn, pc-1, hostFailure(ErrGone))
			default:
				return t.failAt(fn, pc-1, wrongKind("После «destroy»", KObj, o))
			}
		case OpPrint:
			v := t.pop()
			if e.prints >= e.Limits.PrintsPerFrame {
				return t.failAt(fn, pc-1, fl(CodeTooBig, fmt.Sprintf("Слишком много сообщений «print» за кадр: не больше %d.", e.Limits.PrintsPerFrame),
					"Выводите в консоль реже: например, только когда что-то изменилось."))
			}
			e.prints++
			s, ok := v.show(false, 0, 4*e.Limits.TextChars)
			if !ok {
				return t.failAt(fn, pc-1, tooBig("текст", e.Limits.TextChars))
			}
			t.cost(len(s))
			e.Host.Log(truncateChars(s, e.Limits.PrintChars))

		case OpListAppend, OpListRemove, OpListClear, OpListSort, OpListShuffle:
			if f := t.listCmd(ins.Op); f != nil {
				return t.failAt(fn, pc-1, f)
			}

		case OpName:
			name := prog.Consts[ins.A].S
			if v, ok := e.shared[name]; ok {
				t.push(v)
			} else if id, ok := e.Host.Find(name); ok {
				t.push(Obj(id))
			} else {
				t.push(Value{})
			}
		case OpSetName:
			v := t.pop()
			name := prog.Consts[ins.A].S
			if _, ok := e.shared[name]; !ok {
				msg := "Я не знаю переменной «" + name + "»."
				if _, isObj := e.Host.Find(name); isObj {
					msg = "«" + name + "» — объект сцены, ему нельзя присвоить значение."
				}
				return t.failAt(fn, pc-1, fl(CodeUnknownName, msg, "Общую переменную объявляют так: «shared "+name+" = ...»."))
			}
			e.shared[name] = v
		case OpOther:
			t.push(t.other)
		case OpValue:
			t.push(t.value)
		case OpMouse:
			x, y, held := e.Host.Mouse()
			switch ins.A {
			case 0:
				t.push(Num(x))
			case 1:
				t.push(Num(y))
			default:
				t.push(Bool(held))
			}
		case OpMoveAvoid:
			tag, by, d := t.pop(), t.pop(), t.pop()
			if d.K != KDir {
				return t.failAt(fn, pc-1, wrongKind("Направление в «move»", KDir, d))
			}
			if by.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("Шаг в «move … by»", KNum, by))
			}
			if tag.K != KText {
				return t.failAt(fn, pc-1, wrongKind("Метка в «avoiding»", KText, tag))
			}
			if f := rangeFailure("Шаг в «move … by»", by.N); f != nil {
				return t.failAt(fn, pc-1, f)
			}
			dx, dy := d.Dir().Delta(by.N)
			work, err := e.Host.MoveAvoiding(in.self, dx, dy, tag.S)
			t.work(work)
			if err != nil {
				return t.failAt(fn, pc-1, hostFailure(err))
			}
		case OpSpawn:
			y, x, tpl := t.pop(), t.pop(), t.pop()
			if tpl.K == KObj {
				if v, err := e.Host.GetProp(tpl.Obj(), PropName); err == nil {
					tpl = v
				}
			}
			if tpl.K != KText {
				return t.failAt(fn, pc-1, wrongKind("Шаблон в «spawn»", KText, tpl))
			}
			if x.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("Координата x в «spawn»", KNum, x))
			}
			if y.K != KNum {
				return t.failAt(fn, pc-1, wrongKind("Координата y в «spawn»", KNum, y))
			}
			if f := rangeFailure("Координата x в «spawn»", x.N); f != nil {
				return t.failAt(fn, pc-1, f)
			}
			if f := rangeFailure("Координата y в «spawn»", y.N); f != nil {
				return t.failAt(fn, pc-1, f)
			}
			id, work, err := e.Host.Spawn(tpl.S, x.N, y.N)
			t.left -= work
			if err != nil {
				return t.failAt(fn, pc-1, hostFailure(err))
			}
			t.push(Obj(id))
		case OpSend:
			to, with, text := t.pop(), t.pop(), t.pop()
			if text.K != KText {
				return t.failAt(fn, pc-1, wrongKind("Сообщение в «send»", KText, text))
			}
			m := Message{Text: text.S, Value: with}
			if ins.B == 1 {
				switch to.K {
				case KObj:
					m.To, m.HasTo = to.Obj(), true
				case KNothing:
					return t.failAt(fn, pc-1, hostFailure(ErrGone))
				default:
					return t.failAt(fn, pc-1, wrongKind("После «send … to»", KObj, to))
				}
			}
			if e.sent >= e.Limits.SendsPerFrame || len(e.queue) >= e.Limits.Backlog {
				return t.failAt(fn, pc-1, fl(CodeTooBig, fmt.Sprintf("Слишком много сообщений: за кадр можно отправить не больше %d.", e.Limits.SendsPerFrame),
					"Отправляйте сообщения реже: например, только когда что-то изменилось."))
			}
			e.sent++
			e.queue = append(e.queue, m)

		case OpFail:
			return t.failAt(fn, pc-1, &prog.fails[ins.A])
		default:
			return t.failAt(fn, pc-1, fl(CodeUnsupported, "Неизвестная инструкция "+ins.Op.String()+".", ""))
		}
	}
}

func condFailure(a Value) *failure {
	return fl(CodeCondition, "Условие должно быть «да» или «нет», а тут "+kindNom[a.K]+".", "")
}

func tooBig(what string, limit int) *failure {
	unit := "символов"
	if what == "список" {
		unit = "элементов"
	}
	return fl(CodeTooBig, "Слишком большой "+what+": в нём не больше "+strconv.Itoa(limit)+" "+unit+".", "")
}

var opSigns = map[Op]string{OpSub: "-", OpMul: "*", OpDiv: "/", OpMod: "%"}

// arith implements + - * / %. Every result is wrapped in float64() so that
// the compiler cannot fuse operations (see the package comment).
func (t *Task) arith(op Op, a, b Value) (Value, *failure) {
	if op == OpAdd {
		switch {
		case a.K == KNum && b.K == KNum:
			return Num(float64(a.N + b.N)), nil
		case a.K == KText && b.K == KText:
			limit := t.eng.Limits.TextChars
			if len(a.S)+len(b.S) > 4*limit {
				return Value{}, tooBig("текст", limit)
			}
			s := a.S + b.S
			t.cost(len(s))
			if textTooLong(s, limit) {
				return Value{}, tooBig("текст", limit)
			}
			return Text(s), nil
		case a.K == KNum && b.K == KText, a.K == KText && b.K == KNum:
			return Value{}, fl(CodeAddTypes, "Нельзя сложить "+kindAcc[a.K]+" с "+kindIns[b.K]+".",
				"Чтобы вставить число в текст, используйте { }: \"Счёт: {score}\".")
		}
		bad := a
		if a.K == KNum || a.K == KText {
			bad = b
		}
		return Value{}, fl(CodeWrongType, "Складывать можно только числа или текст, а тут "+kindNom[bad.K]+".", "")
	}
	ctx := "Для «" + opSigns[op] + "»"
	if a.K != KNum {
		return Value{}, wrongKind(ctx, KNum, a)
	}
	if b.K != KNum {
		return Value{}, wrongKind(ctx, KNum, b)
	}
	switch op {
	case OpSub:
		return Num(float64(a.N - b.N)), nil
	case OpMul:
		return Num(float64(a.N * b.N)), nil
	}
	if b.N == 0 {
		return Value{}, fl(CodeDivZero, "Деление на ноль.", "Проверьте делитель до деления: например «if n is not 0».")
	}
	if op == OpDiv {
		return Num(float64(a.N / b.N)), nil
	}
	r := math.Mod(a.N, b.N) // exact on every platform
	if r != 0 && (r < 0) != (b.N < 0) {
		r = float64(r + b.N)
	}
	return Num(r), nil
}

// compare implements is, is not, < > <= >=, in, not in.
func (t *Task) compare(op Op, a, b Value) (Value, *failure) {
	switch op {
	case OpEq, OpNe:
		var eq bool
		switch {
		case a.K != b.K && (a.K == KNothing || b.K == KNothing):
			eq = false
		case a.K != b.K:
			return Value{}, mismatch(a, b)
		default:
			var w work
			eq = equal(a, b, &w)
			t.cost(w.n)
			if w.over {
				return Value{}, fl(CodeTooBig, "Слишком большие списки для сравнения.", "")
			}
		}
		return Bool(eq == (op == OpEq)), nil
	case OpIn, OpNotIn:
		var found bool
		switch b.K {
		case KList:
			var w work
			t.cost(len(b.L.Items))
			for _, it := range b.L.Items {
				if equal(a, it, &w) {
					found = true
					break
				}
			}
			t.cost(w.n)
			if w.over {
				return Value{}, fl(CodeTooBig, "Слишком большие списки для сравнения.", "")
			}
		case KText:
			if a.K != KText {
				return Value{}, fl(CodeWrongType, "В тексте можно искать только текст, а тут "+kindNom[a.K]+".", "Например: \"ab\" in word.")
			}
			t.cost(len(b.S))
			found = strings.Contains(b.S, a.S)
		default:
			return Value{}, fl(CodeWrongType, "После «in» нужен список или текст, а тут "+kindNom[b.K]+".", "")
		}
		return Bool(found == (op == OpIn)), nil
	}
	if a.K != b.K {
		return Value{}, mismatch(a, b)
	}
	sign := "<"
	switch op {
	case OpGt:
		sign = ">"
	case OpLe:
		sign = "<="
	case OpGe:
		sign = ">="
	}
	var c int
	switch a.K {
	case KNum:
		switch {
		case a.N < b.N:
			c = -1
		case a.N > b.N:
			c = 1
		}
		if math.IsNaN(a.N) || math.IsNaN(b.N) {
			return Bool(false), nil
		}
	case KText:
		t.cost(min(len(a.S), len(b.S)))
		c = strings.Compare(a.S, b.S)
	default:
		return Value{}, fl(CodeWrongType, "Сравнивать по величине («"+sign+"») можно числа и текст, а тут "+kindNom[a.K]+".", "")
	}
	switch op {
	case OpLt:
		return Bool(c < 0), nil
	case OpGt:
		return Bool(c > 0), nil
	case OpLe:
		return Bool(c <= 0), nil
	}
	return Bool(c >= 0), nil
}

func mismatch(a, b Value) *failure {
	hint := ""
	if (a.K == KNum && b.K == KText) || (a.K == KText && b.K == KNum) {
		hint = "Число пишут без кавычек: 10, текст — в кавычках: \"10\". Текст в число превращает «number(...)»."
	}
	return fl(CodeCompare, "Нельзя сравнить "+kindAcc[a.K]+" с "+kindIns[b.K]+".", hint)
}

// ---- lists ----

func (t *Task) listPos(l Value, i Value, nameConst int32) (int, *failure) {
	if l.K != KList {
		return 0, fl(CodeWrongType, "Квадратные скобки работают только со списком, а тут "+kindNom[l.K]+".", "")
	}
	if i.K != KNum {
		return 0, wrongKind("Номер элемента", KNum, i)
	}
	n := len(l.L.Items)
	name, nm := "", "В списке"
	if nameConst >= 0 {
		name = t.in.prog.Consts[nameConst].S
		nm = "В списке «" + name + "»"
	}
	count := fmt.Sprintf("В списке сейчас %d %s.", n, plural(n, "элемент", "элемента", "элементов"))
	if n == 0 {
		count = "Список пуст."
	}
	if i.N != math.Floor(i.N) || math.IsInf(i.N, 0) {
		return 0, fl(CodeNoItem, nm+" нет элемента с номером "+FormatNum(i.N)+": номера целые.", count)
	}
	if i.N == 0 {
		first := "первый элемент — номер 1"
		if name != "" {
			first = "первый элемент — " + name + "[1]"
		}
		return 0, fl(CodeNoItem, nm+" нет элемента с номером 0. Нумерация идёт с единицы: "+first+".", count)
	}
	if i.N < 1 || i.N > float64(n) {
		return 0, fl(CodeNoItem, nm+" нет элемента с номером "+FormatNum(i.N)+".", count)
	}
	return int(i.N) - 1, nil
}

func (t *Task) indexGet(l, i Value, nameConst int32) (Value, *failure) {
	p, f := t.listPos(l, i, nameConst)
	if f != nil {
		return Value{}, f
	}
	return l.L.Items[p], nil
}

func (t *Task) indexSet(l, i, v Value, nameConst int32) *failure {
	p, f := t.listPos(l, i, nameConst)
	if f != nil {
		return f
	}
	if v.K == KList {
		if f := t.checkNoCycle(v.L, l.L, "Нельзя положить список внутрь самого себя."); f != nil {
			return f
		}
	}
	l.L.Items[p] = v
	return nil
}

// reaches reports whether target is from or is reachable from it through
// nested lists. When the search gets too long or too deep it stops and sets
// w.over: the answer is then unknown, which is not the same as "yes".
func reaches(from, target *List, w *work, depth int) bool {
	if from == target {
		return true
	}
	if depth > 64 {
		w.over = true
		return false
	}
	for _, it := range from.Items {
		if it.K != KList {
			continue
		}
		if w.n++; w.n > maxCompareWork {
			w.over = true
			return false
		}
		if reaches(it.L, target, w, depth+1) {
			return true
		}
		if w.over {
			return false
		}
	}
	return false
}

// checkNoCycle refuses to put list item into list target if item contains
// target (a list inside itself would loop forever when shown or compared),
// and charges the search to the step budget.
func (t *Task) checkNoCycle(item, target *List, msg string) *failure {
	var w work
	found := reaches(item, target, &w, 0)
	t.cost(w.n)
	switch {
	case found:
		return fl(CodeWrongType, msg, "")
	case w.over:
		return fl(CodeTooBig, "Слишком сложная вложенность списков: не удалось проверить, что список не окажется внутри самого себя.",
			"Не складывайте списки в списки глубже 64 уровней и не делайте их слишком большими.")
	}
	return nil
}

func (t *Task) listCmd(op Op) *failure {
	l := t.pop()
	var item Value
	if op == OpListAppend || op == OpListRemove {
		item = t.pop()
	}
	if l.K != KList {
		return wrongKind("В этой команде", KList, l)
	}
	items := &l.L.Items
	switch op {
	case OpListAppend:
		if len(*items) >= t.eng.Limits.ListItems {
			return tooBig("список", t.eng.Limits.ListItems)
		}
		if item.K == KList {
			if f := t.checkNoCycle(item.L, l.L, "Нельзя добавить список в самого себя."); f != nil {
				return f
			}
		}
		*items = append(*items, item)
	case OpListRemove:
		var w work
		t.cost(len(*items))
		for i, it := range *items {
			if equal(item, it, &w) {
				n := len(*items)
				copy((*items)[i:], (*items)[i+1:])
				(*items)[n-1] = Value{} // do not keep the removed value alive
				*items = (*items)[:n-1]
				break
			}
		}
		t.cost(w.n)
	case OpListClear:
		t.cost(len(*items))
		clear(*items)
		*items = (*items)[:0]
	case OpListSort:
		t.cost(len(*items) * bits.Len(uint(len(*items))))
		if f := sortItems(*items); f != nil {
			return f
		}
	case OpListShuffle:
		t.cost(len(*items))
		for i := len(*items) - 1; i > 0; i-- {
			j := int(t.eng.rng.Below(uint64(i + 1)))
			(*items)[i], (*items)[j] = (*items)[j], (*items)[i]
		}
	}
	return nil
}

func sortItems(items []Value) *failure {
	if len(items) < 2 {
		return nil
	}
	k := items[0].K
	for _, it := range items {
		if it.K != k || (k != KNum && k != KText) {
			return fl(CodeWrongType, "Сортировать можно список только из чисел или только из текста.", "")
		}
	}
	// insertion-free, deterministic: a simple stable merge sort on a copy
	tmp := make([]Value, len(items))
	mergeSort(items, tmp)
	return nil
}

func less(a, b Value) bool {
	if a.K == KNum {
		return a.N < b.N
	}
	return a.S < b.S
}

func mergeSort(a, tmp []Value) {
	if len(a) < 2 {
		return
	}
	mid := len(a) / 2
	mergeSort(a[:mid], tmp[:mid])
	mergeSort(a[mid:], tmp[mid:])
	i, j, k := 0, mid, 0
	for i < mid && j < len(a) {
		if less(a[j], a[i]) {
			tmp[k] = a[j]
			j++
		} else {
			tmp[k] = a[i]
			i++
		}
		k++
	}
	k += copy(tmp[k:], a[i:mid])
	copy(tmp[k:], a[j:])
	copy(a, tmp[:len(a)])
}

// ---- properties and members ----

func (t *Task) getProp(o Value, p Prop) (Value, *failure) {
	switch o.K {
	case KObj:
		v, err := t.eng.Host.GetProp(o.Obj(), p)
		if err != nil {
			return Value{}, hostFailure(err)
		}
		return v, nil
	case KNothing:
		return Value{}, hostFailure(ErrGone)
	}
	return Value{}, wrongKind("Свойство берут у объекта", KObj, o)
}

func (t *Task) setProp(o Value, p Prop, v Value) *failure {
	if o.K == KNothing {
		return hostFailure(ErrGone)
	}
	if o.K != KObj {
		return wrongKind("Менять свойства можно только у объекта", KObj, o)
	}
	if p == PropName {
		return fl(CodeReadOnly, "«name» только для чтения: его нельзя изменить.", "Имя объекта задаётся в панели Scene.")
	}
	if want := propKind[p]; v.K != want {
		return fl(CodeWrongType, "Здесь хранится "+kindNom[want]+", а записывается "+kindNom[v.K]+".", "")
	}
	if v.K == KNum {
		if f := rangeFailure("Свойство объекта", v.N); f != nil {
			return f
		}
	}
	if v.K == KText && textTooLong(v.S, t.eng.Limits.TextChars) {
		return tooBig("текст", t.eng.Limits.TextChars)
	}
	if err := t.eng.Host.SetProp(o.Obj(), p, v); err != nil {
		return hostFailure(err)
	}
	return nil
}

func (t *Task) member(v Value, name string, p Prop) (Value, *failure) {
	switch v.K {
	case KList:
		items := v.L.Items
		switch name {
		case "length":
			return Num(float64(len(items))), nil
		case "first", "last":
			if len(items) == 0 {
				return Value{}, fl(CodeNoItem, "У пустого списка нет «"+name+"».", "Список пуст.")
			}
			if name == "first" {
				return items[0], nil
			}
			return items[len(items)-1], nil
		}
	case KText:
		if name == "length" {
			t.cost(len(v.S))
			n := 0
			for range v.S {
				n++
			}
			return Num(float64(n)), nil
		}
	case KObj:
		if p == PropNone {
			return t.objVar(v.Obj(), name)
		}
		return t.getProp(v, p)
	case KNothing:
		return Value{}, hostFailure(ErrGone)
	}
	return Value{}, fl(CodeWrongType, "У значения ("+kindNom[v.K]+") нет свойства «"+name+"».", "")
}

// instanceOf is the instance of a live object; one that has not started yet is
// created first.
func (e *Engine) instanceOf(id ObjID) *Instance {
	if in := e.insts[id]; in != nil {
		return in
	}
	if st, ok := e.Host.(Starter); ok {
		st.EnsureInstance(id)
	}
	return e.insts[id]
}

// objVar reads the variable name of another object's script.
func (t *Task) objVar(id ObjID, name string) (Value, *failure) {
	e := t.eng
	if !e.Host.Exists(id) {
		return Value{}, hostFailure(ErrGone)
	}
	if in := e.instanceOf(id); in != nil {
		for i, n := range in.prog.Globals {
			if n == name {
				return in.globals[i], nil
			}
		}
	}
	return Value{}, noVar(name)
}

func noVar(name string) *failure {
	return fl(CodeNoProp, "У объекта нет свойства или переменной «"+name+"».",
		"Переменная должна быть объявлена в скрипте этого объекта: «var "+name+" = ...».")
}

// setObjVar writes the variable name of another object's script.
func (t *Task) setObjVar(id ObjID, name string, v Value) *failure {
	e := t.eng
	if !e.Host.Exists(id) {
		return hostFailure(ErrGone)
	}
	if in := e.instanceOf(id); in != nil {
		for i, n := range in.prog.Globals {
			if n == name {
				in.globals[i] = v
				return nil
			}
		}
	}
	return noVar(name)
}

// ---- built-in functions ----

func (t *Task) builtin(id, n int) *failure {
	args := t.stack[len(t.stack)-n:]
	res, f := t.callBuiltin(id, args)
	clear(args)
	t.stack = t.stack[:len(t.stack)-n]
	if f != nil {
		return f
	}
	t.push(res)
	return nil
}

var builtinNames = func() [len(builtinArgs)]string {
	var out [len(builtinArgs)]string
	for n, id := range builtinIDs {
		out[id] = n
	}
	return out
}()

func (t *Task) callBuiltin(id int, a []Value) (Value, *failure) {
	ctx := "Для «" + builtinNames[id] + "»"
	switch id {
	case bAbs, bRound, bFloor, bCeil, bSqrt:
		if a[0].K != KNum {
			return Value{}, wrongKind(ctx, KNum, a[0])
		}
		x := a[0].N
		switch id {
		case bAbs:
			return Num(math.Abs(x)), nil
		case bRound:
			return Num(math.Round(x)), nil
		case bFloor:
			return Num(math.Floor(x)), nil
		case bCeil:
			return Num(math.Ceil(x)), nil
		}
		if x < 0 {
			return Value{}, fl(CodeWrongType, "Корня из отрицательного числа нет: sqrt("+FormatNum(x)+").", "")
		}
		return Num(math.Sqrt(x)), nil
	case bMin, bMax:
		for _, v := range a {
			if v.K != KNum {
				return Value{}, wrongKind(ctx, KNum, v)
			}
		}
		if id == bMin {
			if a[1].N < a[0].N {
				return a[1], nil
			}
			return a[0], nil
		}
		if a[1].N > a[0].N {
			return a[1], nil
		}
		return a[0], nil
	case bRandom:
		for _, v := range a {
			if v.K != KNum {
				return Value{}, wrongKind(ctx, KNum, v)
			}
			if !(math.Abs(v.N) <= 1e15) {
				return Value{}, fl(CodeWrongType, "Для «random»: числа слишком большие.", "")
			}
		}
		lo, hi := int64(math.Floor(a[0].N)), int64(math.Floor(a[1].N))
		if hi < lo {
			lo, hi = hi, lo
		}
		r := t.eng.rng.Below(uint64(hi-lo) + 1)
		return Num(float64(lo + int64(r))), nil
	case bPick:
		if a[0].K != KList {
			return Value{}, wrongKind(ctx, KList, a[0])
		}
		items := a[0].L.Items
		if len(items) == 0 {
			return Value{}, fl(CodeNoItem, "В пустом списке нечего выбирать.", "Список пуст.")
		}
		return items[t.eng.rng.Below(uint64(len(items)))], nil
	case bUpper, bLower, bNumber:
		if a[0].K != KText {
			return Value{}, wrongKind(ctx, KText, a[0])
		}
		switch id {
		case bUpper:
			return Text(strings.ToUpper(a[0].S)), nil
		case bLower:
			return Text(strings.ToLower(a[0].S)), nil
		}
		s := strings.TrimSpace(a[0].S)
		if s != "" && strings.Trim(s, "0123456789+-.eE") == "" {
			if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) {
				return Num(f), nil
			}
		}
		return Value{}, fl(CodeWrongType, "Не могу превратить текст «"+a[0].S+"» в число.", "Число пишут цифрами, например \"12\" или \"3.5\".")
	case bOpposite:
		if a[0].K != KDir {
			return Value{}, wrongKind(ctx, KDir, a[0])
		}
		return DirValue(a[0].Dir().Opposite()), nil
	case bFind:
		if a[0].K != KText {
			return Value{}, wrongKind(ctx, KText, a[0])
		}
		if id, ok := t.eng.Host.Find(a[0].S); ok {
			return Obj(id), nil
		}
		return Value{}, nil
	case bAll:
		if a[0].K != KText {
			return Value{}, wrongKind(ctx, KText, a[0])
		}
		ids, work, err := t.eng.Host.All(a[0].S, t.eng.Limits.ListItems)
		t.work(work)
		if err != nil {
			return Value{}, hostFailure(err)
		}
		t.cost(len(ids))
		items := make([]Value, len(ids))
		for i, id := range ids {
			items[i] = Obj(id)
		}
		return ListValue(&List{Items: items}), nil
	case bCount:
		if a[0].K != KText {
			return Value{}, wrongKind(ctx, KText, a[0])
		}
		return Num(float64(t.eng.Host.Count(a[0].S))), nil
	case bExists:
		switch a[0].K {
		case KObj:
			return Bool(t.eng.Host.Exists(a[0].Obj())), nil
		case KNothing:
			return Bool(false), nil
		}
		return Value{}, wrongKind(ctx, KObj, a[0])
	case bDistance:
		for _, v := range a {
			if v.K == KNothing {
				return Value{}, hostFailure(ErrGone)
			}
			if v.K != KObj {
				return Value{}, wrongKind(ctx, KObj, v)
			}
		}
		d, err := t.eng.Host.Distance(a[0].Obj(), a[1].Obj())
		if err != nil {
			return Value{}, hostFailure(err)
		}
		return Num(d), nil
	case bTouching:
		var tgt Target
		switch a[0].K {
		case KText:
			tgt.Tag = a[0].S
		case KObj:
			tgt.Obj, tgt.HasObj = a[0].Obj(), true
		case KNothing:
			return Bool(false), nil // that object is not in the world
		default:
			return Value{}, fl(CodeWrongType, ctx+": ожидается метка в кавычках или объект, а тут "+kindNom[a[0].K]+".", "")
		}
		var dir Dir
		useDir := len(a) > 1
		if useDir {
			if a[1].K != KDir {
				return Value{}, wrongKind(ctx, KDir, a[1])
			}
			dir = a[1].Dir()
		}
		yes, work, err := t.eng.Host.Touching(t.in.self, tgt, dir, useDir)
		t.work(work)
		if err != nil {
			return Value{}, hostFailure(err)
		}
		return Bool(yes), nil
	}
	return Value{}, fl(CodeUnsupported, "Неизвестная функция.", "")
}

// rangeFailure rejects numbers that must not reach the world: NaN, infinity
// and anything beyond MaxCoord in absolute value.
func rangeFailure(ctx string, f float64) *failure {
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0):
		return fl(CodeWrongType, ctx+": число вне допустимых пределов (не число или бесконечность).",
			"Так бывает после слишком больших вычислений. Проверьте формулу.")
	case math.Abs(f) > MaxCoord:
		return fl(CodeWrongType, ctx+": число вне допустимых пределов (по модулю не больше "+FormatNum(MaxCoord)+").", "")
	}
	return nil
}

// truncateChars keeps the first n characters of s.
func truncateChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j] + "…"
		}
		i++
	}
	return s
}
