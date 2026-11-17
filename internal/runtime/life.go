package runtime

import (
	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// The offline simulation ("A-Life") of unloaded map chunks.
//
// An object marked "alive" in an optimized map does not freeze when its chunk
// is unloaded: it goes to sleep, and every life period it gets one simplified
// step, a "life tick" (Instance.RunOffline in the VM). When the chunk loads
// again, the state the life ticks produced is what the object comes back
// with. The rules, with the reasons, are in reference.md section 8a.
//
// Determinism: the schedule depends only on the step counter (World.Tick) and
// on the order (chunk position, tree order inside the chunk); never on the
// wall clock, the frame rate of the display or the number of objects loaded.

// Offline simulation parameters. They are constants of the engine, not of the
// project, so that every game (and every tournament) means the same thing by
// "alive".
const (
	// LifePeriodSeconds is how much game time passes between the life ticks of
	// one object: the period in steps is LifePeriodSeconds * TickRate, so it is
	// the same 1 s at 60, 90 and 120 Hz.
	LifePeriodSeconds = 1
	// LifeMaxPerStep is the most life ticks one simulation step may run. Objects
	// that are due and do not fit wait for the next step (round-robin, see
	// World.lifeStep), so a huge map cannot stall a frame.
	LifeMaxPerStep = 200
	// LifeMaxObjects is the most alive objects the world keeps asleep at once;
	// further ones freeze like ordinary objects (one line in the log).
	LifeMaxObjects = 20_000
	// LifeMaxEntities bounds the memory of sleeping objects: the alive objects
	// together with the members of their groups.
	LifeMaxEntities = 100_000
	// lifeBudgetDiv: life ticks together use at most 1/lifeBudgetDiv of the
	// step budget of a frame (vm.Limits.FrameSteps), so they never starve the
	// loaded game and the game never starves them (they run first).
	lifeBudgetDiv = 4
	// lifeTickDiv: one object's life tick uses at most
	// min(HandlerSteps, FrameSteps / (lifeBudgetDiv * lifeTickDiv)) steps in all
	// its handlers together; the limit is enforced while the tick runs.
	lifeTickDiv = 4
	// LifeMailPerObject is how many messages wait for a sleeping object; when
	// more arrive the oldest are dropped (one line in the log).
	LifeMailPerObject = 16
)

// The limits as variables, so that tests can lower them.
var (
	lifeMaxObjects  = LifeMaxObjects
	lifeMaxEntities = LifeMaxEntities
)

// lifeCtx is set on the World while a life tick runs: the script host uses it
// to find the sleeping objects and to refuse what an offline object may not do.
type lifeCtx struct {
	u    *chunkUnit
	self *Entity
}

var (
	errOfflineWrite = &vm.HostError{Code: vm.CodeOffline,
		Message: "Пока объект «живёт» за пределами видимой карты, он не может менять другие объекты.",
		Hint:    "Читать их можно; а чтобы повлиять на другой объект, отправьте ему сообщение: send \"hit\" to Door (спящему объекту оно придёт в его следующий жизненный тик)."}
	errOfflineNoSpawn = &vm.HostError{Code: vm.CodeOffline,
		Message: "Объект за пределами видимой карты не может создавать объекты командой «spawn».",
		Hint:    "Создавайте объекты, когда игрок рядом: например, по сообщению."}
	errOfflineScene = &vm.HostError{Code: vm.CodeOffline,
		Message: "Объект за пределами видимой карты не может менять сцену.",
		Hint:    "Смену сцены пусть запускает объект, который игрок видит."}
)

// lifeTickSteps is the step limit of one life tick of a script engine.
func (w *World) lifeTickSteps() int {
	l := w.scripts.eng.Limits
	return max(1, min(l.HandlerSteps, l.FrameSteps/(lifeBudgetDiv*lifeTickDiv)))
}

// Post implements vm.Mailbox: a message "to" a sleeping object waits for its
// next life tick (at most LifeMailPerObject, the oldest are dropped; and no more
// than the engine's backlog in the whole world). A list or an object in the
// message arrives as nothing. A message to an object that is not asleep
// (destroyed, or not alive) is lost, as before.
func (h *scriptHost) Post(to vm.ObjID, text string, v vm.Value) {
	w := h.w
	e := w.asleepByID[int(to)]
	if e == nil || e.Dead || !e.keepInst {
		return
	}
	if len(e.mail) >= LifeMailPerObject {
		copy(e.mail, e.mail[1:])
		e.mail = e.mail[:len(e.mail)-1]
		w.mailTotal--
		w.scripts.logOnce("life-mail-full", "Объекту вне видимой карты пришло слишком много сообщений: ждут не больше 16, самые старые отброшены.")
	}
	if w.mailTotal >= w.scripts.eng.Limits.Backlog {
		w.scripts.logOnce("life-mail-total", "Слишком много сообщений ждут объекты вне видимой карты: новые отброшены.")
		return
	}
	e.mail = append(e.mail, vm.Message{Text: text, Value: v, To: to, HasTo: true})
	w.mailTotal++
}

func (w *World) lifePeriod() uint64 { return uint64(w.TickRate * LifePeriodSeconds) }

// beginSleep runs when chunk u unloads, before its entities are dropped: it
// picks the alive scripted objects (and the members of their groups) that stay
// in memory as sleepers. The rest of the chunk is only saved in ChunkState.
func (w *World) beginSleep(u *chunkUnit) {
	if w.scripts == nil {
		return
	}
	for _, e := range u.ents {
		if !e.alive || e.Dead || e.Script == "" || e.asleep && e.keepInst {
			continue
		}
		if _, ok := e.Behavior.(*scriptBehavior); !ok {
			continue
		}
		if w.lifeObjs >= lifeMaxObjects {
			w.scripts.logOnce("life-cap", "Слишком много «живых» объектов за пределами карты: не больше 20 000. Остальные замирают, как обычные.")
			continue
		}
		var group []*Entity // e and its members that are not asleep yet
		var walk func(x *Entity)
		walk = func(x *Entity) {
			if !x.asleep {
				group = append(group, x)
			}
			for _, c := range x.Children {
				walk(c)
			}
		}
		walk(e)
		if w.lifeEnts+len(group) > lifeMaxEntities {
			w.scripts.logOnce("life-cap", "Слишком много объектов в «живых» группах за пределами карты: остальные замирают.")
			continue
		}
		for _, x := range group {
			x.asleep = true
		}
		e.keepInst = true
		w.asleepByID[e.ID] = e
		w.lifeObjs++
		w.lifeEnts += len(group)
	}
	// the sleepers in tree order, with a sequence number for the cursor
	u.sleep, u.life, u.sleepByID = nil, nil, map[int]*Entity{}
	for _, e := range u.ents {
		if !e.asleep {
			continue
		}
		u.sleep = append(u.sleep, e)
		u.sleepByID[e.ID] = e
		if e.keepInst {
			e.lifeLast, e.lifeSeq = w.Tick, len(u.life)
			u.life = append(u.life, e)
		}
	}
	u.lifeNext = w.Tick + w.lifePeriod()
	w.cursorLeaves(u)
}

// cursorLeaves fixes the round-robin cursor when the chunk it points into
// starts or ends a sleep: the sequence numbers of that chunk begin anew, so the
// stale position inside it would make its objects wait for the whole round.
// The cursor goes to the start of the chunk instead; other chunks keep their place.
func (w *World) cursorLeaves(u *chunkUnit) {
	if w.lifeCI < len(w.order) && w.order[w.lifeCI] == u.key {
		w.lifeSeq = 0
	}
}

// forgetSleeper takes a sleeping entity out of the live indexes; its script
// instance stays in the engine when it is going to be ticked.
func (w *World) forgetSleeper(e *Entity) {
	delete(w.byID, e.ID)
	e.gIn, e.gDirty, e.gListed, e.bbOK = false, false, false, false
	if !e.keepInst && w.scripts != nil {
		w.scripts.eng.Detach(vm.ObjID(e.ID))
	}
}

// wake ends the sleep of chunk u (it is being loaded): the state of every
// sleeper goes into the chunk's ChunkState, where the load picks it up.
func (w *World) wake(u *chunkUnit) {
	if len(u.sleep) == 0 {
		return
	}
	for _, e := range u.sleep {
		if e.Dead {
			continue
		}
		old := u.state.Entities[e.key]
		if old != nil && e.keepInst {
			if is, ok := old.Behavior.(*vm.InstanceState); ok {
				is.Release() // replaced by the fresh snapshot below
			}
		}
		st := snapshotEntity(e)
		st.PrevX, st.PrevY = st.X, st.Y // no gliding from where it was before the sleep
		if !e.keepInst && old != nil {
			st.Behavior = old.Behavior // a group member without a life of its own: its script state is as saved
		}
		u.state.Entities[e.key] = st
		if e.keepInst {
			w.scripts.eng.Detach(vm.ObjID(e.ID)) // the new entity gets its own instance, from this state
			w.lifeObjs--
			delete(w.asleepByID, e.ID)
			for _, m := range e.mail { // messages that came too late for a life tick go to the object now
				w.mailTotal--
				w.scripts.eng.Enqueue(m)
			}
			e.mail = nil
		}
		w.lifeEnts--
		e.asleep, e.keepInst = false, false
	}
	u.sleep, u.life, u.sleepByID = nil, nil, nil
	w.cursorLeaves(u)
}

// lifeDestroy marks e and the members of its group destroyed; the offline tick
// removes them when the handler is done.
func (w *World) lifeDestroy(e *Entity) {
	if e.pendingDestroy || e.Dead {
		return
	}
	stack := []*Entity{e}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x.pendingDestroy || x.Dead {
			continue
		}
		x.pendingDestroy = true
		w.lifeDQ = append(w.lifeDQ, x)
		stack = append(stack, x.Children...)
	}
}

