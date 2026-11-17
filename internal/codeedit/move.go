package codeedit

import "unicode"

// Cursor возвращает позицию курсора.
func (b *Buffer) Cursor() Pos { b.ensure(); b.ClampCursor(); return b.cur }

// Anchor возвращает якорь выделения (равен курсору, если выделения нет).
func (b *Buffer) Anchor() Pos {
	b.ensure()
	if _, _, ok := b.selRange(); !ok {
		return b.cur
	}
	return b.anchor
}

// HasSelection сообщает, есть ли непустое выделение.
func (b *Buffer) HasSelection() bool {
	b.ensure()
	_, _, ok := b.selRange()
	return ok
}

// Selection возвращает нормализованное выделение (start <= end); ok=false,
// если выделения нет или оно пустое.
func (b *Buffer) Selection() (start, end Pos, ok bool) {
	b.ensure()
	return b.selRange()
}

func (b *Buffer) selRange() (s, e Pos, ok bool) {
	if !b.hasSel || len(b.lines) == 0 {
		return Pos{}, Pos{}, false
	}
	s, e = b.clamp(b.anchor), b.clamp(b.cur)
	if s == e {
		return Pos{}, Pos{}, false
	}
	if e.Less(s) {
		s, e = e, s
	}
	return s, e, true
}

// ClearSelection снимает выделение (курсор остаётся на месте).
func (b *Buffer) ClearSelection() { b.hasSel = false }

// SetCursor ставит курсор в (line, col) с ограничением до границ текста.
// При extend=true выделение расширяется от прежнего якоря (Shift+клик).
func (b *Buffer) SetCursor(line, col int, extend bool) {
	b.ensure()
	b.moveTo(Pos{line, col}, extend, false)
}

// SetSelection задаёт выделение: якорь и курсор (с ограничением до границ).
func (b *Buffer) SetSelection(anchor, cursor Pos) {
	b.ensure()
	b.anchor, b.cur = b.clamp(anchor), b.clamp(cursor)
	b.hasSel = true
	b.want = -1
	b.breakMerge = true
}

func (b *Buffer) moveTo(p Pos, extend, keepWant bool) {
	b.cur = b.clamp(b.cur)
	if extend {
		if !b.hasSel {
			b.anchor = b.cur
			b.hasSel = true
		}
	} else {
		b.hasSel = false
	}
	b.cur = b.clamp(p)
	if !keepWant {
		b.want = -1
	}
	b.breakMerge = true
}

// MoveLeft сдвигает курсор влево на символ (через границу строк). Без extend
// при наличии выделения курсор переходит к его началу.
func (b *Buffer) MoveLeft(extend bool) {
	b.ensure()
	if s, _, ok := b.selRange(); ok && !extend {
		b.moveTo(s, false, false)
		return
	}
	p := b.clamp(b.cur)
	switch {
	case p.Col > 0:
		p.Col--
	case p.Line > 0:
		p = Pos{p.Line - 1, len(b.lines[p.Line-1])}
	}
	b.moveTo(p, extend, false)
}

// MoveRight сдвигает курсор вправо на символ. Без extend при наличии
// выделения курсор переходит к его концу.
func (b *Buffer) MoveRight(extend bool) {
	b.ensure()
	if _, e, ok := b.selRange(); ok && !extend {
		b.moveTo(e, false, false)
		return
	}
	p := b.clamp(b.cur)
	switch {
	case p.Col < len(b.lines[p.Line]):
		p.Col++
	case p.Line < len(b.lines)-1:
		p = Pos{p.Line + 1, 0}
	}
	b.moveTo(p, extend, false)
}

func (b *Buffer) moveVert(delta int, extend bool) {
	b.ensure()
	cur := b.clamp(b.cur)
	want := b.want
	if want < 0 {
		want = cur.Col
	}
	line := cur.Line + delta
	var p Pos
	switch {
	case line < 0:
		p = Pos{0, 0}
		want = -1
	case line >= len(b.lines):
		last := len(b.lines) - 1
		p = Pos{last, len(b.lines[last])}
		want = -1
	default:
		p = Pos{line, min(want, len(b.lines[line]))}
	}
	b.moveTo(p, extend, true)
	b.want = want
}

// MoveUp сдвигает курсор на строку вверх, помня желаемую колонку.
// На первой строке переходит в начало текста.
func (b *Buffer) MoveUp(extend bool) { b.moveVert(-1, extend) }

// MoveDown сдвигает курсор на строку вниз, помня желаемую колонку.
// На последней строке переходит в конец текста.
func (b *Buffer) MoveDown(extend bool) { b.moveVert(1, extend) }

// MovePageUp сдвигает курсор на n строк вверх (n<1 считается за 1).
func (b *Buffer) MovePageUp(n int, extend bool) { b.moveVert(-max(n, 1), extend) }

