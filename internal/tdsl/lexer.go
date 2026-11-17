package tdsl

import (
	"strconv"
	"strings"
	"unicode"
)

const (
	maxDepth  = 64 // максимальная вложенность блоков, выражений и текстов со вставками
	maxErrors = 20 // после стольких ошибок разбор останавливается

	maxSourceSize = 2 << 20 // больше байт Parse не разбирает (одна ошибка TD005)
)

// haltPanic — внутренний сигнал «останавливаем разбор» (слишком много ошибок или
// слишком глубокая вложенность). Перехватывается в Parse и наружу не уходит.
type haltPanic struct{}

// sink собирает ошибки лексера и парсера.
type sink struct {
	errs []*Error
	lex  map[*Error]bool // ошибки лексера (см. finishErrors)
}

// addLex добавляет ошибку лексера.
func (s *sink) addLex(e *Error) {
	if s.lex == nil {
		s.lex = map[*Error]bool{}
	}
	s.lex[e] = true
	s.add(e)
}

func (s *sink) add(e *Error) {
	s.errs = append(s.errs, e)
	if len(s.errs) >= maxErrors {
		panic(haltPanic{})
	}
}

type tokKind int

const (
	tEOF tokKind = iota
	tNL
	tIndent
	tDedent
	tName
	tKeyword
	tNumber
	tText
	tOp
)

// token — лексема. text — исходная запись (для текста вместе с кавычками).
type token struct {
	kind   tokKind
	text   string
	line   int
	col    int
	endCol int
	num    float64
	parts  []textPart
}

// textPart — часть текстового литерала: литерал или сырой исходник вставки.
type textPart struct {
	isExpr bool
	lit    string
	src    []rune
	line   int
	col    int // столбец первого символа вставки
	depth  int
}

// keywords — зарезервированные слова языка, которые лексер отдаёт как ключевые.
// Имена свойств (x, y, text ...) и mouse — обычные имена: их «занятость» проверяет
// семантический анализ, а не парсер.
var keywords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`add and as at avoiding by clear destroy down each else
		every for forever go held hide if in is key left message move next no not nothing of or
		other per play print released remember remove repeat repeating restart return right round
		scene second seconds self send shared show shuffle slide sort spawn start stop sound music
		times to touch up update use value var wait when while with yes click pressed
		turn from`) {
		m[w] = true
	}
	return m
}()

// blockOpeners — слова, после которых строка обычно открывает блок.
var blockOpeners = map[string]bool{
	"when": true, "to": true, "if": true, "else": true, "repeat": true, "while": true, "for": true,
}

type lexer struct {
	s      *sink
	lines  [][]rune
	first  int // номер первой строки
	colOff int // сдвиг столбцов (для вставок в тексте)
	depth  int // глубина вложенности вставок

	toks       []token
	brackets   int  // глубина открытых ( и [
	openIndent int  // отступ строки, где открылась внешняя скобка
	pending    bool // в текущей логической строке уже есть токены
	opensBlock bool // предыдущая логическая строка открывала блок
	lineOpens  bool // первая лексема текущей строки — слово, открывающее блок
	lastLine   int
	lastEndCol int
}

func splitLines(src string) [][]rune {
	src = strings.TrimPrefix(src, "\uFEFF")
	raw := strings.Split(src, "\n")
	out := make([][]rune, len(raw))
	for i, r := range raw {
		out[i] = []rune(strings.TrimSuffix(r, "\r"))
	}
	return out
}

// lexSource разбивает весь файл на токены с INDENT/DEDENT и NL.
func lexSource(s *sink, src string) []token {
	l := &lexer{s: s, lines: splitLines(src), first: 1}
	l.run()
	return l.toks
}

// lexInline разбивает одну вставку из текста: без отступов и переводов строк.
func lexInline(s *sink, src []rune, line, colOff, depth int) []token {
	l := &lexer{s: s, lines: [][]rune{src}, first: line, colOff: colOff, depth: depth}
	l.scanLine(src, line, 0)
	l.lastLine, l.lastEndCol = line, len(src)+1+colOff
	l.eof()
	return l.toks
}

func (l *lexer) eof() {
	l.toks = append(l.toks, token{kind: tEOF, line: l.lastLine, col: l.lastEndCol, endCol: l.lastEndCol + 1})
}

func (l *lexer) errAt(code string, line, col, endCol int, msg, hint string) {
	l.s.addLex(&Error{Code: code, Line: line, Col: col + l.colOff, EndCol: endCol + l.colOff, Message: msg, Hint: hint})
}

