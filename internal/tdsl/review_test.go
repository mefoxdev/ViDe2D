package tdsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- 1. round(...) как функция ----

func TestRoundFunction(t *testing.T) {
	s, errs := Parse("when start\n  x = round(3.4)\n  y = round (x) + 1\n  next round\n")
	if len(errs) != 0 {
		t.Fatalf("round(...) должен разбираться: %v", errs)
	}
	h := s.Items[0].(*Handler)
	as := h.Body[0].(*Assign)
	if call, ok := as.Value.(*Call); !ok || call.Name != "round" || len(call.Args) != 1 {
		t.Errorf("ожидался вызов round, получено %T %v", as.Value, as.Value)
	}
	if _, ok := h.Body[2].(*NextRound); !ok {
		t.Errorf("«next round» должно остаться командой, получено %T", h.Body[2])
	}
	// «round» без скобок как значение по-прежнему ошибка
	if _, errs := Parse("when start\n  x = round\n"); len(errs) == 0 {
		t.Error("«x = round» должно быть ошибкой")
	}
	// Check: типы и число аргументов
	if got := codes(checkSrc(t, "when start\n  x = round(3.4)\n  next round\n", nil)); got != "" {
		t.Errorf("round(3.4): %q", got)
	}
	if got := codes(checkSrc(t, "when start\n  x = round(\"a\")\n", nil)); got != "TD205" {
		t.Errorf("round(\"a\"): %q", got)
	}
	if got := codes(checkSrc(t, "when start\n  x = round(1, 2)\n", nil)); got != "TD203" {
		t.Errorf("round(1, 2): %q", got)
	}
}

// ---- 2. TD004 в направлении: только в Check и только для необъявленных имён ----

func TestDirectionNames(t *testing.T) {
	for _, name := range []string{"lift", "dawn", "ups", "Left"} {
		src := "when start\n  var " + name + " = left\n  move " + name + " by 2\n"
		if got := codes(checkSrc(t, src, nil)); got != "" {
			t.Errorf("%s: ложная ошибка %q", name, got)
		}
		// параметр и переменная цикла
		src = "to f(" + name + ")\n  move " + name + " by 2\nwhen start\n  f(left)\n"
		if got := codes(checkSrc(t, src, nil)); got != "" {
			t.Errorf("параметр %s: ложная ошибка %q", name, got)
		}
		// переменная верхнего уровня
		src = "var " + name + " = up\nwhen start\n  move " + name + " by 2\n"
		if got := codes(checkSrc(t, src, projEnv)); got != "" {
			t.Errorf("верхний уровень %s: ложная ошибка %q", name, got)
		}
	}
	errs := checkSrc(t, "when start\n  move rigth by 5\n", projEnv)
	if codes(errs) != "TD004" || !strings.Contains(errs[0].Hint, "right") {
		t.Errorf("опечатка rigth: %v", errs)
	}
	if got := codes(checkSrc(t, "when start\n  move Left by 5\n", nil)); got != "TD004" {
		t.Errorf("Left без объявления: %q", got)
	}
	// несходное имя — обычная неизвестная переменная, а не TD004
	if got := codes(checkSrc(t, "when start\n  move heading by 5\n", projEnv)); got != "TD101" {
		t.Errorf("heading: %q", got)
	}
	// парсер молчит
	if _, perrs := Parse("when start\n  move rigth by 5\n"); len(perrs) != 0 {
		t.Errorf("парсер не должен ругаться: %v", perrs)
	}
}

// ---- 3. ошибка лексера важнее ошибки парсера на той же строке ----

func TestLexErrorWins(t *testing.T) {
	wantError(t, "when start\n  print @\n", "TD005", 2, 9, 10, "Неожиданный символ «@»", "")
	wantError(t, "when start\n  print 1 + $\n", "TD005", 2, 13, 14, "Неожиданный символ «$»", "")
	wantError(t, "when start\n  print \"abc\n", "TD003", 2, 9, 13, "Не закрыта кавычка", "Добавьте « \" » в конце текста.")
	wantError(t, "when start\n  x = (1 @ 2\n", "TD005", 2, 10, 11, "Неожиданный символ", "")
	// без лексической ошибки по-прежнему побеждает самая левая
	wantError(t, "when start\n  print min(1, 2\n", "TD006", 2, 12, 13, "Не закрыта скобка", "Добавьте «)».")
}

// ---- 4. пределы длины и размера ----

