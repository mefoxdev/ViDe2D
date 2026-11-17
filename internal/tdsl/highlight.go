package tdsl

import (
	"strings"
	"unicode"
)

// HighlightKind — вид раскрашенного куска кода.
type HighlightKind int

// Виды кусков подсветки. Всё, что не попало в Span (пробелы), рисуется обычным цветом.
const (
	KindName      HighlightKind = iota // прочие имена
	KindKeyword                        // when, if, else, repeat, while, for, each, return, to, var, shared, use, stop, next ...
	KindCommand                        // move, show, hide, destroy, spawn, play, print, send, wait ...
	KindEvent                          // слова события после when: start, update, key, click, touch ...
	KindBuiltin                        // встроенные функции: abs, min, find, touching ...
	KindProperty                       // x, y, width ... и любое имя после точки
	KindDirection                      // left right up down
	KindLiteral                        // yes no nothing
	KindSelfWord                       // self other value mouse
	KindNumber                         // 42, 0.5
	KindText                           // строка в кавычках (без частей {…})
	KindComment                        // # до конца строки
	KindOperator                       // + - * / % < > <= >= = += -= *= /= is not and or in per
	KindPunct                          // ( ) [ ] , . и скобки { } вставки в тексте
	KindError                          // неизвестные символы
)

var hlKindNames = [...]string{"Name", "Keyword", "Command", "Event", "Builtin", "Property",
	"Direction", "Literal", "SelfWord", "Number", "Text", "Comment", "Operator", "Punct", "Error"}

func (k HighlightKind) String() string {
	if k >= 0 && int(k) < len(hlKindNames) {
		return hlKindNames[k]
	}
	return "?"
}

// Span — раскрашенный кусок строки. Start и End — колонки в РУНАХ (с нуля),
// End не включается: кусок — это строка[Start:End] в рунах.
type Span struct {
	Start, End int
	Kind       HighlightKind
}

func hlSet(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

var (
	hlKeywords = hlSet(`when if else repeat while for each return to var shared use stop next
		forever times repeating round second seconds as at by from with of scene sound music
		avoiding key held pressed released touch start update click message every`)
	hlCommands = hlSet(`move show hide destroy spawn play print send wait slide turn go restart
		remember add remove clear sort shuffle`)
	hlEventWords = hlSet(`start update key click touch stop touching message every pressed released held`)
	hlBuiltins   = hlSet(`abs min max round floor ceil sqrt sin cos random pick upper lower number
		find all count exists distance touching opposite recall`)
	hlProperties = hlSet(`x y width height angle visible image tint flip layer tag name text font_size`)
	hlDirections = hlSet(`left right up down`)
	hlLiterals   = hlSet(`yes no nothing`)
	hlSelfWords  = hlSet(`self other value mouse`)
	hlOperators  = hlSet(`is not and or in per`)
)

const hlMaxDepth = 16

// Highlight раскрашивает исходный текст построчно: результат[i] — куски i-й строки
// (в порядке возрастания Start, без пересечений; пробелы не входят ни в один кусок).
// Строки делятся по «\n» (завершающий «\r» отбрасывается). Работает на любом тексте,
// в том числе недописанном и с ошибками: Parse не используется, состояние между
// строками не нужно (многострочных конструкций в языке нет).
func Highlight(src string) [][]Span {
	lines := strings.Split(src, "\n")
	out := make([][]Span, len(lines))
	for i, ln := range lines {
		out[i] = HighlightLine(strings.TrimSuffix(ln, "\r"))
	}
	return out
}

// HighlightLine раскрашивает одну строку (без перевода строки).
func HighlightLine(line string) []Span {
	h := &hlState{r: []rune(line), spans: make([]Span, 0, 8)}
	h.whenLine = h.startsWithWhen()
	h.code(0, false, 0)
	return h.spans
}

type hlState struct {
	r        []rune
	spans    []Span
	whenLine bool // строка начинается с «when»: слова события раскрашиваются как Event
	afterDot bool
	prevStop bool // предыдущее слово — «stop»
}

func hlIsStart(c rune) bool { return c == '_' || unicode.IsLetter(c) }
func hlIsPart(c rune) bool  { return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c) }

func (h *hlState) startsWithWhen() bool {
	i := 0
	for i < len(h.r) && (h.r[i] == ' ' || h.r[i] == '\t') {
		i++
	}
	r := h.r
	return i+4 <= len(r) && string(r[i:i+4]) == "when" && (i+4 == len(r) || !hlIsPart(r[i+4]))
}

func (h *hlState) add(s, e int, k HighlightKind) {
	if e > s {
		h.spans = append(h.spans, Span{s, e, k})
	}
}