func (l *lexer) emit(kind tokKind, text string, line, from, to int) *token {
	l.toks = append(l.toks, token{kind: kind, text: text, line: line, col: from + 1 + l.colOff, endCol: to + 1 + l.colOff})
	l.pending = true
	l.lastLine, l.lastEndCol = line, to+1+l.colOff
	return &l.toks[len(l.toks)-1]
}

// endLine завершает логическую строку токеном NL.
func (l *lexer) endLine() {
	if !l.pending {
		return
	}
	l.toks = append(l.toks, token{kind: tNL, line: l.lastLine, col: l.lastEndCol, endCol: l.lastEndCol + 1})
	l.pending = false
	l.opensBlock = l.lineOpens
}

func (l *lexer) run() {
	cur := 0
	for i, line := range l.lines {
		ln := l.first + i
		j := 0
		for j < len(line) && (line[j] == ' ' || line[j] == '\t' || line[j] == '\r') {
			j++
		}
		if j == len(line) || line[j] == '#' {
			continue
		}
		if l.brackets > 0 && j <= l.openIndent && line[j] != ')' && line[j] != ']' {
			// Строка без сдвига относительно открывшей скобку: скобка не закрыта.
			// Завершаем прежнюю строку, а ошибку про скобку сообщит парсер.
			l.brackets = 0
			l.endLine()
		}
		if l.brackets == 0 {
			cur = l.indent(line, ln, j, cur)
			l.lineOpens = false
		}
		l.scanLine(line, ln, j)
		if l.brackets == 0 {
			l.endLine()
		}
	}
	l.endLine()
	if l.lastLine == 0 {
		l.lastLine, l.lastEndCol = l.first+len(l.lines)-1, 1
	}
	for ; cur > 0; cur-- {
		l.toks = append(l.toks, token{kind: tDedent, line: l.lastLine, col: l.lastEndCol, endCol: l.lastEndCol})
	}
	l.eof()
}

// indent разбирает отступ строки и выдаёт INDENT/DEDENT. Возвращает новый уровень.
func (l *lexer) indent(line []rune, ln, j, cur int) int {
	n, tabAt := 0, -1
	for k := 0; k < j; k++ {
		switch line[k] {
		case '\t':
			if tabAt < 0 {
				tabAt = k
			}
			n += 2
		case ' ':
			n++
		}
	}
	reported := false
	if tabAt >= 0 {
		l.errAt(CodeTab, ln, tabAt+1, tabAt+2, "В отступе табуляция: используйте пробелы.",
			"Замените Tab на 2 пробела на каждый уровень.")
		reported = true
	}
	level := n / 2
	if n%2 != 0 {
		if l.opensBlock {
			level++
		}
		l.errAt(CodeIndent, ln, 1, j+1, "Неверный отступ: нужно 2 пробела на уровень.",
			"Сейчас пробелов: "+strconv.Itoa(n)+".")
		reported = true
	}
	if level > cur+1 {
		if !reported {
			l.errAt(CodeIndent, ln, 1, j+1, "Неверный отступ: нужно 2 пробела на уровень.",
				"Здесь можно сдвинуться только на 2 пробела относительно предыдущей строки.")
		}
		level = cur + 1
	}
	for ; cur < level; cur++ {
		l.toks = append(l.toks, token{kind: tIndent, line: ln, col: 1 + l.colOff, endCol: j + 1 + l.colOff})
	}
	for ; cur > level; cur-- {
		l.toks = append(l.toks, token{kind: tDedent, line: ln, col: j + 1 + l.colOff, endCol: j + 1 + l.colOff})
	}
	return cur
}

func isWordStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isWordPart(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// scanLine разбирает токены строки начиная с позиции start.
func (l *lexer) scanLine(line []rune, ln, start int) {
	first := !l.pending
	for k := start; k < len(line); {
		r := line[k]
		switch {
		case r == ' ' || r == '\t' || r == '\r':
			k++
		case r == '#':
			return
		case isWordStart(r):
			e := k
			for e < len(line) && isWordPart(line[e]) {
				e++
			}
			w := string(line[k:e])
			if keywords[w] {
				l.emit(tKeyword, w, ln, k, e)
				if first && blockOpeners[w] {
					l.lineOpens = true
				}
			} else {
				l.emit(tName, w, ln, k, e)
			}
			first = false
			k = e
		case r >= '0' && r <= '9':
			k = l.scanNumber(line, ln, k)
			first = false
		case r == '"':
			k = l.scanText(line, ln, k)
			first = false
		default:
			k = l.scanOp(line, ln, k, start)
			first = false
		}
	}
}

func (l *lexer) scanNumber(line []rune, ln, k int) int {
	e := k
	for e < len(line) && line[e] >= '0' && line[e] <= '9' {
		e++
	}
	if e+1 < len(line) && line[e] == '.' && line[e+1] >= '0' && line[e+1] <= '9' {
		e++
		for e < len(line) && line[e] >= '0' && line[e] <= '9' {
			e++
		}
	}
	raw := string(line[k:e])
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		l.errAt(CodeExpected, ln, k+1, e+1, "Слишком большое число.", "")
		v = 0
	}
	l.emit(tNumber, raw, ln, k, e).num = v
	return e
}

