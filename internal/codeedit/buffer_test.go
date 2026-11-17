package codeedit

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Разметка в тестах: '|' — курсор, '«' — якорь выделения.
const (
	curMark = '|'
	ancMark = '«'
)

func mk(t testing.TB, marked string) *Buffer {
	t.Helper()
	var text []rune
	line, col := 0, 0
	var cur, anc Pos
	hasCur, hasAnc := false, false
	for _, r := range marked {
		switch r {
		case curMark:
			cur, hasCur = Pos{line, col}, true
		case ancMark:
			anc, hasAnc = Pos{line, col}, true
		case '\n':
			text = append(text, r)
			line, col = line+1, 0
		default:
			text = append(text, r)
			col++
		}
	}
	b := New(string(text))
	if !hasCur {
		cur = Pos{}
	}
	if hasAnc {
		b.SetSelection(anc, cur)
	} else {
		b.SetCursor(cur.Line, cur.Col, false)
	}
	return b
}

func show(b *Buffer) string {
	cur := b.Cursor()
	anc, hasAnc := b.Anchor(), b.HasSelection()
	var sb strings.Builder
	for i := 0; i < b.Lines(); i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		for c, r := range append([]rune(b.Line(i)), 0) {
			if hasAnc && anc == (Pos{i, c}) {
				sb.WriteRune(ancMark)
			}
			if cur == (Pos{i, c}) {
				sb.WriteRune(curMark)
			}
			if r != 0 {
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
}

func check(t testing.TB, b *Buffer) {
	t.Helper()
	if len(b.lines) == 0 || len(b.lines) > MaxLines {
		t.Fatalf("bad line count %d", len(b.lines))
	}
	for i, l := range b.lines {
		if len(l) > MaxLineLen {
			t.Fatalf("line %d too long", i)
		}
		for _, r := range l {
			if r == '\n' || r == '\t' || r == '\r' {
				t.Fatalf("bad rune %q in line %d", r, i)
			}
		}
	}
	txt := b.Text()
	if !utf8.ValidString(txt) {
		t.Fatalf("invalid utf8")
	}
	if len(txt) != b.bytes || b.bytes > MaxBytes {
		t.Fatalf("bytes accounting: %d vs %d", len(txt), b.bytes)
	}
	in := func(p Pos) bool {
		return p.Line >= 0 && p.Line < len(b.lines) && p.Col >= 0 && p.Col <= len(b.lines[p.Line])
	}
	if !in(b.cur) || !in(b.anchor) {
		t.Fatalf("cursor/anchor out of range: %v %v", b.cur, b.anchor)
	}
}

type opTest struct {
	name string
	in   string
	op   func(b *Buffer)
	want string
}

func runOps(t *testing.T, tests []opTest) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := mk(t, tc.in)
			tc.op(b)
			check(t, b)
			if got := show(b); got != tc.want {
				t.Fatalf("got\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestInsert(t *testing.T) {
	ins := func(s string) func(*Buffer) { return func(b *Buffer) { b.Insert(s) } }
	runOps(t, []opTest{
		{"simple", "ab|c", ins("X"), "abX|c"},
		{"multiline", "a|b", ins("1\n2\n3"), "a1\n2\n3|b"},
		{"replace selection", "a«bcd|e", ins("X"), "aX|e"},
		{"replace backwards sel", "a|bcd«e", ins("X"), "aX|e"},
		{"tab", "|", ins("\tx"), "  x|"},
		{"crlf", "|", ins("a\r\nb\rc"), "a\nb\nc|"},
		{"russian", "при|вет", ins("ё"), "приё|вет"},
		{"control dropped", "|", ins("a\x00\x07b"), "ab|"},
		{"empty", "a|", ins(""), "a|"},
		{"invalid utf8", "|", ins("a\xffb"), "a�b|"},
	})
}

func TestEnterAutoIndent(t *testing.T) {
	enter := func(b *Buffer) { b.Enter() }
	runOps(t, []opTest{
		{"after when", "when start|", enter, "when start\n  |"},
		{"after if", "if x|", enter, "if x\n  |"},
		{"after else", "else|", enter, "else\n  |"},
		{"after else if", "else if y|", enter, "else if y\n  |"},
		{"after repeat", "repeat 3|", enter, "repeat 3\n  |"},
		{"after while", "while a < 3|", enter, "while a < 3\n  |"},
		{"after for", "for i in list|", enter, "for i in list\n  |"},
		{"after to", "to jump|", enter, "to jump\n  |"},
		{"nested", "  if x|", enter, "  if x\n    |"},
		{"inherits", "    say hi|", enter, "    say hi\n    |"},
		{"empty line", "|", enter, "\n|"},
		{"empty indented", "  |", enter, "\n  |"},
		{"comment at end", "if x # note|", enter, "if x # note\n  |"},
		{"comment with keyword", "# if x|", enter, "# if x\n|"},
		{"hash in string", "say \"#\"|", enter, "say \"#\"\n|"},
		{"not keyword prefix", "iffy|", enter, "iffy\n|"},
		{"inside line", "  say |hi", enter, "  say \n  |hi"},
		{"inside block header", "if |x", enter, "if \n  |x"},
		{"before keyword", "|if x", enter, "\n|if x"},
		{"cursor in indent", "    |foo", enter, "\n    |foo"},
		{"replaces selection", "if x«abc|", enter, "if x\n  |"},
		{"russian comment", "когда x # заметка|", enter, "когда x # заметка\n|"},
	})
}

func TestBackspaceDelete(t *testing.T) {
	bs := func(b *Buffer) { b.Backspace() }
	del := func(b *Buffer) { b.Delete() }
	runOps(t, []opTest{
		{"char", "ab|c", bs, "a|c"},
		{"start of text", "|abc", bs, "|abc"},
		{"join lines", "ab\n|cd", bs, "ab|cd"},
		{"indent 2", "  |x", bs, "|x"},
		{"indent 4", "    |x", bs, "  |x"},
		{"indent 3", "   |x", bs, "  |x"},
		{"indent 1", " |x", bs, "|x"},
		{"spaces after text", "a   |", bs, "a  |"},
		{"selection", "a«bc|d", bs, "a|d"},
		{"russian", "при|вет", bs, "пр|вет"},
		{"delete char", "a|bc", del, "a|c"},
		{"delete join", "ab|\ncd", del, "ab|cd"},
		{"delete end of text", "ab|", del, "ab|"},
		{"delete selection", "«ab|c", del, "|c"},
		{"word back", "hello wor|ld", func(b *Buffer) { b.DeleteWordBack() }, "hello |ld"},
		{"word back spaces", "hello   |", func(b *Buffer) { b.DeleteWordBack() }, "|"},
		{"word back join", "a\n|b", func(b *Buffer) { b.DeleteWordBack() }, "a|b"},
		{"word back russian", "привет мир|", func(b *Buffer) { b.DeleteWordBack() }, "привет |"},
		{"word fwd", "|hello world", func(b *Buffer) { b.DeleteWordForward() }, "|world"},
		{"word fwd join", "a|\nb", func(b *Buffer) { b.DeleteWordForward() }, "a|b"},
	})
}

func TestTabs(t *testing.T) {
	tab := func(b *Buffer) { b.Tab() }
	stab := func(b *Buffer) { b.ShiftTab() }
	runOps(t, []opTest{
		{"tab at 0", "|a", tab, "  |a"},
		{"tab at 1", "a|b", tab, "a |b"},
		{"tab at 2", "ab|c", tab, "ab  |c"},
		{"tab replaces single-line selection", "a«bc|d", tab, "a |d"},
		{"tab multi", "«a\nb|", tab, "«  a\n  b|"},
		{"tab multi skips empty", "«a\n\nb|", tab, "«  a\n\n  b|"},
		{"tab multi end col0", "«a\nb\n|c", tab, "«  a\n  b\n|c"},
		{"shift tab", "    a|", stab, "  a|"},
		{"shift tab odd", " a|", stab, "a|"},
		{"shift tab none", "a|", stab, "a|"},
		{"shift tab cursor in indent", "  |a", stab, "|a"},
		{"shift tab multi", "«  a\n    b|", stab, "«a\n  b|"},
	})
}

func TestMovement(t *testing.T) {
	m := func(f func(*Buffer, bool), ext bool) func(*Buffer) { return func(b *Buffer) { f(b, ext) } }
	runOps(t, []opTest{
		{"left", "a|b", m((*Buffer).MoveLeft, false), "|ab"},
		{"left wrap", "a\n|b", m((*Buffer).MoveLeft, false), "a|\nb"},
		{"left at start", "|a", m((*Buffer).MoveLeft, false), "|a"},
		{"left collapses sel", "a«bc|d", m((*Buffer).MoveLeft, false), "a|bcd"},
		{"right collapses sel", "a|bc«d", m((*Buffer).MoveRight, false), "abc|d"},
		{"right wrap", "a|\nb", m((*Buffer).MoveRight, false), "a\n|b"},
		{"right at end", "a|", m((*Buffer).MoveRight, false), "a|"},
		{"shift right", "a|bc", m((*Buffer).MoveRight, true), "a«b|c"},
		{"shift left extends", "a«bc|d", m((*Buffer).MoveLeft, true), "a«b|cd"},
		{"up keeps col", "abcd\nx\nabcd|", func(b *Buffer) { b.MoveUp(false); b.MoveUp(false) }, "abcd|\nx\nabcd"},
		{"up short line", "abcd|\nx", func(b *Buffer) { b.MoveDown(false) }, "abcd\nx|"},
		{"down wants col", "abcd|\nx\nabcd", func(b *Buffer) { b.MoveDown(false); b.MoveDown(false) }, "abcd\nx\nabcd|"},
		{"up at top", "ab|c", m((*Buffer).MoveUp, false), "|abc"},
		{"down at bottom", "a|bc", m((*Buffer).MoveDown, false), "abc|"},
		{"word right", "|hello  world", m((*Buffer).MoveWordRight, false), "hello  |world"},
		{"word right punct", "|a.b", m((*Buffer).MoveWordRight, false), "a|.b"},
		{"word right eol", "a|\nb", m((*Buffer).MoveWordRight, false), "a\n|b"},
		{"word left", "hello  world|", m((*Buffer).MoveWordLeft, false), "hello  |world"},
		{"word left russian", "привет мир|", m((*Buffer).MoveWordLeft, false), "привет |мир"},
		{"word left start of line", "a\n|b", m((*Buffer).MoveWordLeft, false), "a|\nb"},
		{"home smart", "  ab|c", m((*Buffer).MoveHome, false), "  |abc"},
		{"home to 0", "  |abc", m((*Buffer).MoveHome, false), "|  abc"},
		{"home from 0", "|  abc", m((*Buffer).MoveHome, false), "  |abc"},
		{"home blank line", "  |", m((*Buffer).MoveHome, false), "|  "},
		{"end", "a|bc", m((*Buffer).MoveEnd, false), "abc|"},
		{"text start", "a\nb|", m((*Buffer).MoveTextStart, false), "|a\nb"},
		{"text end", "|a\nb", m((*Buffer).MoveTextEnd, false), "a\nb|"},
		{"page down", "a|\nb\nc\nd", func(b *Buffer) { b.MovePageDown(2, false) }, "a\nb\nc|\nd"},
		{"page down clamp", "a|\nb", func(b *Buffer) { b.MovePageDown(10, false) }, "a\nb|"},
		{"page up", "a\nb\nc|", func(b *Buffer) { b.MovePageUp(2, false) }, "a|\nb\nc"},
		{"page n<1", "a|\nb", func(b *Buffer) { b.MovePageDown(0, false) }, "a\nb|"},
		{"select all", "a|b\nc", func(b *Buffer) { b.SelectAll() }, "«ab\nc|"},
		{"select word", "ab cd|", func(b *Buffer) { b.SelectWord(0, 4) }, "ab «cd|"},
		{"select word at end", "ab cd|", func(b *Buffer) { b.SelectWord(0, 5) }, "ab «cd|"},
		{"select word space", "ab   cd|", func(b *Buffer) { b.SelectWord(0, 3) }, "ab«   |cd"},
		{"select word russian", "|привет мир", func(b *Buffer) { b.SelectWord(0, 2) }, "«привет| мир"},
		{"select line", "a\nb|\nc", func(b *Buffer) { b.SelectLine(1) }, "a\n«b\n|c"},
		{"select last line", "a\nb|", func(b *Buffer) { b.SelectLine(1) }, "a\n«b|"},
		{"set cursor clamps", "ab|", func(b *Buffer) { b.SetCursor(99, 99, false) }, "ab|"},
		{"set cursor neg", "ab|", func(b *Buffer) { b.SetCursor(-5, -5, false) }, "|ab"},
	})
}

func TestLineOps(t *testing.T) {
	runOps(t, []opTest{
		{"dup", "a|b\nc", func(b *Buffer) { b.DuplicateLines() }, "ab\na|b\nc"},
		{"dup last", "a\nb|", func(b *Buffer) { b.DuplicateLines() }, "a\nb\nb|"},
		{"dup sel", "«a\nb|\nc", func(b *Buffer) { b.DuplicateLines() }, "a\nb\n«a\nb|\nc"},
		{"delete mid", "a\nb|\nc", func(b *Buffer) { b.DeleteLines() }, "a\nc|"},
		{"delete last", "a\nb|", func(b *Buffer) { b.DeleteLines() }, "a|"},
		{"delete only", "ab|", func(b *Buffer) { b.DeleteLines() }, "|"},
		{"delete sel", "«a\nb|\nc", func(b *Buffer) { b.DeleteLines() }, "c|"},
		{"move up", "a\nb|", func(b *Buffer) { b.MoveLinesUp() }, "b|\na"},
		{"move up top", "a|\nb", func(b *Buffer) { b.MoveLinesUp() }, "a|\nb"},
		{"move down", "a|\nb\nc", func(b *Buffer) { b.MoveLinesDown() }, "b\na|\nc"},
		{"move down bottom", "a\nb|", func(b *Buffer) { b.MoveLinesDown() }, "a\nb|"},
		{"move block down", "«a\nb|\nc\nd", func(b *Buffer) { b.MoveLinesDown() }, "c\n«a\nb|\nd"},
		{"move block up", "a\n«b\nc|\nd", func(b *Buffer) { b.MoveLinesUp() }, "«b\nc|\na\nd"},
		{"comment", "  a|b", func(b *Buffer) { b.ToggleComment() }, "  # a|b"},
		{"uncomment", "  # a|b", func(b *Buffer) { b.ToggleComment() }, "  a|b"},
		{"uncomment no space", "#a|b", func(b *Buffer) { b.ToggleComment() }, "a|b"},
		{"comment block", "«  a\n\n    b|", func(b *Buffer) { b.ToggleComment() }, "«  # a\n\n  #   b|"},
		{"uncomment block", "«# a\n# b|", func(b *Buffer) { b.ToggleComment() }, "«a\nb|"},
		{"comment mixed", "«# a\nb|", func(b *Buffer) { b.ToggleComment() }, "«# # a\n# b|"},
		{"comment empty line", "|", func(b *Buffer) { b.ToggleComment() }, "# |"},
	})
}

func TestClipboard(t *testing.T) {
	b := mk(t, "ab«cd|ef")
	if got := b.Copy(); got != "cd" || b.Text() != "abcdef" {
		t.Fatalf("copy %q %q", got, b.Text())
	}
	if got := b.Cut(); got != "cd" || b.Text() != "abef" {
		t.Fatalf("cut %q %q", got, b.Text())
	}
	b.Paste("X\r\nY\t")
	if b.Text() != "abX\nY  ef" {
		t.Fatalf("paste %q", b.Text())
	}
	if b.Cut() != "" {
		t.Fatal("cut without selection")
	}
	b.SelectAll()
	if b.Selected() != b.Text() {
		t.Fatal("selected all")
	}
}

func TestUndoRedo(t *testing.T) {
	b := New("")
	for _, r := range "hello" {
		b.Insert(string(r))
	}
	b.Insert(" ")
	for _, r := range "мир" {
		b.Insert(string(r))
	}
	// "hello" | " мир" — два шага (пробел после слова начинает новый шаг).
	if !b.Undo() || b.Text() != "hello" {
		t.Fatalf("undo1 %q", b.Text())
	}
	if !b.Undo() || b.Text() != "" {
		t.Fatalf("undo2 %q", b.Text())
	}
	if b.Undo() {
		t.Fatal("undo on empty history")
	}
	for i := 0; i < 2; i++ {
		if !b.Redo() {
			t.Fatal("redo")
		}
	}
	if b.Text() != "hello мир" || b.Cursor() != (Pos{0, 9}) {
		t.Fatalf("redo result %q %v", b.Text(), b.Cursor())
	}
	if b.Redo() {
		t.Fatal("redo past end")
	}

	// Курсор и выделение возвращаются.
	b = mk(t, "ab«cd|ef")
	b.Insert("X")
	if show(b) != "abX|ef" {
		t.Fatal(show(b))
	}
	b.Undo()
	if show(b) != "ab«cd|ef" {
		t.Fatalf("undo sel: %s", show(b))
	}
	b.Redo()
	if show(b) != "abX|ef" {
		t.Fatalf("redo sel: %s", show(b))
	}

	// Новое действие очищает redo.
	b.Undo()
	b.Insert("Z")
	if b.CanRedo() {
		t.Fatal("redo not cleared")
	}

	// Движение курсора прерывает склейку.
	b = New("")
	b.Insert("a")
	b.Insert("b")
	b.MoveLeft(false)
	b.MoveRight(false)
	b.Insert("c")
	b.Undo()
	if b.Text() != "ab" {
		t.Fatalf("merge break: %q", b.Text())
	}
	b.Undo()
	if b.Text() != "" {
		t.Fatalf("merge: %q", b.Text())
	}

	// Многошаговые операции откатываются целиком.
	b = mk(t, "«a\nb|\nc")
	b.Tab()
	b.ToggleComment()
	b.MoveLinesDown()
	b.Undo()
	b.Undo()
	if show(b) != "«  a\n  b|\nc" {
		t.Fatalf("multi undo: %q", show(b))
	}
	b.Undo()
	if show(b) != "«a\nb|\nc" {
		t.Fatalf("multi undo: %q", show(b))
	}

	// Backspace подряд склеивается.
	b = mk(t, "abc|")
	b.Backspace()
	b.Backspace()
	b.Undo()
	if b.Text() != "abc" {
		t.Fatalf("back merge %q", b.Text())
	}

	// SetText сбрасывает историю.
	b.SetText("x")
	if b.CanUndo() || b.CanRedo() {
		t.Fatal("history not reset")
	}
}

func TestUndoLimit(t *testing.T) {
	b := New("")
	for i := 0; i < 2000; i++ {
		b.Enter()
	}
	n := 0
	for b.Undo() {
		n++
	}
	if n != maxUndoSteps {
		t.Fatalf("steps %d", n)
	}
	b = New("")
	big := strings.Repeat("x", 300_000)
	b.Paste(big)
	b.Paste(big)
	b.Paste(big)
	if len(b.undo) < 1 || b.undoCost > maxUndoCost && len(b.undo) > 1 {
		t.Fatalf("cost limit: %d steps %d cost", len(b.undo), b.undoCost)
	}
}

func TestLimits(t *testing.T) {
	// Строки.
	b := New(strings.Repeat("a\n", MaxLines+500))
	if b.Lines() != MaxLines || !b.Truncated() {
		t.Fatalf("lines %d trunc %v", b.Lines(), b.Truncated())
	}
	check(t, b)
	b.Insert("\n")
	if b.Lines() != MaxLines {
		t.Fatal("insert beyond lines limit")
	}
	b.DuplicateLines()
	if b.Lines() != MaxLines {
		t.Fatal("dup beyond lines limit")
	}
	check(t, b)

	// Длина строки: лишнее отбрасывается до конца строки.
	b = New(strings.Repeat("я", MaxLineLen+50) + "\nok")
	if b.LineLen(0) != MaxLineLen || b.Line(1) != "ok" || !b.Truncated() {
		t.Fatalf("line len %d %q", b.LineLen(0), b.Line(1))
	}
	check(t, b)
	b.SetCursor(0, 5, false)
	b.Insert("zzz")
	if b.LineLen(0) != MaxLineLen || b.Line(0)[:10] == "" {
		t.Fatal("insert in full line")
	}
	b.Tab()
	b.ToggleComment()
	check(t, b)

	// Объём: ~1 МБ.
	line := strings.Repeat("ж", 5000) + "\n"
	b = New(strings.Repeat(line, 400))
	check(t, b)
	if !b.Truncated() || b.Size() > MaxBytes {
		t.Fatalf("bytes: %d trunc=%v", b.Size(), b.Truncated())
	}
	before := b.Text()
	b.Paste("x")
	if b.Text() != before && b.Size() > MaxBytes {
		t.Fatal("over limit")
	}
	check(t, b)

	// Вставка не должна удалять выделение, если всё отвергнуто.
	b = New(strings.Repeat("a", MaxLineLen))
	b.SetSelection(Pos{0, 0}, Pos{0, 0})
	b.SetCursor(0, MaxLineLen, false)
	b.Insert("b")
	if b.LineLen(0) != MaxLineLen || b.Version() == 0 {
		t.Fatal("full line")
	}
}

func TestCRLFAndSetText(t *testing.T) {
	b := New("a\r\nb\rc\n\td")
	if b.Text() != "a\nb\nc\n  d" || b.Lines() != 4 {
		t.Fatalf("%q", b.Text())
	}
	v := b.Version()
	b.SetText("")
	if b.Version() <= v || b.Lines() != 1 || b.Text() != "" {
		t.Fatal("settext")
	}
	var z Buffer // нулевое значение
	z.Insert("hi")
	z.Enter()
	if z.Text() != "hi\n" {
		t.Fatalf("zero value: %q", z.Text())
	}
	var z2 Buffer
	if z2.Undo() || z2.Line(5) != "" || z2.Lines() != 1 {
		t.Fatal("zero value reads")
	}
}

func TestVersion(t *testing.T) {
	b := New("a")
	v := b.Version()
	b.MoveEnd(false)
	if b.Version() != v {
		t.Fatal("move changes version")
	}
	b.Insert("b")
	if b.Version() <= v {
		t.Fatal("insert must bump version")
	}
	v = b.Version()
	b.Undo()
	if b.Version() <= v {
		t.Fatal("undo must bump version")
	}
}

func TestOffsets(t *testing.T) {
	b := New("ab\nпривет\n")
	cases := []struct {
		p   Pos
		off int
	}{{Pos{0, 0}, 0}, {Pos{0, 2}, 2}, {Pos{1, 0}, 3}, {Pos{1, 6}, 9}, {Pos{2, 0}, 10}}
	for _, c := range cases {
		if got := b.Offset(c.p); got != c.off {
			t.Errorf("Offset(%v)=%d want %d", c.p, got, c.off)
		}
		if got := b.PosAt(c.off); got != c.p {
			t.Errorf("PosAt(%d)=%v want %v", c.off, got, c.p)
		}
	}
	if b.Len() != 10 || b.PosAt(999) != (Pos{2, 0}) || b.PosAt(-3) != (Pos{}) {
		t.Fatal("clamping")
	}
	if b.Offset(Pos{-1, 5}) != 0 || b.Offset(Pos{99, 99}) != 10 {
		t.Fatal("offset clamp")
	}
}

func TestFind(t *testing.T) {
	b := New("Hello world\nхорошо Мир\nhello again")
	cases := []struct {
		name       string
		q          string
		l, c       int
		fwd, mcase bool
		wl, wc     int
		wantOK     bool
	}{
		{"fwd from start", "hello", 0, 0, true, false, 0, 0, true},
		{"fwd next", "hello", 0, 1, true, false, 2, 0, true},
		{"fwd case", "hello", 0, 0, true, true, 2, 0, true},
		{"fwd wrap", "world", 1, 0, true, false, 0, 6, true},
		{"fwd russian nocase", "мир", 0, 0, true, false, 1, 7, true},
		{"fwd russian case", "мир", 0, 0, true, true, 0, 0, false},
		{"back", "hello", 2, 0, false, false, 0, 0, true},
		{"back same line", "o", 0, 5, false, false, 0, 4, true},
		{"back wrap", "again", 0, 0, false, false, 2, 6, true},
		{"missing", "zzz", 0, 0, true, false, 0, 0, false},
		{"empty", "", 0, 0, true, false, 0, 0, false},
		{"multiline", "a\nb", 0, 0, true, false, 0, 0, false},
		{"out of range", "hello", 99, 99, true, false, 0, 0, true},
		{"fwd wrap onto itself", "again", 2, 7, true, false, 2, 6, true},
	}
	for _, tc := range cases {
		l, c, ok := b.Find(tc.q, tc.l, tc.c, tc.fwd, tc.mcase)
		if ok != tc.wantOK || (ok && (l != tc.wl || c != tc.wc)) {
			t.Errorf("%s: got %d,%d,%v want %d,%d,%v", tc.name, l, c, ok, tc.wl, tc.wc, tc.wantOK)
		}
	}
}

func TestNoPanicOnBadInput(t *testing.T) {
	b := New("")
	b.SetCursor(-1, -1, true)
	b.SetSelection(Pos{9, 9}, Pos{-9, -9})
	b.SelectWord(5, 5)
	b.SelectLine(-3)
	b.Backspace()
	b.Delete()
	b.DeleteWordBack()
	b.DeleteWordForward()
	b.DeleteLines()
	b.MoveLinesUp()
	b.MoveLinesDown()
	b.DuplicateLines()
	b.ToggleComment()
	b.ShiftTab()
	b.Tab()
	b.Enter()
	b.MovePageUp(-5, true)
	b.Find("x", -4, 99, false, false)
	b.Undo()
	b.Redo()
	check(t, b)
}
