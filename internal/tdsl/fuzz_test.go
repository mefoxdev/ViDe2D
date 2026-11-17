package tdsl

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse проверяет, что Parse не паникует, не зацикливается и возвращает
// осмысленные ошибки на любом входе.
func FuzzParse(f *testing.F) {
	files, _ := filepath.Glob("testdata/*.tdsl")
	for _, name := range files {
		if src, err := os.ReadFile(name); err == nil {
			f.Add(string(src))
		}
	}
	for _, seed := range []string{
		"", "\t", "when start\n\tprint 1", "when start\n   x = [1,\n", "print \"{\"{\"{",
		"when start\n  print \"ab {x +} cd\"\n", "when key", "if", "\"", "(((((", "var a = [\n  1,\n",
		"when start\r\n  slide to 1, 2 in 3 seconds\r\n", "to f(\n", "x = 1 < 2 < 3",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		script, errs := Parse(src)
		if script == nil {
			t.Fatal("Script == nil")
		}
		if len(errs) > maxErrors {
			t.Fatalf("слишком много ошибок: %d", len(errs))
		}
		for _, e := range errs {
			if e.Line < 1 || e.Col < 1 || e.EndCol <= e.Col || e.Message == "" || e.Code == "" {
				t.Fatalf("некорректная ошибка: %+v", e)
			}
			_ = e.Format(src)
		}
	})
}