func (l *lexer) scanOp(line []rune, ln, k, lineStart int) int {
	r := line[k]
	two := ""
	if k+1 < len(line) {
		two = string(line[k : k+2])
	}
	switch two {
	case "+=", "-=", "*=", "/=", "<=", ">=":
		l.emit(tOp, two, ln, k, k+2)
		return k + 2
	case "==", "!=":
		l.errAt(CodeExpected, ln, k+1, k+3, "В TrueDSL нет «"+two+"».",
			"Равенство проверяется словом «is», а «не равно» — «is not».")
		return k + 2
	}
	switch r {
	case '+', '-', '*', '/', '%', '<', '>', '=', ',', '.':
		l.emit(tOp, string(r), ln, k, k+1)
	case '(', '[':
		if l.brackets == 0 {
			l.openIndent = lineStart
		}
		l.brackets++
		l.emit(tOp, string(r), ln, k, k+1)
	case ')', ']':
		if l.brackets > 0 {
			l.brackets--
		}
		l.emit(tOp, string(r), ln, k, k+1)
	case '{', '}':
		l.errAt(CodeExpected, ln, k+1, k+2, "Фигурные скобки здесь не нужны.",
			"Они используются только внутри текста: \"Счёт: {score}\".")
	default:
		l.errAt(CodeExpected, ln, k+1, k+2, "Неожиданный символ «"+string(r)+"».", "")
	}
	return k + 1
}

// scanText разбирает текст в кавычках, начиная с открывающей кавычки line[k].
func (l *lexer) scanText(line []rune, ln, k int) int {
	var parts []textPart
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, textPart{lit: lit.String()})
			lit.Reset()
		}
	}
	m := k + 1
	closed := false
loop:
	for m < len(line) {
		switch r := line[m]; r {
		case '"':
			m++
			closed = true
			break loop
		case '\\':
			if m+1 >= len(line) {
				m++
				break loop
			}
			switch line[m+1] {
			case '"', '\\', '{', '}':
				lit.WriteRune(line[m+1])
			case 'n':
				lit.WriteByte('\n')
			default:
				l.errAt(CodeExpected, ln, m+1, m+3, "Неизвестный спецсимвол «\\"+string(line[m+1])+"».",
					"Можно писать: \\\" \\\\ \\n \\{ \\}")
			}
			m += 2
		case '{':
			end := skipInterp(line, m+1, l.depth+1)
			if end < 0 {
				l.errAt(CodeBracket, ln, m+1, m+2, "Не закрыта фигурная скобка в тексте.",
					"Добавьте «}» или напишите «\\{», если нужна сама скобка.")
				m = len(line)
				break loop
			}
			flush()
			parts = append(parts, textPart{isExpr: true, src: line[m+1 : end], line: ln, col: m + 2 + l.colOff, depth: l.depth + 1})
			m = end + 1
		default:
			lit.WriteRune(r)
			m++
		}
	}
	if !closed {
		l.errAt(CodeQuote, ln, k+1, len(line)+1, "Не закрыта кавычка.", "Добавьте « \" » в конце текста.")
		m = len(line)
	}
	flush()
	t := l.emit(tText, string(line[k:m]), ln, k, m)
	t.parts = parts
	return m
}

// skipInterp ищет «}», закрывающую вставку, которая начинается с индекса i.
// Строки в кавычках внутри вставки пропускаются целиком. Возвращает индекс «}» или -1.
func skipInterp(line []rune, i, depth int) int {
	if depth > maxDepth {
		return -1
	}
	for i < len(line) {
		switch line[i] {
		case '}':
			return i
		case '"':
			i = skipString(line, i, depth)
			if i < 0 {
				return -1
			}
		default:
			i++
		}
	}
	return -1
}

// skipString пропускает строку в кавычках с индекса открывающей кавычки i.
// Возвращает индекс за закрывающей кавычкой или -1.
func skipString(line []rune, i, depth int) int {
	i++
	for i < len(line) {
		switch line[i] {
		case '"':
			return i + 1
		case '\\':
			i += 2
		case '{':
			end := skipInterp(line, i+1, depth+1)
			if end < 0 {
				return -1
			}
			i = end + 1
		default:
			i++
		}
	}
	return -1
}
