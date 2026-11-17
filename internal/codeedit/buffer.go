// Пакет codeedit — чистая (без UI) модель текстового редактора VCode для
// языка TrueDSL: строки из рун, курсор, выделение, undo/redo, автоотступы.
//
// Правила текста: перевод строки внутри буфера всегда "\n"; табуляции нет
// (при вставке и загрузке заменяются двумя пробелами); прочие управляющие
// символы отбрасываются. Отступ — ровно 2 пробела (IndentWidth).
//
// Лимиты (MaxLines, MaxBytes, MaxLineLen) никогда не приводят к панике:
//   - вставка/загрузка, превышающая число строк или объём в байтах,
//     обрезается на первом нарушении (остаток отбрасывается);
//   - символы, не помещающиеся в строку длиной MaxLineLen, отбрасываются
//     до ближайшего перевода строки (следующие строки вставляются дальше);
//   - операции Tab/комментирования/дублирования, которые раздули бы текст
//     сверх лимитов, пропускают затронутые строки (или не делают ничего).
//
// После обрезки Truncated() возвращает true до следующей вставки/SetText.
package codeedit

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Лимиты и параметры буфера.
const (
	MaxLines    = 20000   // максимум строк
	MaxBytes    = 1 << 20 // максимум байт текста (UTF-8, включая переводы строк)
	MaxLineLen  = 10000   // максимум рун в строке
	IndentWidth = 2       // ширина отступа в пробелах

	maxUndoSteps = 500     // максимум шагов истории
	maxUndoCost  = 500_000 // суммарный объём истории в рунах (~2 МБ)
)

// Pos — позиция в тексте: номер строки и колонка в рунах (оба с нуля).
type Pos struct{ Line, Col int }

// Less сообщает, что p находится в тексте раньше q.
func (p Pos) Less(q Pos) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col)
}

// Buffer — текстовый буфер редактора. Нулевое значение пригодно к
// использованию (пустой текст), но обычно создаётся через New.
// Буфер не потокобезопасен.
type Buffer struct {
	lines     [][]rune
	bytes     int
	cur       Pos
	anchor    Pos
	hasSel    bool
	want      int // желаемая колонка для Up/Down, -1 если не задана
	version   int
	truncated bool

	undo, redo []*step
	undoCost   int
	op         *step // текущая незавершённая операция
	breakMerge bool  // следующий шаг не склеивать с предыдущим
	trimmed    bool  // старые шаги истории были отброшены по лимиту
}

// New создаёт буфер с текстом s (см. SetText).
func New(s string) *Buffer {
	b := &Buffer{}
	b.SetText(s)
	return b
}

func (b *Buffer) ensure() {
	if len(b.lines) == 0 {
		b.lines = [][]rune{{}}
		b.bytes = 0
		b.cur, b.anchor, b.hasSel = Pos{}, Pos{}, false
	}
}

// SetText заменяет весь текст (с нормализацией и лимитами), ставит курсор в
// начало, снимает выделение и сбрасывает историю undo/redo.
func (b *Buffer) SetText(s string) {
	b.lines = [][]rune{{}}
	b.bytes = 0
	b.cur, b.anchor, b.hasSel, b.want = Pos{}, Pos{}, false, -1
	b.undo, b.redo, b.undoCost, b.op, b.trimmed = nil, nil, 0, nil, false
	b.breakMerge = false
	ins := b.fit(Pos{}, Pos{}, sanitize(s))
	b.rawReplace(Pos{}, Pos{}, ins)
}

// Version возвращает счётчик изменений текста: растёт при каждом изменении
// (включая Undo/Redo и SetText).
func (b *Buffer) Version() int { return b.version }

// Truncated сообщает, что последняя вставка или SetText были обрезаны лимитами.
func (b *Buffer) Truncated() bool { return b.truncated }

// Text возвращает весь текст (строки разделены "\n").
func (b *Buffer) Text() string {
	b.ensure()
	var sb strings.Builder
	sb.Grow(b.bytes)
	for i, l := range b.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(l))
	}
	return sb.String()
}

// Lines возвращает число строк (не меньше 1).
func (b *Buffer) Lines() int {
	b.ensure()
	return len(b.lines)
}

// Line возвращает строку i без перевода строки; вне диапазона — "".
func (b *Buffer) Line(i int) string {
	b.ensure()
	if i < 0 || i >= len(b.lines) {
		return ""
	}
	return string(b.lines[i])
}

// LineLen возвращает длину строки i в рунах; вне диапазона — 0.
func (b *Buffer) LineLen(i int) int {
	b.ensure()
	if i < 0 || i >= len(b.lines) {
		return 0
	}
	return len(b.lines[i])
}

// Size возвращает размер текста в байтах UTF-8.
func (b *Buffer) Size() int { b.ensure(); return b.bytes }

// Len возвращает длину всего текста в рунах (перевод строки — одна руна).
func (b *Buffer) Len() int {
	b.ensure()
	return b.Offset(Pos{len(b.lines) - 1, len(b.lines[len(b.lines)-1])})
}

// Offset переводит позицию (после ограничения до допустимой) в смещение в рунах.
func (b *Buffer) Offset(p Pos) int {
	b.ensure()
	p = b.clamp(p)
	n := 0
	for i := 0; i < p.Line; i++ {
		n += len(b.lines[i]) + 1
	}
	return n + p.Col
}

// PosAt переводит смещение в рунах в позицию; значения вне текста ограничиваются.
func (b *Buffer) PosAt(off int) Pos {
	b.ensure()
	if off < 0 {
		off = 0
	}
	for i, l := range b.lines {
		if off <= len(l) {
			return Pos{i, off}
		}
		off -= len(l) + 1
	}
	last := len(b.lines) - 1
	return Pos{last, len(b.lines[last])}
}