// MovePageDown сдвигает курсор на n строк вниз (n<1 считается за 1).
func (b *Buffer) MovePageDown(n int, extend bool) { b.moveVert(max(n, 1), extend) }

func wordClass(r rune) int {
	switch {
	case r == ' ':
		return 0
	case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
		return 1
	}
	return 2
}

func (b *Buffer) wordLeft(p Pos) Pos {
	p = b.clamp(p)
	if p.Col == 0 {
		if p.Line > 0 {
			return Pos{p.Line - 1, len(b.lines[p.Line-1])}
		}
		return p
	}
	l, c := b.lines[p.Line], p.Col
	for c > 0 && wordClass(l[c-1]) == 0 {
		c--
	}
	if c > 0 {
		k := wordClass(l[c-1])
		for c > 0 && wordClass(l[c-1]) == k {
			c--
		}
	}
	return Pos{p.Line, c}
}

func (b *Buffer) wordRight(p Pos) Pos {
	p = b.clamp(p)
	l, c := b.lines[p.Line], p.Col
	if c >= len(l) {
		if p.Line < len(b.lines)-1 {
			return Pos{p.Line + 1, 0}
		}
		return p
	}
	k := wordClass(l[c])
	for c < len(l) && wordClass(l[c]) == k {
		c++
	}
	for c < len(l) && wordClass(l[c]) == 0 {
		c++
	}
	return Pos{p.Line, c}
}

// MoveWordLeft переходит к началу предыдущего слова (Ctrl+←).
func (b *Buffer) MoveWordLeft(extend bool) {
	b.ensure()
	b.moveTo(b.wordLeft(b.cur), extend, false)
}

// MoveWordRight переходит к началу следующего слова (Ctrl+→).
func (b *Buffer) MoveWordRight(extend bool) {
	b.ensure()
	b.moveTo(b.wordRight(b.cur), extend, false)
}

// MoveHome — «умный» Home: сначала к первому непробельному символу строки,
// если курсор уже там — к колонке 0.
func (b *Buffer) MoveHome(extend bool) {
	b.ensure()
	p := b.clamp(b.cur)
	f := leadingSpaces(b.lines[p.Line])
	if p.Col == f {
		p.Col = 0
	} else {
		p.Col = f
	}
	b.moveTo(p, extend, false)
}

// MoveEnd переходит в конец строки.
func (b *Buffer) MoveEnd(extend bool) {
	b.ensure()
	p := b.clamp(b.cur)
	p.Col = len(b.lines[p.Line])
	b.moveTo(p, extend, false)
}

// MoveTextStart переходит в начало текста (Ctrl+Home).
func (b *Buffer) MoveTextStart(extend bool) { b.ensure(); b.moveTo(Pos{}, extend, false) }

// MoveTextEnd переходит в конец текста (Ctrl+End).
func (b *Buffer) MoveTextEnd(extend bool) {
	b.ensure()
	last := len(b.lines) - 1
	b.moveTo(Pos{last, len(b.lines[last])}, extend, false)
}

// SelectAll выделяет весь текст; курсор оказывается в конце.
func (b *Buffer) SelectAll() {
	b.ensure()
	last := len(b.lines) - 1
	b.SetSelection(Pos{}, Pos{last, len(b.lines[last])})
}

// SelectWord выделяет слово (или группу пробелов/символов) в позиции
// (line, col) — для двойного клика. На пустой строке выделение снимается.
func (b *Buffer) SelectWord(line, col int) {
	b.ensure()
	p := b.clamp(Pos{line, col})
	l := b.lines[p.Line]
	if len(l) == 0 {
		b.moveTo(p, false, false)
		return
	}
	c := p.Col
	if c >= len(l) {
		c = len(l) - 1
	}
	k := wordClass(l[c])
	s, e := c, c+1
	for s > 0 && wordClass(l[s-1]) == k {
		s--
	}
	for e < len(l) && wordClass(l[e]) == k {
		e++
	}
	b.SetSelection(Pos{p.Line, s}, Pos{p.Line, e})
}

// SelectLine выделяет строку line целиком вместе с переводом строки
// (для последней строки — без него).
func (b *Buffer) SelectLine(line int) {
	b.ensure()
	p := b.clamp(Pos{line, 0})
	if p.Line < len(b.lines)-1 {
		b.SetSelection(p, Pos{p.Line + 1, 0})
		return
	}
	b.SetSelection(p, Pos{p.Line, len(b.lines[p.Line])})
}

func leadingSpaces(l []rune) int {
	n := 0
	for n < len(l) && l[n] == ' ' {
		n++
	}
	return n
}

// Selected возвращает выделенный текст ("" если выделения нет).
func (b *Buffer) Selected() string {
	b.ensure()
	s, e, ok := b.selRange()
	if !ok {
		return ""
	}
	return string(b.textRange(s, e))
}
