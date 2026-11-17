package codeedit

import "testing"

var fuzzSnippets = []string{
	"a", "я", " ", "\n", "\t", "\r\n", "if x", "when start\n", "  ", "# ", "else if y\n",
	"привет мир", "\x00", "\xff", "строка\nвторая\n  третья", "end ", "}", "\r",
}

// applyFuzzOp выполняет одну операцию, выбранную байтом op с параметром arg.
func applyFuzzOp(b *Buffer, op, arg byte) {
	ext := arg&1 == 1
	switch op % 40 {
	case 0, 1, 2:
		b.Insert(fuzzSnippets[int(arg)%len(fuzzSnippets)])
	case 3:
		b.Paste(fuzzSnippets[int(arg)%len(fuzzSnippets)])
	case 4:
		b.Enter()
	case 5:
		b.Backspace()
	case 6:
		b.Delete()
	case 7:
		b.DeleteWordBack()
	case 8:
		b.DeleteWordForward()
	case 9:
		b.Tab()
	case 10:
		b.ShiftTab()
	case 11:
		b.MoveLeft(ext)
	case 12:
		b.MoveRight(ext)
	case 13:
		b.MoveUp(ext)
	case 14:
		b.MoveDown(ext)
	case 15:
		b.MoveWordLeft(ext)
	case 16:
		b.MoveWordRight(ext)
	case 17:
		b.MoveHome(ext)
	case 18:
		b.MoveEnd(ext)
	case 19:
		b.MoveTextStart(ext)
	case 20:
		b.MoveTextEnd(ext)
	case 21:
		b.MovePageUp(int(arg)%5, ext)
	case 22:
		b.MovePageDown(int(arg)%5, ext)
	case 23:
		b.SelectAll()
	case 24:
		b.SelectWord(int(arg)%5-1, int(arg)%9-1)
	case 25:
		b.SelectLine(int(arg)%5 - 1)
	case 26:
		b.DuplicateLines()
	case 27:
		b.DeleteLines()
	case 28:
		b.MoveLinesUp()
	case 29:
		b.MoveLinesDown()
	case 30:
		b.ToggleComment()
	case 31:
		b.Cut()
	case 32:
		b.Copy()
	case 33:
		b.SetCursor(int(arg)%6-1, int(arg)%11-1, ext)
	case 34:
		b.SetSelection(Pos{int(arg)%4 - 1, int(arg) % 7}, Pos{int(arg) % 5, int(arg) % 3})
	case 35:
		b.Find("a", int(arg)%4, int(arg)%6, ext, arg&2 == 2)
	case 36:
		b.ClampCursor()
	case 37:
		b.DeleteSelection()
	case 38:
		b.Insert("x" + fuzzSnippets[int(arg)%len(fuzzSnippets)])
	case 39:
		b.Insert("é")
	}
}

func FuzzBuffer(f *testing.F) {
	f.Add("when start\n  say hi\n", []byte{0, 1, 4, 4, 9, 30, 28, 5, 6, 12, 33, 26})
	f.Add("привет\tмир\r\nstrока", []byte{23, 31, 3, 7, 8, 15, 16, 24, 30, 30, 27})
	f.Add("", []byte{4, 4, 4, 5, 5, 5, 26, 27, 29, 28})
	f.Fuzz(func(t *testing.T, initial string, ops []byte) {
		if len(ops) > 400 {
			ops = ops[:400]
		}
		b := New(initial)
		check(t, b)
		base := b.Text()
		prevVersion := b.Version()
		for i := 0; i+1 < len(ops); i += 2 {
			before := b.Text()
			applyFuzzOp(b, ops[i], ops[i+1])
			check(t, b)
			if b.Version() < prevVersion {
				t.Fatal("version decreased")
			}
			if b.Text() != before && b.Version() == prevVersion {
				t.Fatalf("text changed without version bump (op %d)", ops[i]%40)
			}
			prevVersion = b.Version()
		}
		final := b.Text()
		n := 0
		for b.Undo() {
			n++
			check(t, b)
		}
		if b.Text() != base && !b.trimmed {
			t.Fatalf("undo-all mismatch (%d steps): %q != %q", n, b.Text(), base)
		}
		for b.Redo() {
			check(t, b)
		}
		if b.Text() != final {
			t.Fatalf("redo-all mismatch: %q != %q", b.Text(), final)
		}
	})
}
