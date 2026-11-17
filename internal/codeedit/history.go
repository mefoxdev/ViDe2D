package codeedit

// state — положение курсора и выделения.
type state struct {
	cur, anchor Pos
	sel         bool
}

// edit — одна замена текста: на позиции at removed заменено на inserted.
type edit struct {
	at                Pos
	removed, inserted []rune
}

// step — шаг истории (одна пользовательская операция или склеенная серия).
type step struct {
	kind          string
	edits         []edit
	before, after state
	fresh         bool // не склеивать с предыдущим шагом
	cost          int
}

func (b *Buffer) state() state {
	if _, _, ok := b.selRange(); ok {
		return state{cur: b.cur, anchor: b.anchor, sel: true}
	}
	return state{cur: b.cur, anchor: b.cur}
}

func (b *Buffer) setState(s state) {
	b.cur = b.clamp(s.cur)
	b.anchor = b.clamp(s.anchor)
	b.hasSel = s.sel
	b.want = -1
}

// begin начинает операцию; изменения текста до end() попадают в один шаг.
func (b *Buffer) begin(kind string) {
	b.ensure()
	b.cur, b.anchor = b.clamp(b.cur), b.clamp(b.anchor)
	if _, _, ok := b.selRange(); !ok {
		b.hasSel, b.anchor = false, b.cur // пустое выделение не должно «ожить» после правки
	}
	b.op = &step{kind: kind, before: b.state()}
}

// noMerge запрещает склейку текущей операции с предыдущим шагом.
func (b *Buffer) noMerge() {
	if b.op != nil {
		b.op.fresh = true
	}
}

// apply заменяет [a,e] на ins с записью в текущую операцию.
func (b *Buffer) apply(a, e Pos, ins []rune) {
	a, e = b.clamp(a), b.clamp(e)
	if e.Less(a) {
		a, e = e, a
	}
	removed := b.rawReplace(a, e, ins)
	if b.op != nil {
		b.op.edits = append(b.op.edits, edit{at: a, removed: removed, inserted: append([]rune(nil), ins...)})
		b.op.cost += len(removed) + len(ins)
	}
}

// end завершает операцию и кладёт её в историю.
func (b *Buffer) end() {
	op := b.op
	b.op = nil
	b.cur, b.anchor = b.clamp(b.cur), b.clamp(b.anchor)
	if _, _, ok := b.selRange(); !ok {
		b.hasSel, b.anchor = false, b.cur
	}
	if op == nil || len(op.edits) == 0 {
		return
	}
	op.after = b.state()
	mergeable := op.kind == "type" || op.kind == "back" || op.kind == "del"
	if n := len(b.undo); n > 0 && mergeable && !op.fresh && !b.breakMerge &&
		!op.before.sel {
		last := b.undo[n-1]
		if last.kind == op.kind && last.after == op.before {
			last.edits = append(last.edits, op.edits...)
			last.after = op.after
			last.cost += op.cost
			b.undoCost += op.cost
			b.redo = nil
			b.trim()
			return
		}
	}
	b.breakMerge = false
	b.undo = append(b.undo, op)
	b.undoCost += op.cost
	b.redo = nil
	b.trim()
}

func (b *Buffer) trim() {
	for len(b.undo) > 1 && (len(b.undo) > maxUndoSteps || b.undoCost > maxUndoCost) {
		b.trimmed = true
		b.undoCost -= b.undo[0].cost
		b.undo[0] = nil
		b.undo = b.undo[1:]
	}
}

// CanUndo сообщает, есть ли что отменять.
func (b *Buffer) CanUndo() bool { return len(b.undo) > 0 }

// CanRedo сообщает, есть ли что повторять.
func (b *Buffer) CanRedo() bool { return len(b.redo) > 0 }

// Undo отменяет последний шаг истории и возвращает курсор/выделение,
// какими они были до шага. Возвращает false, если история пуста.
func (b *Buffer) Undo() bool {
	b.ensure()
	n := len(b.undo)
	if n == 0 {
		return false
	}
	s := b.undo[n-1]
	b.undo = b.undo[:n-1]
	b.undoCost -= s.cost
	for i := len(s.edits) - 1; i >= 0; i-- {
		e := s.edits[i]
		b.rawReplace(e.at, endOf(e.at, e.inserted), e.removed)
	}
	b.setState(s.before)
	b.redo = append(b.redo, s)
	b.breakMerge = true
	return true
}

// Redo повторяет последний отменённый шаг. Возвращает false, если нечего повторять.
func (b *Buffer) Redo() bool {
	b.ensure()
	n := len(b.redo)
	if n == 0 {
		return false
	}
	s := b.redo[n-1]
	b.redo = b.redo[:n-1]
	for _, e := range s.edits {
		b.rawReplace(e.at, endOf(e.at, e.removed), e.inserted)
	}
	b.setState(s.after)
	b.undo = append(b.undo, s)
	b.undoCost += s.cost
	b.breakMerge = true
	return true
}