func TestChainLimit(t *testing.T) {
	for name, op := range map[string]string{"+": "+", "-": "-", "*": "*", "and": " and ", "or": " or "} {
		src := "var a = 1" + strings.Repeat(op+"1", maxChain+5) + "\nwhen start\n  print a\n"
		script, errs := Parse(src)
		if len(errs) != 1 || errs[0].Code != "TD005" || !strings.Contains(errs[0].Message, "слишком длинное") || errs[0].Line != 1 {
			t.Errorf("%s: %v", name, errs)
		}
		if len(script.Items) != 1 {
			t.Errorf("%s: разбор должен продолжиться, элементов %d", name, len(script.Items))
		}
		// допустимая длина проходит
		ok := "var a = 1" + strings.Repeat(op+"1", 1000) + "\n"
		if _, errs := Parse(ok); len(errs) != 0 {
			t.Errorf("%s: длина 1000 должна проходить: %v", name, errs)
		}
	}
	// цепочка точек и индексов
	src := "var a = b" + strings.Repeat(".c", maxChain+5) + "\n"
	if _, errs := Parse(src); len(errs) != 1 {
		t.Errorf("точки: %v", errs)
	}
	// ровно в пределах: ни Check, ни String не должны падать
	deep := "var a = 1" + strings.Repeat("+1", maxChain-1) + "\nwhen start\n  print a\n"
	script, errs := Parse(deep)
	if len(errs) != 0 {
		t.Fatalf("цепочка длиной %d должна разбираться: %v", maxChain, errs)
	}
	_ = Check(script, projEnv)
	_ = script.Items[0].(*VarDecl).Value.String()
}

func TestSourceSizeLimit(t *testing.T) {
	src := "var a = 1" + strings.Repeat("+1", 3_000_000) + "\n"
	script, errs := Parse(src)
	if len(errs) != 1 || errs[0].Code != "TD005" || errs[0].Line != 1 || !strings.Contains(errs[0].Message, "слишком большой") {
		t.Fatalf("ожидалась одна ошибка размера: %v", errs)
	}
	if script == nil || len(script.Items) != 0 {
		t.Errorf("дерево должно быть пустым: %+v", script)
	}
	_ = Check(script, nil)
	_ = Highlight(src)
	_ = Complete(src, 0, 100, nil)
	_ = errs[0].Format(src)
}

// ---- 6. сообщения и подчёркивание ----

func TestWrongTypeMessages(t *testing.T) {
	cases := []struct{ src, want string }{
		{"when start\n  print [1] * 2\n", "Для «*»: ожидается число, а тут список."},
		{"when start\n  x = \"a\"\n", "Здесь хранится число, а записывается текст."},
		{"when touch 5\n  print 1\n", "После «touch» ожидается метка в кавычках или объект, а тут число."},
		{"when start\n  print abs(\"a\")\n", "Для «abs»: ожидается число, а тут текст."},
		{"when start\n  print -\"a\"\n", "После минуса: ожидается число, а тут текст."},
	}
	for _, c := range cases {
		errs := checkSrc(t, c.src, nil)
		if len(errs) == 0 || errs[0].Message != c.want {
			t.Errorf("%q:\nполучено %v\nожидалось %q", c.src, errs, c.want)
		}
		for _, e := range errs {
			for _, bad := range []string{"нужно нужно", "Сюда нужно", "нужно текст", ": нужно"} {
				if strings.Contains(e.Message, bad) {
					t.Errorf("плохая формулировка %q: %q", bad, e.Message)
				}
			}
		}
	}
}

func TestUnderlineSourceLength(t *testing.T) {
	cases := []struct {
		src      string
		col, end int
	}{
		{"when start\n  tag = 0.50\n", 9, 13},
		{"when start\n  tag = 007\n", 9, 12},
		{"when start\n  x = \"a\\nb\"\n", 7, 13},
		{"when start\n  x = \"{y}x\"\n", 7, 13},
	}
	for _, c := range cases {
		errs := checkSrc(t, c.src, nil)
		if len(errs) != 1 || errs[0].Col != c.col || errs[0].EndCol != c.end {
			t.Errorf("%q: %v (ожидалось %d-%d)", c.src, errs, c.col, c.end)
		}
	}
}

// ---- 7. запятая в конце ----

func TestTrailingComma(t *testing.T) {
	wantError(t, "when start\n  print f(1,)\n", "TD005", 2, 12, 13, "После запятой нужно значение — уберите запятую.", "")
	wantError(t, "when start\n  var a = [1, 2,]\n", "TD005", 2, 16, 17, "После запятой нужно значение — уберите запятую.", "")
	wantError(t, "var a = [\n  1,\n  2,\n]\n", "TD005", 3, 4, 5, "После запятой нужно значение", "")
	wantError(t, "to f(a,)\n  print a\n", "TD005", 1, 7, 8, "После запятой нужно значение", "")
	for _, src := range []string{"when start\n  print f(1, 2)\n", "var a = [1, 2]\n", "var a = []\n"} {
		if _, errs := Parse(src); len(errs) != 0 {
			t.Errorf("%q: %v", src, errs)
		}
	}
}

// ---- 8. BOM ----

func TestBOMColumns(t *testing.T) {
	src := "\uFEFFwhen start\n  print 1\n"
	plain := "when start\n  print 1\n"
	a, b := Highlight(src), Highlight(plain)
	if len(a) != len(b) {
		t.Fatalf("число строк: %d и %d", len(a), len(b))
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			t.Fatalf("строка %d: %v и %v", i, a[i], b[i])
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				t.Errorf("строка %d, кусок %d: %v и %v", i, j, a[i][j], b[i][j])
			}
		}
	}
	ca := Complete("\uFEFFwhen st", 0, 7, nil)
	cb := Complete("when st", 0, 7, nil)
	if len(ca) == 0 || len(ca) != len(cb) || ca[0] != cb[0] {
		t.Errorf("Complete с BOM: %v и %v", ca, cb)
	}
	_, errs := Parse("\uFEFFwhen start\n  print @\n")
	if len(errs) != 1 {
		t.Fatalf("%v", errs)
	}
	if line, _ := sourceLine("\uFEFFwhen start\n", 1); line != "when start" {
		t.Errorf("sourceLine: %q", line)
	}
	e := &Error{Code: "TD005", Line: 1, Col: 1, EndCol: 5, Message: "m"}
	if !strings.Contains(e.Format("\uFEFFwhen start\n"), "    when start\n    ^^^^\n") {
		t.Errorf("Format с BOM: %q", e.Format("\uFEFFwhen start\n"))
	}
}

// ---- 11. touching: событие и функция ----

func TestTouching(t *testing.T) {
	src := "when touch \"coin\"\n  destroy other\nwhen stop touching \"coin\"\n  print 1\n" +
		"when update\n  if touching(\"wall\", down) and not touching(\"x\")\n    print 2\n"
	if got := codes(checkSrc(t, src, nil)); got != "" {
		t.Errorf("touching: %q", got)
	}
	// «touching» — имя встроенной функции, как переменную его не объявить
	if got := codes(checkSrc(t, "var touching = 1\nwhen start\n  print 1\n", nil)); got != "TD103" {
		t.Errorf("var touching: %q", got)
	}
	wantError(t, "when stop moving \"coin\"\n  print 1\n", "TD005", 1, 11, 17, "touching", "")
	if got := codes(checkSrc(t, "when start\n  print touching(1)\n", nil)); got != "" {
		t.Errorf("touching(1) тип первого аргумента не проверяется: %q", got)
	}
	if got := codes(checkSrc(t, "when start\n  print touching(\"a\", 5)\n", nil)); got != "TD205" {
		t.Errorf("touching(\"a\", 5): %q", got)
	}
}

// ---- 11. примеры с окружением проекта ----

func sharedNames(s *Script) []string {
	var out []string
	for _, it := range s.Items {
		if d, ok := it.(*SharedDecl); ok {
			out = append(out, d.Name)
		}
	}
	return out
}

func TestCheckExamplesWithEnv(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.tdsl")
	scripts := map[string]*Script{}
	srcs := map[string]string{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		s, errs := Parse(string(b))
		if len(errs) > 0 {
			t.Fatalf("%s: %v", f, errs[0])
		}
		scripts[f], srcs[f] = s, string(b)
	}
	for _, f := range files {
		own := map[string]bool{}
		// имена, которые файл объявляет сам (в том числе локально), чужим shared не конфликтуют
		for _, it := range scripts[f].Items {
			switch d := it.(type) {
			case *SharedDecl:
				own[d.Name] = true
			case *VarDecl:
				own[d.Name] = true
			case *ActionDef:
				eachStmt(d.Body, func(st Stmt) {
					if v, ok := st.(*VarDecl); ok {
						own[v.Name] = true
					}
				})
			case *Handler:
				eachStmt(d.Body, func(st Stmt) {
					if v, ok := st.(*VarDecl); ok {
						own[v.Name] = true
					}
				})
			}
		}
		env := &Env{Objects: []string{"Hero", "Coin", "Door"}, SharedDeclared: map[string]string{}}
		for g, s := range scripts {
			if g == f {
				continue
			}
			for _, n := range sharedNames(s) {
				if !own[n] {
					env.SharedDeclared[n] = filepath.Base(g)
				}
			}
		}
		for _, e := range Check(scripts[f], env) {
			t.Errorf("%s: %s", f, e.Format(srcs[f]))
		}
	}
}
