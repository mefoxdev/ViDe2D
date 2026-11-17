package tdsl

import (
	"strings"
	"testing"
	"time"
)

// TestStress проверяет, что патологический ввод разбирается быстро и без паники.
func TestStress(t *testing.T) {
	inputs := map[string]string{
		"скобки":  "when start\n  print " + strings.Repeat("(", 200000) + "\n",
		"вставки": "when start\n  print " + strings.Repeat("\"{", 100000) + "\n",
		"отступ":  "when start\n" + strings.Repeat(" ", 1000000) + "hide\n",
		"лесенка": func() string {
			var b strings.Builder
			for i := 0; i < 2000; i++ {
				b.WriteString(strings.Repeat("  ", i) + "if a\n")
			}
			return b.String()
		}(),
		"минусы":      "when start\n  print " + strings.Repeat("-", 200000) + "1\n",
		"not":         "when start\n  print " + strings.Repeat("not ", 100000) + "a\n",
		"много_строк": strings.Repeat("when start\n  hide\n", 50000),
	}
	for name, src := range inputs {
		start := time.Now()
		Parse(src)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: слишком долго: %v", name, d)
		}
	}
}
