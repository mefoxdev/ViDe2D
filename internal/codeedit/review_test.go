package codeedit

import (
	"strings"
	"testing"
)

// Enter на пределе MaxLines не должен менять текст (раньше стирал пробелы слева от курсора).
func TestEnterAtMaxLinesKeepsText(t *testing.T) {
	text := strings.Repeat("a\n", MaxLines-1) + "  x"
	b := New(text)
	if b.Lines() != MaxLines {
		t.Fatalf("строк %d, ожидалось %d", b.Lines(), MaxLines)
	}
	b.SetCursor(MaxLines-1, 2, false)
	v := b.Version()
	b.Enter()
	if b.Text() != text {
		t.Errorf("текст изменился: последняя строка %q", b.Line(b.Lines()-1))
	}
	if b.Version() != v || b.CanUndo() {
		t.Errorf("версия %d -> %d, CanUndo=%v", v, b.Version(), b.CanUndo())
	}
	if !b.Truncated() {
		t.Error("ожидался признак Truncated")
	}
	// с выделением нескольких строк перевод строки допустим: строк станет меньше
	b.SetSelection(Pos{MaxLines - 3, 0}, Pos{MaxLines - 1, 1})
	b.Enter()
	if b.Lines() > MaxLines || b.Lines() != MaxLines-1 {
		t.Errorf("строк %d", b.Lines())
	}
}

func TestSanitizeUnicodeSpaces(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\u00a0b", "a b"},
		{"a\u2003b\u3000c\u202fd", "a b c d"},
		{"\ufeffwhen\u200b start\u200d\u2060", "when start"},
		{"a\u2028b\u2029c\u0085d", "a\nb\nc\nd"},
		{"a\u0090b\u007fc", "abc"},
		{"a\r\nb\rc\td", "a\nb\nc  d"},
	}
	for _, c := range cases {
		if got := string(sanitize(c.in)); got != c.want {
			t.Errorf("sanitize(%q) = %q, ожидалось %q", c.in, got, c.want)
		}
	}
	b := New("\ufeffprint\u00a01")
	if b.Text() != "print 1" {
		t.Errorf("SetText: %q", b.Text())
	}
	b.Paste("\u200b\u00a0x")
	if b.Text() != " xprint 1" {
		t.Errorf("Paste: %q", b.Text())
	}
}

// Один шаг истории не может превышать maxUndoCost: такая история сбрасывается целиком.
func TestHugeStepNotKept(t *testing.T) {
	b := New("start")
	b.Insert("a")
	b.Insert("\n")
	big := strings.Repeat(strings.Repeat("x", 100)+"\n", 6000) // ~600 тыс. рун > maxUndoCost
	b.Paste(big)
	if b.Truncated() || !strings.Contains(b.Text(), big) {
		t.Fatalf("вставка не применилась (truncated=%v)", b.Truncated())
	}
	if b.undoCost > maxUndoCost {
		t.Errorf("undoCost %d > %d", b.undoCost, maxUndoCost)
	}
	if b.CanUndo() {
		t.Error("огромный шаг не должен храниться, а прежние шаги без него применять нельзя")
	}
	// после этого история снова работает
	text := b.Text()
	b.Insert("z")
	if !b.Undo() || b.Text() != text {
		t.Error("Undo после сброса истории")
	}
	// склейка набора символов не должна раздуть шаг сверх предела
	b2 := New("")
	for i := 0; i < 50; i++ {
		b2.Paste(strings.Repeat("y", 9000))
	}
	if b2.undoCost > maxUndoCost {
		t.Errorf("undoCost %d > %d", b2.undoCost, maxUndoCost)
	}
}
