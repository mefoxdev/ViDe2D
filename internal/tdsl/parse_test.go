package tdsl

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExamples разбирает программы из testdata (копии примеров из документации).
func TestExamples(t *testing.T) {
	files, err := filepath.Glob("testdata/*.tdsl")
	if err != nil || len(files) == 0 {
		t.Fatalf("нет файлов testdata: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			script, errs := Parse(string(src))
			for _, e := range errs {
				t.Errorf("%s", e.Format(string(src)))
			}
			if len(errs) == 0 && len(script.Items) == 0 {
				t.Error("пустое дерево")
			}
		})
	}
}
