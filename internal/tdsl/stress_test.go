package tdsl

import (
	"strings"
	"testing"
)

// pipeline прогоняет текст через все стадии, как это делает редактор: Parse, Check,
// Highlight и Complete в нескольких позициях. Тест проходит, если ничего не падает и
// всё завершается (общий таймаут `go test` ловит зависание).
func pipeline(t *testing.T, name, src string) {
	t.Helper()
	script, errs := Parse(src)
	if script == nil {
		t.Fatalf("%s: Script == nil", name)
	}
	_ = Check(script, projEnv)
	_ = Check(script, nil)
	spans := Highlight(src)
	if len(spans) == 0 {
		t.Fatalf("%s: Highlight вернул пусто", name)
	}
	lines := strings.Count(src, "\n")
	for _, p := range [][2]int{{0, 0}, {0, 10}, {lines / 2, 5}, {lines, 3}, {lines, 100000}} {
		_ = Complete(src, p[0], p[1], cmEnv)
	}
	for i, e := range errs {
		if i < 3 {
			_ = e.Format(src)
		}
	}
}

// TestStress проверяет, что патологический ввод обрабатывается без паники и зависания.
func TestStress(t *testing.T) {
	inputs := map[string]string{
		"скобки":  "when start\n  print " + strings.Repeat("(", 200000) + "\n",
		"вставки": "when start\n  print " + strings.Repeat("\"{", 100000) + "\n",
		"отступ":  "when start\n" + strings.Repeat(" ", 1000000) + "hide\n",
		"лесенка": func() string {
			var b strings.Builder
			for i := 0; i < 900; i++ {
				b.WriteString(strings.Repeat("  ", i) + "if a\n")
			}
			return b.String()
		}(),
		"минусы":      "when start\n  print " + strings.Repeat("-", 200000) + "1\n",
		"not":         "when start\n  print " + strings.Repeat("not ", 100000) + "a\n",
		"много_строк": strings.Repeat("when start\n  hide\n", 50000),
		// левые цепочки: допустимая длина, слишком длинная и файл больше 2 МБ
		"цепочка_в_пределе":   "var a = 1" + strings.Repeat("+1", maxChain-1) + "\nwhen start\n  print a\n",
		"цепочка_длинная":     "var a = 1" + strings.Repeat("+1", 100000) + "\n",
		"цепочка_огромная":    "var a = 1" + strings.Repeat("+1", 6_000_000) + "\n",
		"цепочка_умножений":   "var a = 1" + strings.Repeat("*1", 100000) + "\n",
		"цепочка_or":          "var a = yes" + strings.Repeat(" or no", 100000) + "\n",
		"цепочка_точек":       "var a = b" + strings.Repeat(".c", 100000) + "\n",
		"цепочка_индексов":    "var a = b" + strings.Repeat("[1]", 100000) + "\n",
		"цепочка_в_тексте":    "var a = \"{1" + strings.Repeat("+1", 100000) + "}\"\n",
		"цепочки_во_вложении": "var a = " + strings.Repeat("(1"+strings.Repeat("+1", 9000), 30) + strings.Repeat(")", 30) + "\n",
		"длинный_список":      "var a = [" + strings.Repeat("1,", 200000) + "1]\n",
		"вызов_цепочкой":      "when start\n  print " + strings.Repeat("f(", 60) + strings.Repeat(")", 60) + "\n",
	}
	for name, src := range inputs {
		t.Run(name, func(t *testing.T) { pipeline(t, name, src) })
	}
}