// flushLifeDestroyed removes the objects destroyed during a life tick of
// chunk u: they never come back with the chunk.
func (w *World) flushLifeDestroyed(u *chunkUnit) {
	if len(w.lifeDQ) == 0 {
		return
	}
	for _, x := range w.lifeDQ {
		x.Dead = true
		u.state.Destroyed[x.key] = true
		if st := u.state.Entities[x.key]; st != nil {
			if is, ok := st.Behavior.(*vm.InstanceState); ok {
				is.Release()
			}
		}
		delete(u.state.Entities, x.key)
		delete(u.sleepByID, x.ID)
		w.scripts.eng.Detach(vm.ObjID(x.ID))
		w.lifeEnts--
		if x.keepInst {
			w.lifeObjs--
			delete(w.asleepByID, x.ID)
			w.mailTotal -= len(x.mail)
			x.mail = nil
		}
		if p := x.Parent; p != nil && !p.pendingDestroy {
			for q := p; q != nil; q = q.Parent {
				q.bbOK = false
			}
		}
	}
	for _, x := range w.lifeDQ {
		if p := x.Parent; p != nil && !p.Dead {
			kids := p.Children[:0]
			for _, c := range p.Children {
				if !c.Dead {
					kids = append(kids, c)
				}
			}
			clear(p.Children[len(kids):])
			p.Children = kids
		}
	}
	w.lifeDQ = w.lifeDQ[:0]
	u.sleep = dropDead(u.sleep)
	u.life = dropDead(u.life)
	// lifeSeq numbers keep their gaps: the cursor only needs them in order
}

