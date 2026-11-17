package vm

import (
	"fmt"
	"strings"
)

// Runtime error codes (docs/truedsl/errors.md). The type codes TD2xx are the
// ones the static checker uses; the VM reports the same mistakes when the
// checker could not see them. CodeUnsupported is not in errors.md: it marks a
// language feature the VM does not implement yet.
const (
	CodeCompare     = "TD201"
	CodeCondition   = "TD202"
	CodeAddTypes    = "TD204"
	CodeWrongType   = "TD205"
	CodeDivZero     = "TD301"
	CodeNoItem      = "TD302"
	CodeGone        = "TD303"
	CodeSteps       = "TD401"
	CodeTooBig      = "TD402"
	CodeUnknownName = "TD101"
	CodeObjects     = "TD403"
	CodeDeep        = "TD404"
	CodeNoProp      = "TD106"
	CodeReadOnly    = "TD107"
	CodeUnsupported = "TD499"
	CodeInternal    = "TD405" // a bug of the engine, caught by recover
)

// Error is a runtime error of a script: it stops one handler run only.
type Error struct {
	Code    string
	File    string
	Line    int
	Col     int
	EndCol  int
	Message string
	Hint    string
	Handler string // description of the handler, e.g. "when key space pressed"; "" for start-up code
	SrcLine string // the source line, for the quotation
}

// Error is the one-line form.
func (e *Error) Error() string {
	s := fmt.Sprintf("%s, строка %d (%s): %s", e.File, e.Line, e.Code, e.Message)
	if e.Hint != "" {
		s += " " + e.Hint
	}
	return s
}

// Format prints the error as shown in errors.md ("Во время игры, ...").
func (e *Error) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Во время игры, %s, строка %d (%s):\n\n", e.File, e.Line, e.Code)
	if e.SrcLine != "" {
		line := strings.ReplaceAll(e.SrcLine, "\t", " ")
		n := e.EndCol - e.Col
		if limit := len([]rune(line)) + 1 - e.Col; n > limit {
			n = limit
		}
		n = max(n, 1)
		b.WriteString("    " + line + "\n")
		b.WriteString("    " + strings.Repeat(" ", max(e.Col-1, 0)) + strings.Repeat("^", n) + "\n")
	}
	b.WriteString(e.Message)
	if e.Hint != "" {
		b.WriteString(" " + e.Hint)
	}
	b.WriteString("\n")
	if e.Handler != "" {
		fmt.Fprintf(&b, "Обработчик «%s» остановлен. Игра продолжается.\n", e.Handler)
	} else {
		b.WriteString("Запуск скрипта остановлен. Игра продолжается.\n")
	}
	return b.String()
}

// plural picks the Russian word form for n.
func plural(n int, one, few, many string) string {
	m10, m100 := n%10, n%100
	switch {
	case m10 == 1 && m100 != 11:
		return one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		return few
	}
	return many
}
