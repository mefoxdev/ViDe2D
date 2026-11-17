package codeedit

import (
	"strings"
	"unicode"
)

// blockKeywords — слова, строка с которых открывает блок TrueDSL.
var blockKeywords = map[string]bool{
	"when": true, "to": true, "if": true, "else": true,
	"repeat": true, "while": true, "for": true,
}

// stripComment отбрасывает комментарий `# …` в конце строки
// (символ # внутри двойных кавычек комментарием не считается).
func stripComment(s string) string {
	in := false
	for i, r := range s {
		switch {
		case r == '"':
			in = !in
		case r == '#' && !in:
			return s[:i]
		}
	}
	return s
}

// opensBlock сообщает, что код строки (без комментария) открывает блок.
func opensBlock(line []rune) bool {
	f := strings.Fields(stripComment(string(line)))
	return len(f) > 0 && blockKeywords[f[0]]
}

func spaces(n int) []rune {
	out := make([]rune, n)
	for i := range out {
		out[i] = ' '
	}
	return out
}

func isBlank(l []rune) bool {
	for _, r := range l {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// deleteSel удаляет выделение внутри операции; true, если оно было.
func (b *Buffer) deleteSel() bool {
	s, e, ok := b.selRange()
	if !ok {
		return false
	}
	b.apply(s, e, nil)
	b.cur = s
	b.hasSel = false
	b.want = -1
	return true
}

// insertRunes вставляет уже нормализованный текст вместо выделения / в курсор.
func (b *Buffer) insertRunes(ins []rune) {
	s, e := b.cur, b.cur
	if rs, re, ok := b.selRange(); ok {
		s, e = rs, re
	}
	n := len(ins)
	ins = b.fit(s, e, ins)
	if n > 0 && len(ins) == 0 {
		return // всё отвергнуто лимитами: ничего не меняем
	}
	if len(ins) == 0 && s == e {
		return
	}
	b.apply(s, e, ins)
	b.cur = endOf(s, ins)
	b.hasSel = false
	b.want = -1
}

// Insert вставляет текст s в позицию курсора (или вместо выделения);
// курсор оказывается после вставленного. Текст нормализуется (CRLF/CR -> LF,
// таб -> 2 пробела, управляющие символы отбрасываются) и обрезается по
// лимитам (см. описание пакета). Набор одиночных символов подряд
// объединяется в один шаг undo. Автоотступа Insert не делает — см. Enter.
func (b *Buffer) Insert(s string) {
	b.ensure()
	rs := sanitize(s)
	if len(rs) == 0 {
		return
	}
	kind := "insert"
	b.begin(kind)
	if len(rs) == 1 && rs[0] != '\n' {
		b.op.kind = "type"
		// Пробел после непробельного символа начинает новый шаг (по словам).
		if rs[0] == ' ' && b.cur.Col > 0 && b.lines[b.cur.Line][b.cur.Col-1] != ' ' {
			b.noMerge()
		}
	} else {
		b.noMerge()
	}
	if b.HasSelection() {
		b.noMerge()
	}
	b.insertRunes(rs)
	b.end()
}

// Paste вставляет текст из буфера обмена как отдельный шаг undo.
func (b *Buffer) Paste(s string) {
	b.ensure()
	rs := sanitize(s)
	if len(rs) == 0 {
		return
	}
	b.begin("paste")
	b.insertRunes(rs)
	b.end()
}

// Copy возвращает выделенный текст для буфера обмена (буфер не меняется).
func (b *Buffer) Copy() string { return b.Selected() }

// Cut удаляет выделение и возвращает его текст ("" и без изменений, если
// выделения нет).
func (b *Buffer) Cut() string {
	b.ensure()
	s := b.Selected()
	if s == "" {
		return ""
	}
	b.begin("cut")
	b.deleteSel()
	b.end()
	return s
}

// DeleteSelection удаляет выделение; false, если выделения нет.
func (b *Buffer) DeleteSelection() bool {
	b.ensure()
	if !b.HasSelection() {
		return false
	}
	b.begin("cut")
	b.deleteSel()
	b.end()
	return true
}

// Enter вставляет перевод строки с автоотступом: новая строка наследует
// отступ текущей (не больше колонки курсора), а если текст слева от курсора
// открывает блок (when/to/if/else/else if/repeat/while/for; комментарий
// `# …` не учитывается) — получает ещё +2 пробела. Выделение заменяется.
func (b *Buffer) Enter() {
	b.ensure()
	// Лимиты проверяем до любых изменений: иначе пробелы слева от курсора были бы
	// стёрты, а перевод строки отвергнут (текст изменился бы без вставки).
	s, e := b.cur, b.cur
	if rs, re, ok := b.selRange(); ok {
		s, e = rs, re
	}
	s, e = b.clamp(s), b.clamp(e)
	if len(b.lines)-(e.Line-s.Line) >= MaxLines || b.bytes-bytesOf(b.textRange(s, e)) >= MaxBytes {
		b.truncated = true
		return
	}
	b.begin("enter")
	b.deleteSel()
	line := b.lines[b.cur.Line]
	ind := min(leadingSpaces(line), b.cur.Col)
	if ind > 0 && ind == b.cur.Col {
		// Слева от курсора только пробелы: убираем их, отступ уйдёт в новую строку.
		b.apply(Pos{b.cur.Line, 0}, b.cur, nil)
		b.cur.Col = 0
		line = b.lines[b.cur.Line]
	}
	if opensBlock(line[:b.cur.Col]) {
		ind += IndentWidth
	}
	b.insertRunes(append([]rune{'\n'}, spaces(ind)...))
	b.end()
}

// Backspace удаляет выделение либо символ слева; в начале строки склеивает
// её с предыдущей; внутри пробельного отступа стирает сразу до предыдущей
// колонки, кратной 2.
func (b *Buffer) Backspace() {
	b.ensure()
	b.begin("back")
	switch {
	case b.HasSelection():
		b.noMerge()
		b.deleteSel()
	case b.cur.Col == 0:
		if b.cur.Line > 0 {
			prev := Pos{b.cur.Line - 1, len(b.lines[b.cur.Line-1])}
			b.apply(prev, b.cur, nil)
			b.cur = prev
		}
	default:
		c := b.cur.Col
		t := c - 1
		if leadingSpaces(b.lines[b.cur.Line]) >= c {
			t = (c - 1) / IndentWidth * IndentWidth
		}
		b.apply(Pos{b.cur.Line, t}, b.cur, nil)
		b.cur.Col = t
	}
	b.want = -1
	b.end()
}

// Delete удаляет выделение либо символ справа; в конце строки склеивает со
// следующей.
func (b *Buffer) Delete() {
	b.ensure()
	b.begin("del")
	switch {
	case b.HasSelection():
		b.noMerge()
		b.deleteSel()
	case b.cur.Col < len(b.lines[b.cur.Line]):
		b.apply(b.cur, Pos{b.cur.Line, b.cur.Col + 1}, nil)
	case b.cur.Line < len(b.lines)-1:
		b.apply(b.cur, Pos{b.cur.Line + 1, 0}, nil)
	}
	b.want = -1
	b.end()
}

// DeleteWordBack удаляет выделение либо слово слева (Ctrl+Backspace).
func (b *Buffer) DeleteWordBack() {
	b.ensure()
	b.begin("delword")
	if !b.deleteSel() {
		if t := b.wordLeft(b.cur); t != b.cur {
			b.apply(t, b.cur, nil)
			b.cur = t
		}
	}
	b.want = -1
	b.end()
}

// DeleteWordForward удаляет выделение либо слово справа (Ctrl+Delete).
func (b *Buffer) DeleteWordForward() {
	b.ensure()
	b.begin("delword")
	if !b.deleteSel() {
		if t := b.wordRight(b.cur); t != b.cur {
			b.apply(b.cur, t, nil)
		}
	}
	b.want = -1
	b.end()
}

// lineRange возвращает диапазон строк, затронутых выделением или курсором.
// Если выделение многострочное и заканчивается в колонке 0, последняя
// строка (на которой нет ни одного выделенного символа) не входит.
func (b *Buffer) lineRange() (first, last int) {
	if s, e, ok := b.selRange(); ok {
		last = e.Line
		if e.Col == 0 && e.Line > s.Line {
			last--
		}
		return s.Line, last
	}
	l := b.clamp(b.cur).Line
	return l, l
}

// room сообщает, можно ли добавить n рун в строку line, не нарушив лимиты.
func (b *Buffer) room(line, n int) bool {
	return len(b.lines[line])+n <= MaxLineLen && b.bytes+n <= MaxBytes
}

// shiftIns корректирует позицию p после вставки n рун в строку line в
// колонке at (incl — сдвигать ли позицию, стоящую ровно в at).
func shiftIns(p *Pos, line, at, n int, incl bool) {
	if p.Line == line && (p.Col > at || (incl && p.Col == at)) {
		p.Col += n
	}
}

// shiftInsSel сдвигает курсор и якорь после вставки n рун в строку line в
// колонке at; начало выделения, стоящее ровно в at, остаётся на месте.
func (b *Buffer) shiftInsSel(line, at, n int) {
	_, _, sel := b.selRange()
	curStart := sel && b.cur.Less(b.anchor)
	ancStart := sel && b.anchor.Less(b.cur)
	shiftIns(&b.cur, line, at, n, !curStart)
	shiftIns(&b.anchor, line, at, n, !ancStart)
}

// shiftDel корректирует позицию p после удаления n рун из строки line с колонки at.
func shiftDel(p *Pos, line, at, n int) {
	if p.Line == line && p.Col > at {
		p.Col = max(at, p.Col-n)
	}
}

// Tab: без выделения (или с выделением в пределах одной строки) вставляет
// пробелы до ближайшей колонки, кратной 2, заменяя выделение; при выделении
// нескольких строк увеличивает отступ всех этих (непустых) строк на 2.
func (b *Buffer) Tab() {
	b.ensure()
	b.begin("tab")
	b.noMerge()
	if s, e, ok := b.selRange(); ok && s.Line != e.Line {
		first, last := b.lineRange()
		for l := first; l <= last; l++ {
			if len(b.lines[l]) == 0 || !b.room(l, IndentWidth) {
				continue
			}
			b.apply(Pos{l, 0}, Pos{l, 0}, spaces(IndentWidth))
			shiftIns(&b.cur, l, 0, IndentWidth, false)
			shiftIns(&b.anchor, l, 0, IndentWidth, false)
		}
	} else {
		start := b.cur
		if ok {
			start = s
		}
		n := IndentWidth - start.Col%IndentWidth
		b.insertRunes(spaces(n))
	}
	b.end()
}

// ShiftTab убирает до 2 пробелов отступа у строки курсора или у всех
// строк выделения.
func (b *Buffer) ShiftTab() {
	b.ensure()
	b.begin("untab")
	b.noMerge()
	first, last := b.lineRange()
	for l := first; l <= last; l++ {
		n := min(IndentWidth, leadingSpaces(b.lines[l]))
		if n == 0 {
			continue
		}
		b.apply(Pos{l, 0}, Pos{l, n}, nil)
		shiftDel(&b.cur, l, 0, n)
		shiftDel(&b.anchor, l, 0, n)
	}
	b.want = -1
	b.end()
}

// DuplicateLines дублирует строку курсора (или все строки выделения) ниже;
// курсор и выделение переходят на копию (Ctrl+D).
func (b *Buffer) DuplicateLines() {
	b.ensure()
	first, last := b.lineRange()
	n := last - first + 1
	var text []rune
	for l := first; l <= last; l++ {
		text = append(text, '\n')
		text = append(text, b.lines[l]...)
	}
	if len(b.lines)+n > MaxLines || b.bytes+bytesOf(text) > MaxBytes {
		return
	}
	b.begin("dup")
	b.apply(Pos{last, len(b.lines[last])}, Pos{last, len(b.lines[last])}, text)
	b.cur.Line += n
	b.anchor.Line += n
	b.end()
}

// DeleteLines удаляет строку курсора или все строки выделения (Ctrl+Shift+K).
func (b *Buffer) DeleteLines() {
	b.ensure()
	b.begin("delline")
	first, last := b.lineRange()
	switch {
	case last < len(b.lines)-1:
		b.apply(Pos{first, 0}, Pos{last + 1, 0}, nil)
	case first > 0:
		b.apply(Pos{first - 1, len(b.lines[first-1])}, Pos{last, len(b.lines[last])}, nil)
	default:
		b.apply(Pos{0, 0}, Pos{last, len(b.lines[last])}, nil)
	}
	line := min(first, len(b.lines)-1)
	b.cur = b.clamp(Pos{line, b.cur.Col})
	b.hasSel = false
	b.want = -1
	b.end()
}

// MoveLinesUp меняет строку (или блок выделенных строк) местами со строкой
// выше (Alt+↑). На первой строке ничего не делает.
func (b *Buffer) MoveLinesUp() {
	b.ensure()
	first, last := b.lineRange()
	if first == 0 {
		return
	}
	b.begin("moveline")
	var text []rune
	for l := first; l <= last; l++ {
		text = append(text, b.lines[l]...)
		text = append(text, '\n')
	}
	text = append(text, b.lines[first-1]...)
	b.apply(Pos{first - 1, 0}, Pos{last, len(b.lines[last])}, text)
	b.cur.Line--
	b.anchor.Line--
	b.end()
}

// MoveLinesDown меняет строку (или блок выделенных строк) местами со строкой
// ниже (Alt+↓). На последней строке ничего не делает.
func (b *Buffer) MoveLinesDown() {
	b.ensure()
	first, last := b.lineRange()
	if last >= len(b.lines)-1 {
		return
	}
	b.begin("moveline")
	text := append([]rune(nil), b.lines[last+1]...)
	for l := first; l <= last; l++ {
		text = append(text, '\n')
		text = append(text, b.lines[l]...)
	}
	b.apply(Pos{first, 0}, Pos{last + 1, len(b.lines[last+1])}, text)
	b.cur.Line++
	b.anchor.Line++
	b.end()
}

// ToggleComment комментирует (префикс "# " в колонке минимального отступа)
// или, если все непустые строки уже закомментированы, раскомментирует
// строку курсора / строки выделения (Ctrl+/).
func (b *Buffer) ToggleComment() {
	b.ensure()
	first, last := b.lineRange()
	nonblank, all := 0, true
	minInd := -1
	for l := first; l <= last; l++ {
		ln := b.lines[l]
		if isBlank(ln) {
			continue
		}
		nonblank++
		ind := leadingSpaces(ln)
		if minInd < 0 || ind < minInd {
			minInd = ind
		}
		if ind >= len(ln) || ln[ind] != '#' {
			all = false
		}
	}
	b.begin("comment")
	if nonblank > 0 && all {
		for l := first; l <= last; l++ {
			ln := b.lines[l]
			if isBlank(ln) {
				continue
			}
			ind := leadingSpaces(ln)
			n := 1
			if ind+1 < len(ln) && ln[ind+1] == ' ' {
				n = 2
			}
			b.apply(Pos{l, ind}, Pos{l, ind + n}, nil)
			shiftDel(&b.cur, l, ind, n)
			shiftDel(&b.anchor, l, ind, n)
		}
	} else {
		if minInd < 0 {
			minInd = 0
		}
		for l := first; l <= last; l++ {
			ln := b.lines[l]
			if nonblank > 0 && isBlank(ln) || !b.room(l, 2) {
				continue
			}
			at := min(minInd, len(ln))
			b.apply(Pos{l, at}, Pos{l, at}, []rune("# "))
			b.shiftInsSel(l, at, 2)
		}
	}
	b.want = -1
	b.end()
}