// dropDead returns a new list without the dead (the old one may be being
// walked by lifeStep).
func dropDead(list []*Entity) []*Entity {
	out := make([]*Entity, 0, len(list))
	for _, e := range list {
		if !e.Dead {
			out = append(out, e)
		}
	}
	return out
}

// lifeCanWrite is the VM's question "may this script change that object?":
// only the sleeping objects of the chunk that is being ticked.
func (w *World) lifeCanWrite(id vm.ObjID) bool {
	return w.life != nil && w.life.u.sleepByID[int(id)] != nil
}

// lifeStep runs the life ticks that are due in this step. Objects are visited
// in the order (chunk position, tree order) starting from a persistent cursor,
// and each is ticked when LifePeriod has passed since its last tick. At most
// LifeMaxPerStep objects are ticked, and the ticks stop when they have used
// their share of the step budget; the cursor then stays at the first object
// that did not get its turn, so the next step goes on from there: nobody is
// served twice before everybody that is due has been served once.
func (w *World) lifeStep() {
	if w.lifeObjs == 0 || w.scripts == nil || len(w.order) == 0 {
		return
	}
	eng := w.scripts.eng
	start := eng.StepsLeft()
	budget := eng.Limits.FrameSteps / lifeBudgetDiv
	period := w.lifePeriod()
	nc := len(w.order)
	ci0, seq0 := w.lifeCI, w.lifeSeq
	ticked := 0
	for k := 0; k <= nc; k++ {
		ci := (ci0 + k) % nc
		u := w.chunks[w.order[ci]]
		if len(u.life) == 0 || w.Tick < u.lifeNext {
			continue
		}
		for _, e := range u.life {
			if e.Dead {
				continue
			}
			if k == 0 && e.lifeSeq < seq0 {
				continue // before the cursor: visited at the end of the round
			}
			if k == nc && e.lifeSeq >= seq0 {
				break
			}
			if w.Tick-e.lifeLast < period {
				continue
			}
			// A tick starts only if its whole allowance fits in what the life ticks
			// may still use in this step, so none is ever cut by the share; the
			// allowance itself is enforced while the tick runs.
			share := budget - (start - eng.StepsLeft())
			if ticked >= LifeMaxPerStep || eng.StepsLeft() <= 0 || share < w.lifeTickSteps() {
				w.lifeCI, w.lifeSeq = ci, e.lifeSeq
				return
			}
			w.lifeTick(u, e, w.lifeTickSteps())
			ticked++
			w.lifeCI, w.lifeSeq = ci, e.lifeSeq+1
		}
		u.lifeNext = ^uint64(0)
		for _, e := range u.life {
			if !e.Dead {
				u.lifeNext = min(u.lifeNext, e.lifeLast+period)
			}
		}
	}
}

// lifeTick gives one sleeping object its life tick.
func (w *World) lifeTick(u *chunkUnit, e *Entity, maxSteps int) {
	b, _ := e.Behavior.(*scriptBehavior)
	steps := int64(w.Tick - e.lifeLast)
	e.lifeLast = w.Tick
	if b == nil {
		return
	}
	b.ensure(e)
	if b.inst == nil {
		return
	}
	mail := e.mail
	e.mail = nil
	w.mailTotal -= len(mail)
	w.life = &lifeCtx{u: u, self: e}
	errs := b.inst.RunOffline(vm.OfflineTick{Steps: steps, MaxSteps: maxSteps, CanWrite: w.lifeCanWrite, Mail: mail})
	w.life = nil
	w.scripts.report(errs)
	if n := w.takeWork(); n > 0 {
		w.scripts.eng.Charge(n)
	}
	w.flushLifeDestroyed(u)
}

// LifeObjects is the number of alive objects asleep in unloaded chunks.
func (w *World) LifeObjects() int { return w.lifeObjs }
