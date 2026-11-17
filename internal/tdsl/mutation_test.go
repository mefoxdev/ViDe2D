package tdsl

import (
	"os"
	"path/filepath"
	"testing"
)

// lcg — детерминированный генератор псевдослучайных чисел (результаты воспроизводимы).
type lcg uint64

func (g *lcg) next(n int) int {
	*g = *g*6364136223846793005 + 1442695040888963407
	return int((uint64(*g) >> 33) % uint64(n))
}

var mutationInserts = []string{
	"\"", "(", ")", "[", "]", "{", "}", "@", "$", ",", ".", "-", "+", "=", "#", "\\", "\t", "\n", "  ",
	"é", "я", "\u00a0", "\u200b", "\ufeff", "\u2028", "😀", "\x00", "round", "round(", "is", "not", "touching(",
	"\"{", "}\"", "1+", "0.50", "move ", "when ", " by ",
}

// mutate вносит 1–3 случайные правки в src: удаление, вставка, дублирование и перестановка.
func mutate(g *lcg, src string) string {
	r := []rune(src)
	for k := 1 + g.next(3); k > 0; k-- {
		if len(r) == 0 {
			r = []rune(mutationInserts[g.next(len(mutationInserts))])
			continue
		}
		at := g.next(len(r) + 1)
		switch g.next(5) {
		case 0, 1: // удалить 1–4 символа
			if at >= len(r) {
				at = len(r) - 1
			}
			end := min(len(r), at+1+g.next(4))
			r = append(r[:at:at], r[end:]...)
		case 2, 3: // вставить фрагмент
			ins := []rune(mutationInserts[g.next(len(mutationInserts))])
			r = append(r[:at:at], append(ins, r[at:]...)...)
		default: // продублировать кусок
			end := min(len(r), at+g.next(40))
			chunk := append([]rune(nil), r[at:end]...)
			r = append(r[:end:end], append(chunk, r[end:]...)...)
		}
	}
	return string(r)
}

// TestMutations: ~500 мутаций файлов testdata проходят через Parse, Check, Highlight и
// Complete без паник и зависаний.
func TestMutations(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.tdsl")
	if len(files) == 0 {
		t.Fatal("нет файлов testdata")
	}
	g := lcg(12345)
	total := 0
	for round := 0; total < 500; round++ {
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src := mutate(&g, string(b))
			total++
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("паника на мутации %d файла %s: %v\n%q", total, f, r, src)
					}
				}()
				script, errs := Parse(src)
				for _, e := range errs {
					if e.Line < 1 || e.Col < 1 || e.EndCol <= e.Col {
						t.Fatalf("некорректная ошибка %+v\n%q", e, src)
					}
					_ = e.Format(src)
				}
				for _, e := range Check(script, projEnv) {
					_ = e.Format(src)
				}
				_ = Check(script, nil)
				_ = Highlight(src)
				lines := 1
				for _, c := range src {
					if c == '\n' {
						lines++
					}
				}
				for i := 0; i < 4; i++ {
					_ = Complete(src, g.next(lines), g.next(60), cmEnv)
				}
				_ = Complete(src, 0, 0, nil)
				_ = Complete(src, lines-1, 1000, cmEnv)
			}()
		}
	}
	t.Logf("мутаций: %d", total)
}