// clamp ограничивает позицию границами текста.
func (b *Buffer) clamp(p Pos) Pos {
	if p.Line < 0 {
		return Pos{}
	}
	if p.Line >= len(b.lines) {
		last := len(b.lines) - 1
		return Pos{last, len(b.lines[last])}
	}
	if p.Col < 0 {
		p.Col = 0
	}
	if n := len(b.lines[p.Line]); p.Col > n {
		p.Col = n
	}
	return p
}

// ClampPos возвращает ближайшую допустимую позицию в тексте.
func (b *Buffer) ClampPos(p Pos) Pos { b.ensure(); return b.clamp(p) }

// ClampCursor приводит курсор и якорь выделения к допустимым позициям.
func (b *Buffer) ClampCursor() {
	b.ensure()
	b.cur = b.clamp(b.cur)
	b.anchor = b.clamp(b.anchor)
}

// sanitize нормализует вставляемый текст: CRLF/CR -> LF, таб -> 2 пробела,
// «пробельные» символы Юникода (NBSP, U+2000..200A, U+3000 ...) -> обычный пробел,
// U+2028/U+2029/U+0085 -> перевод строки, символы нулевой ширины и BOM
// (U+200B..200D, U+2060, U+FEFF) и прочие управляющие символы отбрасываются.
// Так в тексте не остаётся невидимых символов, на которые лексер TrueDSL
// ответил бы «Неожиданный символ».
func sanitize(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch {
		case r == '\r':
			if i < len(s) && s[i] == '\n' {
				i++
			}
			out = append(out, '\n')
		case r == '\n':
			out = append(out, '\n')
		case r == '\t':
			out = append(out, ' ', ' ')
		case r == 0x85 || r == 0x2028 || r == 0x2029:
			out = append(out, '\n')
		case r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF || r == 0x180E:
			// нулевая ширина и BOM: отбрасываем
		case unicode.Is(unicode.Zs, r):
			out = append(out, ' ')
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			// управляющие: отбрасываем
		default:
			out = append(out, r)
		}
	}
	return out
}

func runeBytes(r rune) int {
	if n := utf8.RuneLen(r); n > 0 {
		return n
	}
	return 3
}

func bytesOf(rs []rune) int {
	n := 0
	for _, r := range rs {
		n += runeBytes(r)
	}
	return n
}

// fit обрезает ins так, чтобы замена диапазона [a,e] не нарушила лимиты.
func (b *Buffer) fit(a, e Pos, ins []rune) []rune {
	b.truncated = false
	a, e = b.clamp(a), b.clamp(e)
	lines := len(b.lines) - (e.Line - a.Line)
	bytes := b.bytes - bytesOf(b.textRange(a, e))
	suffix := len(b.lines[e.Line]) - e.Col
	base, seg := a.Col, 0
	skipping := false
	out := make([]rune, 0, len(ins))
	for _, r := range ins {
		if r == '\n' {
			if lines >= MaxLines || bytes+1 > MaxBytes {
				b.truncated = true
				break
			}
			lines++
			bytes++
			base, seg, skipping = 0, 0, false
			out = append(out, r)
			continue
		}
		if skipping {
			continue
		}
		n := runeBytes(r)
		if bytes+n > MaxBytes {
			b.truncated = true
			break
		}
		// Хвост строки учитывается консервативно в каждом сегменте.
		if base+seg+1+suffix > MaxLineLen {
			skipping = true
			b.truncated = true
			continue
		}
		bytes += n
		seg++
		out = append(out, r)
	}
	return out
}

// textRange возвращает текст диапазона [a,e] (позиции допустимы, a<=e).
func (b *Buffer) textRange(a, e Pos) []rune {
	if a.Line == e.Line {
		return slices.Clone(b.lines[a.Line][a.Col:e.Col])
	}
	out := slices.Clone(b.lines[a.Line][a.Col:])
	for i := a.Line + 1; i < e.Line; i++ {
		out = append(out, '\n')
		out = append(out, b.lines[i]...)
	}
	out = append(out, '\n')
	out = append(out, b.lines[e.Line][:e.Col]...)
	return out
}

// endOf возвращает позицию после вставки text в позицию p.
func endOf(p Pos, text []rune) Pos {
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] == '\n' {
			nl := 0
			for _, r := range text {
				if r == '\n' {
					nl++
				}
			}
			return Pos{p.Line + nl, len(text) - 1 - i}
		}
	}
	return Pos{p.Line, p.Col + len(text)}
}

// rawReplace — единственный примитив изменения текста: заменяет [a,e] на ins
// (ins уже проверен fit) и возвращает удалённый текст. Историю не пишет.
func (b *Buffer) rawReplace(a, e Pos, ins []rune) []rune {
	b.ensure()
	a, e = b.clamp(a), b.clamp(e)
	if e.Less(a) {
		a, e = e, a
	}
	removed := b.textRange(a, e)
	head, tail := b.lines[a.Line][:a.Col], b.lines[e.Line][e.Col:]
	var parts [][]rune
	start := 0
	for i, r := range ins {
		if r == '\n' {
			parts = append(parts, ins[start:i])
			start = i + 1
		}
	}
	parts = append(parts, ins[start:])
	nl := make([][]rune, len(parts))
	for i, p := range parts {
		l := make([]rune, 0, len(p)+len(head)+len(tail))
		if i == 0 {
			l = append(l, head...)
		}
		l = append(l, p...)
		if i == len(parts)-1 {
			l = append(l, tail...)
		}
		nl[i] = l
	}
	b.lines = slices.Replace(b.lines, a.Line, e.Line+1, nl...)
	b.bytes += bytesOf(ins) - bytesOf(removed)
	b.version++
	return removed
}
