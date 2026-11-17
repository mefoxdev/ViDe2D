package codeedit

import "unicode"

func fold(r rune) rune { return unicode.ToLower(r) }

func matchAt(l, q []rune, i int, matchCase bool) bool {
	if i < 0 || i+len(q) > len(l) {
		return false
	}
	for k, r := range q {
		c := l[i+k]
		if c != r && (matchCase || fold(c) != fold(r)) {
			return false
		}
	}
	return true
}

// Find ищет подстроку query (без регулярных выражений, в пределах одной
// строки; query с переводом строки или пустой — не находится).
// forward=true: первое совпадение, начинающееся в позиции >= (fromLine,
// fromCol); forward=false: последнее совпадение, начинающееся строго
// раньше этой позиции. Поиск зацикливается по тексту. matchCase=false —
// без учёта регистра. Возвращает позицию начала совпадения.
func (b *Buffer) Find(query string, fromLine, fromCol int, forward, matchCase bool) (line, col int, ok bool) {
	b.ensure()
	q := []rune(query)
	if len(q) == 0 {
		return 0, 0, false
	}
	for _, r := range q {
		if r == '\n' || r == '\r' {
			return 0, 0, false
		}
	}
	from := b.clamp(Pos{fromLine, fromCol})
	n := len(b.lines)
	if forward {
		// Строки from.Line (с колонки), следующие, затем с начала (включая from.Line целиком).
		for k := 0; k <= n; k++ {
			l := (from.Line + k) % n
			start, stop := 0, len(b.lines[l])
			if k == 0 {
				start = from.Col
			}
			if k == n {
				stop = from.Col - 1 // добираем начало исходной строки
			}
			for i := start; i <= stop; i++ {
				if matchAt(b.lines[l], q, i, matchCase) {
					return l, i, true
				}
			}
		}
		return 0, 0, false
	}
	for k := 0; k <= n; k++ {
		l := ((from.Line-k)%n + n) % n
		start, stop := len(b.lines[l])-len(q), 0
		if k == 0 {
			start = min(start, from.Col-1)
		}
		if k == n {
			stop = from.Col // добираем конец исходной строки
		}
		for i := start; i >= stop; i-- {
			if matchAt(b.lines[l], q, i, matchCase) {
				return l, i, true
			}
		}
	}
	return 0, 0, false
}
