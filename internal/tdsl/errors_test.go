package tdsl

import (
	"strings"
	"testing"
)

// wantError проверяет, что разбор src даёт ровно одну ошибку с заданными полями.
func wantError(t *testing.T, src, code string, line, col, endCol int, msgPart, hint string) *Error {
	t.Helper()
	_, errs := Parse(src)
	if len(errs) != 1 {
		t.Fatalf("ожидалась одна ошибка, получено %d: %v", len(errs), errs)
	}
	e := errs[0]
	if e.Code != code || e.Line != line || e.Col != col || e.EndCol != endCol {
		t.Errorf("получено %s %d:%d-%d, ожидалось %s %d:%d-%d (%s)", e.Code, e.Line, e.Col, e.EndCol, code, line, col, endCol, e.Message)
	}
	if !strings.Contains(e.Message, msgPart) {
		t.Errorf("сообщение %q не содержит %q", e.Message, msgPart)
	}
	if e.Hint != hint {
		t.Errorf("подсказка %q, ожидалась %q", e.Hint, hint)
	}
	return e
}

func TestTD001(t *testing.T) {
	// три пробела
	wantError(t, "when start\n   print 1\n", "TD001", 2, 1, 4, "Неверный отступ: нужно 2 пробела на уровень", "Сейчас пробелов: 3.")
	// четыре пробела: сдвиг сразу на два уровня
	wantError(t, "when start\n    print 1\n", "TD001", 2, 1, 5, "Неверный отступ", "Здесь можно сдвинуться только на 2 пробела относительно предыдущей строки.")
	// лишний отступ там, где блок не открывался
	wantError(t, "var a = 1\n  var b = 2\n", "TD001", 2, 1, 3, "Неверный отступ", "Сдвигать нужно только строки внутри when, to, if, repeat, while и for.")
}

func TestTD002(t *testing.T) {
	wantError(t, "when start\n\tprint 1\n", "TD002", 2, 1, 2, "В отступе табуляция: используйте пробелы", "Замените Tab на 2 пробела на каждый уровень.")
}

func TestTD003(t *testing.T) {
	wantError(t, "when start\n  print \"привет\n", "TD003", 2, 9, 16, "Не закрыта кавычка", "Добавьте « \" » в конце текста.")
}

func TestTD004(t *testing.T) {
	// опечатка в направлении сообщается проверкой (Check): имя может быть переменной
	src := "when start\n  move rigth by 5\n"
	script, perrs := Parse(src)
	if len(perrs) != 0 {
		t.Fatalf("парсер не должен ругаться на направление: %v", perrs)
	}
	errs := Check(script, nil)
	if len(errs) != 1 {
		t.Fatalf("ожидалась одна ошибка, получено %v", errs)
	}
	e := errs[0]
	if e.Code != "TD004" || e.Line != 2 || e.Col != 8 || e.EndCol != 13 ||
		e.Message != "Я не знаю направления «rigth»." || e.Hint != "Может быть, вы хотели «right»?" {
		t.Fatalf("неверная ошибка: %+v", e)
	}
	want := "Проблема в hero.tdsl, строка 2 (TD004):\n\n" +
		"      move rigth by 5\n" +
		"           ^^^^^\n" +
		"Я не знаю направления «rigth». Может быть, вы хотели «right»?\n"
	e.File = "hero.tdsl"
	if got := e.Format(src); got != want {
		t.Errorf("Format:\n%s\nожидалось:\n%s", got, want)
	}
	// опечатка в команде, событии и слове верхнего уровня
	wantError(t, "when start\n  pritn \"x\"\n", "TD004", 2, 3, 8, "Не знаю слова «pritn»", "Может быть, вы хотели «print»?")
	wantError(t, "when strat\n  print 1\n", "TD004", 1, 6, 11, "Не знаю такого события", "Может быть, вы хотели «start»?")
	wantError(t, "When start\n  print 1\n", "TD004", 1, 1, 5, "Не знаю слова «When»", "Может быть, вы хотели «when»?")
	wantError(t, "when key space pressd\n  print 1\n", "TD004", 1, 16, 22, "Не знаю слова «pressd»", "Может быть, вы хотели «pressed»?")
}

func TestTD005(t *testing.T) {
	e := wantError(t, "when start\n  move right 5\n", "TD005", 2, 14, 15, "Ожидалось слово «by»: например `move right by 5`.", "")
	want := "Проблема в строке 2 (TD005):\n\n" +
		"      move right 5\n" +
		"                 ^\n" +
		"Ожидалось слово «by»: например `move right by 5`.\n"
	if got := e.Format("when start\n  move right 5\n"); got != want {
		t.Errorf("Format:\n%s\nожидалось:\n%s", got, want)
	}
	// условие без продолжения: нет блока
	wantError(t, "when start\n  if lives\n", "TD005", 2, 11, 12, "Ожидался блок", "")
	// пустое выражение
	wantError(t, "when start\n  print\n", "TD005", 2, 8, 9, "ожидалось значение", "")
	// равенство знаком «=»
	wantError(t, "when start\n  if a = 1\n    print 1\n", "TD005", 2, 8, 9, "Знак «=» бывает только в присваивании", "Чтобы сравнить значения, используйте слово «is»: if lives is 0")
}

func TestTD006(t *testing.T) {
	wantError(t, "when start\n  print min(a, b\n", "TD006", 2, 12, 13, "Не закрыта скобка «(»", "Добавьте «)».")
	// незакрытая скобка не «съедает» остальной файл
	_, errs := Parse("when start\n  print min(a, b\n  print 2\nwhen update\n  print 3\n")
	if len(errs) != 1 || errs[0].Code != "TD006" {
		t.Errorf("ожидалась одна TD006, получено %v", errs)
	}
	wantError(t, "when start\n  print a)\n", "TD006", 2, 10, 11, "Лишняя закрывающая скобка", "Уберите её или добавьте открывающую.")
	wantError(t, "when start\n  print [1, 2)\n", "TD006", 2, 14, 15, "закрыта не той скобкой", "")
}

func TestMultipleErrors(t *testing.T) {
	src := "when start\n  pritm 5\n  pritn 1\n  print 2\nwhen strat\n  print 3\n"
	_, errs := Parse(src)
	if len(errs) != 3 {
		t.Fatalf("ожидалось 3 ошибки, получено %v", errs)
	}
	for i, line := range []int{2, 3, 5} {
		if errs[i].Line != line {
			t.Errorf("ошибка %d на строке %d, ожидалась %d", i, errs[i].Line, line)
		}
	}
}

func TestErrorLimit(t *testing.T) {
	src := "when start\n" + strings.Repeat("  pritn 1\n", 100)
	_, errs := Parse(src)
	if len(errs) != maxErrors {
		t.Errorf("ожидалось %d ошибок, получено %d", maxErrors, len(errs))
	}
}

func TestDepthLimit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("  ", i) + "if yes\n")
	}
	b.WriteString(strings.Repeat("  ", 200) + "print 1\n")
	_, errs := Parse("when start\n" + indentAll(b.String()))
	if len(errs) == 0 || !strings.Contains(errs[len(errs)-1].Message, "глубокая вложенность") {
		t.Errorf("ожидалась ошибка глубины, получено %v", errs)
	}
	_, errs = Parse("when start\n  print " + strings.Repeat("(", 500) + "1" + strings.Repeat(")", 500) + "\n")
	if len(errs) == 0 {
		t.Error("ожидалась ошибка глубины выражения")
	}
}

func indentAll(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestSuggest(t *testing.T) {
	cases := []struct {
		word string
		want string
	}{
		{"rigth", "right"}, {"Left", "left"}, {"lft", "left"}, {"UP", "up"}, {"xyz", ""}, {"dwon", "down"},
	}
	for _, c := range cases {
		if got := suggest(c.word, directions, 2); got != c.want {
			t.Errorf("suggest(%q) = %q, ожидалось %q", c.word, got, c.want)
		}
	}
}
