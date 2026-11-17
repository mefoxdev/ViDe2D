package tdsl

import (
	"fmt"
	"strings"
)

// Коды синтаксических ошибок (см. docs/truedsl/errors.md).
const (
	CodeIndent      = "TD001" // неверный отступ
	CodeTab         = "TD002" // табуляция в отступе
	CodeQuote       = "TD003" // не закрыта кавычка
	CodeUnknownWord = "TD004" // не знаю это слово
	CodeExpected    = "TD005" // здесь ожидалось другое
	CodeBracket     = "TD006" // не закрыта скобка
)

// Error — синтаксическая ошибка. Col и EndCol — столбцы (с единицы, в символах);
// EndCol стоит сразу за последним подчёркиваемым символом.
type Error struct {
	Code    string
	File    string // необязательно; заполняет вызывающий код для красивого заголовка
	Line    int
	Col     int
	EndCol  int
	Message string
	Hint    string // необязательная подсказка, например «Может быть, вы хотели «right»?»
}

// Error реализует интерфейс error: короткая однострочная запись.
func (e *Error) Error() string {
	s := fmt.Sprintf("строка %d, столбец %d (%s): %s", e.Line, e.Col, e.Code, e.Message)
	if e.Hint != "" {
		s += " " + e.Hint
	}
	return s
}

// Format выводит ошибку так, как показано в errors.md: заголовок, цитата строки
// из src и стрелки ^^^^ под нужным местом, затем сообщение с подсказкой.
func (e *Error) Format(src string) string {
	var b strings.Builder
	if e.File != "" {
		fmt.Fprintf(&b, "Проблема в %s, строка %d (%s):\n\n", e.File, e.Line, e.Code)
	} else {
		fmt.Fprintf(&b, "Проблема в строке %d (%s):\n\n", e.Line, e.Code)
	}
	if line, ok := sourceLine(src, e.Line); ok {
		line = strings.ReplaceAll(line, "\t", " ")
		n := e.EndCol - e.Col
		if max := len([]rune(line)) + 1 - e.Col; n > max {
			n = max
		}
		if n < 1 {
			n = 1
		}
		pad := e.Col - 1
		if pad < 0 {
			pad = 0
		}
		b.WriteString("    " + line + "\n")
		b.WriteString("    " + strings.Repeat(" ", pad) + strings.Repeat("^", n) + "\n")
	}
	b.WriteString(e.Message)
	if e.Hint != "" {
		b.WriteString(" " + e.Hint)
	}
	b.WriteString("\n")
	return b.String()
}

// sourceLine возвращает строку номер n (с единицы) без перевода строки.
func sourceLine(src string, n int) (string, bool) {
	if n < 1 {
		return "", false
	}
	for i := 1; ; i++ {
		var line string
		idx := strings.IndexByte(src, '\n')
		if idx < 0 {
			line, src = src, ""
		} else {
			line, src = src[:idx], src[idx+1:]
		}
		if i == n {
			return strings.TrimSuffix(line, "\r"), true
		}
		if idx < 0 {
			return "", false
		}
	}
}

// suggest ищет среди candidates слово, похожее на word: совпадение без учёта регистра
// или малое расстояние редактирования (с учётом перестановки соседних букв).
// Допустимое расстояние: 0 для слов до 2 букв, 1 до 4 букв, иначе 2 (но не больше maxDist).
// Возвращает пустую строку, если ничего похожего нет.
func suggest(word string, candidates []string, maxDist int) string {
	w := []rune(word)
	limit := 2
	switch {
	case len(w) <= 2:
		limit = 0
	case len(w) <= 4:
		limit = 1
	}
	if limit > maxDist {
		limit = maxDist
	}
	best, bestDist := "", limit+1
	for _, c := range candidates {
		if strings.EqualFold(c, word) {
			if c == word {
				continue
			}
			return c
		}
		if d := editDistance(w, []rune(c)); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// editDistance — расстояние Левенштейна с перестановкой соседних символов за один шаг.
func editDistance(a, b []rune) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				v = min(v, prev2[j-2]+1)
			}
			cur[j] = v
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}

// didYouMean формирует подсказку «Может быть, вы хотели «слово»?».
func didYouMean(word string) string {
	if word == "" {
		return ""
	}
	return "Может быть, вы хотели «" + word + "»?"
}
