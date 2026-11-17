package tdsl

import (
	"fmt"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Script {
	t.Helper()
	s, errs := Parse(src)
	if len(errs) > 0 {
		t.Fatalf("ошибки: %v", errs)
	}
	return s
}

func TestEmptyAndComments(t *testing.T) {
	for _, src := range []string{"", "\n\n", "   \n", "# только комментарий\n", "# a\n\n  # b\n# c", "\uFEFF# bom\n"} {
		s := mustParse(t, src)
		if len(s.Items) != 0 {
			t.Errorf("%q: ожидалась пустая программа", src)
		}
	}
}

func TestCRLF(t *testing.T) {
	src := "var a = 1\r\n\r\nwhen start\r\n  if a is 1\r\n    print \"x\"\r\n  else\r\n    print \"y\"\r\n"
	s := mustParse(t, src)
	if len(s.Items) != 2 {
		t.Fatalf("элементов %d", len(s.Items))
	}
}

func TestTrailingComment(t *testing.T) {
	s := mustParse(t, "var speed = 70   # скорость\nwhen start  # старт\n  print \"a # не комментарий\"  # да\n  # строка-комментарий\n  hide\n")
	h := s.Items[1].(*Handler)
	if len(h.Body) != 2 {
		t.Fatalf("команд %d", len(h.Body))
	}
	if txt, _ := h.Body[0].(*Print).Value.(*Text).Literal(); txt != "a # не комментарий" {
		t.Errorf("текст %q", txt)
	}
}

func TestNoTrailingNewline(t *testing.T) {
	mustParse(t, "when start\n  if yes\n    print 1")
}

func TestElseIf(t *testing.T) {
	s := mustParse(t, "when start\n  if a\n    print 1\n  else if b\n    print 2\n  else if c\n    print 3\n  else\n    print 4\n    print 5\n  print 6\n")
	h := s.Items[0].(*Handler)
	if len(h.Body) != 2 {
		t.Fatalf("команд %d", len(h.Body))
	}
	st := h.Body[0].(*If)
	if len(st.Branches) != 3 || len(st.Else) != 2 {
		t.Errorf("ветвей %d, else %d", len(st.Branches), len(st.Else))
	}
	if st.Branches[2].Cond.String() != "c" {
		t.Errorf("условие %s", st.Branches[2].Cond)
	}
}

func TestNestedBlocks(t *testing.T) {
	src := `to f(a, b)
  repeat 3 times
    for each i in 1 to a
      if i is b
        stop repeating
      else
        next round
    while yes
      return a
  return
when update
  if a
    if b
      print 1
  else
    print 2
`
	s := mustParse(t, src)
	def := s.Items[0].(*ActionDef)
	if def.Name != "f" || len(def.Params) != 2 || len(def.Body) != 2 {
		t.Fatalf("действие: %+v", def)
	}
	rep := def.Body[0].(*Repeat)
	if rep.Count.String() != "3" || len(rep.Body) != 2 {
		t.Errorf("repeat: %+v", rep)
	}
	fe := rep.Body[0].(*For)
	if fe.Var != "i" || fe.From.String() != "1" || fe.To.String() != "a" || fe.List != nil {
		t.Errorf("for: %+v", fe)
	}
	if _, ok := def.Body[1].(*Return); !ok || def.Body[1].(*Return).Value != nil {
		t.Errorf("return без значения: %+v", def.Body[1])
	}
	upd := s.Items[1].(*Handler)
	outer := upd.Body[0].(*If)
	if len(outer.Branches) != 1 || len(outer.Else) != 1 || len(outer.Branches[0].Body) != 1 {
		t.Errorf("вложенный if: %+v", outer)
	}
}

func TestMultilineBrackets(t *testing.T) {
	src := "var items = [\n  \"меч\",\n  \"щит\",   # комментарий\n  \"лук\"\n]\nwhen start\n  print min(\n    1,\n    2\n  )\n  print 3\n"
	s := mustParse(t, src)
	l := s.Items[0].(*VarDecl).Value.(*List)
	if len(l.Elems) != 3 {
		t.Errorf("элементов %d", len(l.Elems))
	}
	if len(s.Items[1].(*Handler).Body) != 2 {
		t.Error("ожидалось две команды")
	}
}

func TestEvents(t *testing.T) {
	src := `when start
  hide
when update
  hide
when key space pressed
  hide
when key "1" released
  hide
when key left held
  hide
when click
  hide
when touch "coin"
  hide
when touch Door
  hide
when stop touching "coin"
  hide
when message "hit"
  hide
when every 2 seconds
  hide
when every 0.5 second
  hide
`
	s := mustParse(t, src)
	want := []struct {
		kind  EventKind
		key   string
		state KeyState
		arg   string
	}{
		{EventStart, "", "", ""}, {EventUpdate, "", "", ""}, {EventKey, "space", KeyPressed, ""},
		{EventKey, "1", KeyReleased, ""}, {EventKey, "left", KeyHeld, ""}, {EventClick, "", "", ""},
		{EventTouch, "", "", "\"coin\""}, {EventTouch, "", "", "Door"}, {EventStopTouching, "", "", "\"coin\""},
		{EventMessage, "", "", "\"hit\""}, {EventEvery, "", "", "2"}, {EventEvery, "", "", "0.5"},
	}
	if len(s.Items) != len(want) {
		t.Fatalf("обработчиков %d", len(s.Items))
	}
	for i, w := range want {
		h := s.Items[i].(*Handler)
		arg := ""
		if h.Arg != nil {
			arg = h.Arg.String()
		}
		if h.Event != w.kind || h.Key != w.key || h.State != w.state || arg != w.arg {
			t.Errorf("%d: %+v", i, h)
		}
	}
}

func TestCommands(t *testing.T) {
	src := `when start
  move left by 5
  move heading by 60 per second avoiding "wall"
  move to 100, 50
  slide to 100, 50 in 2 seconds
  slide to a in b, c in 1 second
  turn by 90
  show
  hide
  destroy
  destroy other
  spawn "Star" at 10, 20
  var s = spawn "Star" at 10, 20
  play sound "jump.wav"
  play music "theme"
  stop music
  print "hi"
  send "hit"
  send "hit" with 5
  send "open" to Door
  send "hit" with 5 to Door
  wait 2 seconds
  go to scene "level2"
  restart scene
  remember "best" as score
  add "меч" to items
  remove "меч" from items
  clear items
  sort items
  shuffle items
  stop repeating
  next round
  heal(20)
`
	s := mustParse(t, src)
	body := s.Items[0].(*Handler).Body
	types := []string{}
	for _, st := range body {
		name := strings.TrimPrefix(strings.TrimPrefix(typeName(st), "*tdsl."), "*")
		types = append(types, name)
	}
	want := "Move Move MoveTo SlideTo SlideTo Turn Show Hide Destroy Destroy ExprStmt VarDecl Play Play StopMusic Print Send Send Send Send Wait GoToScene RestartScene Remember ListCmd ListCmd ListCmd ListCmd ListCmd StopRepeating NextRound ExprStmt"
	if got := strings.Join(types, " "); got != want {
		t.Errorf("\n%s\n%s", got, want)
	}
	mv := body[1].(*Move)
	if mv.Dir.String() != "heading" || mv.By.String() != "(per-second 60)" || mv.Avoiding.String() != "\"wall\"" {
		t.Errorf("move: %+v", mv)
	}
	sl := body[4].(*SlideTo)
	if sl.X.String() != "(in a b)" || sl.Y.String() != "c" || sl.Duration.String() != "1" {
		t.Errorf("slide: %s %s %s", sl.X, sl.Y, sl.Duration)
	}
	if sd := body[19].(*Send); sd.With.String() != "5" || sd.To.String() != "Door" {
		t.Errorf("send: %+v", sd)
	}
}

func TestAssignments(t *testing.T) {
	src := "when start\n  hp = 1\n  hp -= 5\n  hp += 1\n  hp *= 2\n  hp /= 2\n  Hero.hp -= 10\n  items[2] = \"лук\"\n  a.b[1].c = 3\n  self.x = 1\n  other.hp += 1\n  x = y + 1\n"
	s := mustParse(t, src)
	body := s.Items[0].(*Handler).Body
	ops, targets := []string{}, []string{}
	for _, st := range body {
		a := st.(*Assign)
		ops = append(ops, a.Op)
		targets = append(targets, a.Target.String())
	}
	if got := strings.Join(ops, " "); got != "= -= += *= /= -= = = = += =" {
		t.Errorf("операции: %s", got)
	}
	if got := strings.Join(targets, " "); got != "hp hp hp hp hp (. Hero hp) (index items 2) (. (index (. a b) 1) c) (. self x) (. other hp) x" {
		t.Errorf("цели: %s", got)
	}
	_, errs := Parse("when start\n  f(1) = 2\n")
	if len(errs) != 1 || errs[0].Code != "TD005" {
		t.Errorf("присваивание вызову: %v", errs)
	}
}

func TestTopLevel(t *testing.T) {
	src := "var speed = 70\nshared coins = 0\nuse \"helpers.tdsl\"\nto heal(amount)\n  hp = amount\nto noargs()\n  show\n"
	s := mustParse(t, src)
	if len(s.Items) != 5 {
		t.Fatalf("элементов %d", len(s.Items))
	}
	if u := s.Items[2].(*UseDecl); u.Path != "helpers.tdsl" {
		t.Errorf("use: %+v", u)
	}
	if p := s.Items[0].Position(); p.Line != 1 || p.Col != 1 {
		t.Errorf("позиция: %+v", p)
	}
	if p := s.Items[3].(*ActionDef).Body[0].Position(); p.Line != 5 || p.Col != 3 {
		t.Errorf("позиция тела: %+v", p)
	}
}

func TestRecovery(t *testing.T) {
	// после ошибки в заголовке блок всё равно проверяется
	_, errs := Parse("when strat\n  pritn 1\nwhen start\n  print 1\n")
	if len(errs) != 2 || errs[0].Line != 1 || errs[1].Line != 2 {
		t.Errorf("получено %v", errs)
	}
	// команда вне события
	_, errs = Parse("print 1\n")
	if len(errs) != 1 || errs[0].Code != "TD005" {
		t.Errorf("получено %v", errs)
	}
	// else без if, == и мусорные символы
	for _, src := range []string{"when start\n  else\n    hide\n", "when start\n  if a == 1\n    hide\n", "when start\n  print $\n", "when start\n  x = {\n"} {
		if _, errs := Parse(src); len(errs) == 0 {
			t.Errorf("%q: ожидалась ошибка", src)
		}
	}
}

func TestUnicodeColumns(t *testing.T) {
	_, errs := Parse("when start\n  print \"Привет\" 5\n")
	if len(errs) != 1 || errs[0].Col != 18 {
		t.Errorf("столбец в символах: %v", errs)
	}
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }
