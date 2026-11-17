package tdsl

import "testing"

// parseExprSrc разбирает выражение внутри «print» и возвращает его печать.
func parseExprSrc(t *testing.T, expr string) (string, []*Error) {
	t.Helper()
	script, errs := Parse("when start\n  print " + expr + "\n")
	if len(errs) > 0 {
		return "", errs
	}
	h := script.Items[0].(*Handler)
	return h.Body[0].(*Print).Value.String(), nil
}

func TestPrecedence(t *testing.T) {
	cases := []struct{ src, want string }{
		{"1 + 2 * 3", "(+ 1 (* 2 3))"},
		{"(1 + 2) * 3", "(* (+ 1 2) 3)"},
		{"a - b - c", "(- (- a b) c)"},
		{"a / b * c % d", "(% (* (/ a b) c) d)"},
		{"-a * b", "(* (- a) b)"},
		{"- - 1", "(- (- 1))"},
		{"-(1 + 2)", "(- (+ 1 2))"},
		{"speed per second", "(per-second speed)"},
		{"a + b per second", "(per-second (+ a b))"},
		{"a < b + 1", "(< a (+ b 1))"},
		{"a + 1 is b * 2", "(is (+ a 1) (* b 2))"},
		{"x is not 5", "(is not x 5)"},
		{"x is left", "(is x left)"},
		{"x >= 1", "(>= x 1)"},
		{"\"a\" in items", "(in \"a\" items)"},
		{"\"a\" not in items", "(not in \"a\" items)"},
		{"not a and b", "(and (not a) b)"},
		{"not a is b", "(not (is a b))"},
		{"not not a", "(not (not a))"},
		{"a or b and c", "(or a (and b c))"},
		{"a and b or c and d", "(or (and a b) (and c d))"},
		{"a < b and c or d", "(or (and (< a b) c) d)"},
		{"a.b[1].c", "(. (index (. a b) 1) c)"},
		{"f(1, 2 + 3)[1]", "(index (call f 1 (+ 2 3)) 1)"},
		{"f()", "(call f)"},
		{"[1, [2, a], []]", "[1 [2 a] []]"},
		{"key space is held and yes", "(and (key-held space) yes)"},
		{"not key \"1\" is held", "(not (key-held 1))"},
		{"mouse.held or mouse.x > 5", "(or (. mouse held) (> (. mouse x) 5))"},
		{"spawn \"Star\" at 1 + 1, 2", "(spawn \"Star\" (+ 1 1) 2)"},
		{"yes or no", "(or yes no)"},
		{"nothing is self", "(is nothing self)"},
		{"other.hp", "(. other hp)"},
		{"0.5 + 12", "(+ 0.5 12)"},
		{"touching(\"floor\", down)", "(call touching \"floor\" down)"},
		{"\"Счёт: {coins * 10}!\"", "\"Счёт: {(* coins 10)}!\""},
		{"\"{ \"a\" + b }\"", "\"{(+ \"a\" b)}\""},
		{"\"{f(\"x{y}\")}\"", "\"{(call f \"x{y}\")}\""},
		{"\"\\{нет\\} \\\"q\\\" \\\\ {a}\"", "\"{нет} \\\"q\\\" \\\\ {a}\""},
		{"имя_1 + Имя", "(+ имя_1 Имя)"},
	}
	for _, c := range cases {
		got, errs := parseExprSrc(t, c.src)
		if len(errs) > 0 {
			t.Errorf("%s: ошибки %v", c.src, errs)
			continue
		}
		if got != c.want {
			t.Errorf("%s:\n получено   %s\n ожидалось %s", c.src, got, c.want)
		}
	}
}

func TestComparisonChainForbidden(t *testing.T) {
	for _, src := range []string{"1 < x < 5", "a is b is c", "a < b in c", "a is not b < c"} {
		_, errs := parseExprSrc(t, src)
		if len(errs) != 1 || errs[0].Code != "TD005" || errs[0].Message != "Цепочки сравнений запрещены." {
			t.Errorf("%s: ожидалась ошибка цепочки, получено %v", src, errs)
		}
	}
}

func TestInterpolationErrors(t *testing.T) {
	// позиция ошибки внутри вставки указывает на столбец во всей строке
	_, errs := Parse("when start\n  print \"ab {x +} cd\"\n")
	if len(errs) != 1 || errs[0].Line != 2 || errs[0].Col != 17 {
		t.Errorf("получено %v", errs)
	}
	_, errs = Parse("when start\n  print \"ab {} cd\"\n")
	if len(errs) != 1 || errs[0].Code != "TD005" {
		t.Errorf("пустая вставка: %v", errs)
	}
	_, errs = Parse("when start\n  print \"ab {x\"\n")
	if len(errs) == 0 {
		t.Error("незакрытая вставка должна давать ошибку")
	}
}