// code разбирает код с позиции i. Если inBrace — это вставка {…} в тексте, и разбор
// останавливается на парной «}» (она не поглощается). Возвращает позицию остановки.
func (h *hlState) code(i int, inBrace bool, depth int) int {
	r := h.r
	n := len(r)
	braces := 0
	h.afterDot = false
	for i < n {
		c := r[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case c == '#':
			h.add(i, n, KindComment)
			return n
		case c == '"':
			i = h.text(i, depth)
			h.afterDot, h.prevStop = false, false
			continue
		case inBrace && c == '{':
			braces++
			h.add(i, i+1, KindPunct)
			i++
		case inBrace && c == '}':
			if braces == 0 {
				return i
			}
			braces--
			h.add(i, i+1, KindPunct)
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < n && r[j] >= '0' && r[j] <= '9' {
				j++
			}
			if j+1 < n && r[j] == '.' && r[j+1] >= '0' && r[j+1] <= '9' {
				j++
				for j < n && r[j] >= '0' && r[j] <= '9' {
					j++
				}
			}
			// «2abc» — число и имя вплотную: имя раскрасится отдельно
			h.add(i, j, KindNumber)
			i = j
			h.afterDot, h.prevStop = false, false
			continue
		case hlIsStart(c):
			j := i
			for j < n && hlIsPart(r[j]) {
				j++
			}
			h.add(i, j, h.classify(r[i:j], j))
			h.prevStop = j-i == 4 && string(r[i:j]) == "stop"
			h.afterDot = false
			i = j
			continue
		case c == '.':
			h.add(i, i+1, KindPunct)
			i++
			h.afterDot = true
			continue
		case strings.ContainsRune("()[],", c):
			h.add(i, i+1, KindPunct)
			i++
		case c == '<' || c == '>' || c == '=':
			j := i + 1
			if j < n && r[j] == '=' {
				j++
			}
			h.add(i, j, KindOperator)
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/':
			j := i + 1
			if j < n && r[j] == '=' {
				j++
			}
			h.add(i, j, KindOperator)
			i = j
		case c == '%':
			h.add(i, i+1, KindOperator)
			i++
		case c == '!' && i+1 < n && r[i+1] == '=':
			h.add(i, i+2, KindError)
			i += 2
		default:
			h.add(i, i+1, KindError)
			i++
		}
		h.afterDot, h.prevStop = false, false
	}
	return n
}

// text разбирает строку, начинающуюся с кавычки в позиции i; возвращает позицию после неё.
func (h *hlState) text(i int, depth int) int {
	r := h.r
	n := len(r)
	start := i
	i++
	for i < n {
		switch r[i] {
		case '\\':
			i += 2
			if i > n {
				i = n
			}
		case '"':
			h.add(start, i+1, KindText)
			return i + 1
		case '{':
			if depth >= hlMaxDepth {
				i++
				continue
			}
			h.add(start, i, KindText)
			h.add(i, i+1, KindPunct)
			saveDot, savePrev := h.afterDot, h.prevStop
			j := h.code(i+1, true, depth+1)
			h.afterDot, h.prevStop = saveDot, savePrev
			if j < n && r[j] == '}' {
				h.add(j, j+1, KindPunct)
				j++
			}
			i, start = j, j
			if i >= n {
				return n
			}
		default:
			i++
		}
	}
	h.add(start, n, KindText) // незакрытая строка — до конца строки
	return n
}

func (h *hlState) classify(r []rune, end int) HighlightKind {
	if h.afterDot {
		return KindProperty
	}
	if len(r) > 16 {
		return KindName
	}
	var buf [16]byte
	b := buf[:len(r)]
	for i, c := range r {
		if c >= 0x80 {
			return KindName // все служебные слова — ASCII
		}
		b[i] = byte(c)
	}
	// m[string(b)] компилятор выполняет без выделения памяти
	if h.whenLine && hlEventWords[string(b)] {
		if string(b) == "touching" && !h.prevStop {
			return KindBuiltin
		}
		return KindEvent
	}
	switch {
	case hlOperators[string(b)]:
		return KindOperator
	case string(b) == "round":
		// «round(x)» — функция, «next round» — ключевое слово
		for k := end; k < len(h.r); k++ {
			if h.r[k] == ' ' || h.r[k] == '\t' {
				continue
			}
			if h.r[k] == '(' {
				return KindBuiltin
			}
			break
		}
		return KindKeyword
	case string(b) == "touching" && h.prevStop:
		return KindKeyword
	case hlKeywords[string(b)]:
		return KindKeyword
	case hlCommands[string(b)]:
		return KindCommand
	case hlBuiltins[string(b)]:
		return KindBuiltin
	case hlProperties[string(b)]:
		return KindProperty
	case hlDirections[string(b)]:
		return KindDirection
	case hlLiterals[string(b)]:
		return KindLiteral
	case hlSelfWords[string(b)]:
		return KindSelfWord
	}
	return KindName
}
