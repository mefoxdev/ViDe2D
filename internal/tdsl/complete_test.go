package tdsl

import (
	"fmt"
	"strings"
	"testing"
)

var cmEnv = &CompleteEnv{
	Objects: []string{"Hero", "Door"},
	Tags:    []string{"coin", "wall"},
	Assets:  []string{"hero.png", "jump.wav", "theme.ogg"},
	Scripts: []string{"helpers.tdsl"},
	Scenes:  []string{"level1", "level2"},
}

// cmAt разбирает «текст|» — курсор отмечен символом «|» (последний в тексте — позиция курсора).
func cmAt(t *testing.T, src string) (string, int, int) {
	t.Helper()
	k := strings.IndexRune(src, '|')
	if k < 0 {
		t.Fatal("нет курсора")
	}
	before := src[:k]
	line := strings.Count(before, "\n")
	col := len([]rune(before[strings.LastIndex(before, "\n")+1:]))
	return src[:k] + src[k+1:], line, col
}

func cmLabels(src string, env *CompleteEnv, t *testing.T) []string {
	text, l, c := cmAt(t, src)
	var out []string
	for _, x := range Complete(text, l, c, env) {
		out = append(out, x.Label)
	}
	return out
}

func cmHas(list []string, want ...string) bool {
	for _, w := range want {
		ok := false
		for _, l := range list {
			if l == w {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func TestCompleteContexts(t *testing.T) {
	cases := []struct {
		name string
		src  string
		has  []string
		not  []string
	}{
		{"top empty", "|", []string{"when", "var", "shared", "to", "use"}, []string{"move", "if"}},
		{"top prefix", "wh|", []string{"when"}, []string{"var", "use"}},
		{"top after blank", "var a = 1\n\n|", []string{"when", "to"}, nil},
		{"when events", "when |", []string{"start", "update", "key", "click", "touch", "stop touching", "message", "every"}, []string{"move"}},
		{"when prefix", "when ke|", []string{"key"}, []string{"start"}},
		{"when stop", "when stop |", []string{"touching"}, []string{"start"}},
		{"when key name", "when key |", []string{"space", "left"}, nil},
		{"when key state", "when key space |", []string{"pressed", "released", "held"}, nil},
		{"when touch", "when touch |", []string{"\"coin\"" /*placeholder*/}, nil},
		{"block statements", "when start\n  |", []string{"if", "repeat", "while", "for each", "return", "move", "show", "print", "abs"}, []string{"when", "yes"}},
		{"block prefix", "when start\n  mo|", []string{"move"}, []string{"show"}},
		{"block own vars", "var speed = 1\nshared coins = 0\nto heal(amount)\n  var tmp = 1\n  |", []string{"speed", "coins", "heal", "amount", "tmp"}, nil},
		{"params scoped", "to heal(amount)\n  hp = 1\nwhen start\n  |", []string{"heal"}, []string{"amount"}},
		{"for each var", "when start\n  for each item in items\n    print |", []string{"item"}, nil},
		{"after dot", "print Hero.|", []string{"x", "y", "width", "font_size", "length", "first", "last"}, []string{"move"}},
		{"after dot prefix", "print Hero.wi|", []string{"width"}, []string{"x"}},
		{"move dir", "when start\n  move |", []string{"left", "right", "up", "down", "to"}, []string{"show"}},
		{"move dir vars", "var d = left\nwhen start\n  move |", []string{"d"}, nil},
		{"move by", "move left |", []string{"by"}, nil},
		{"move by expr", "move left by |", []string{"x", "abs", "yes"}, nil},
		{"move per", "move left by 5 |", []string{"per second", "avoiding"}, nil},
		{"move avoiding", "move left by 5 per second |", []string{"avoiding"}, nil},
		{"play", "play |", []string{"sound", "music"}, nil},
		{"play assets", "play sound |", []string{"jump.wav"}, nil},
		{"play assets string", "play sound \"|", []string{"hero.png", "jump.wav", "theme.ogg"}, nil},
		{"play assets string prefix", "play music \"th|", []string{"theme.ogg"}, []string{"jump.wav"}},
		{"image assign", "image = \"|", []string{"hero.png"}, nil},
		{"use", "use \"|", []string{"helpers.tdsl"}, nil},
		{"go scene", "go to scene \"|", []string{"level1", "level2"}, nil},
		{"touch string", "when touch \"|", []string{"coin", "wall", "Hero", "Door"}, nil},
		{"touch string prefix", "when touch \"co|", []string{"coin"}, []string{"wall"}},
		{"stop touching string", "when stop touching \"|", []string{"coin"}, nil},
		{"send messages", "when message \"hit\"\n  print 1\nwhen start\n  send \"|", []string{"hit"}, nil},
		{"send quoted code", "when message \"hit\"\nwhen start\n  send |", []string{"hit"}, nil},
		{"send continue", "when start\n  send \"a\" |", []string{"with", "to"}, nil},
		{"in string nothing", "print \"hello wor|", nil, []string{"x", "abs", "move"}},
		{"in braces", "print \"hp: {|", []string{"x", "abs", "yes"}, []string{"move"}},
		{"in braces prefix", "print \"hp: {ab|", []string{"abs"}, []string{"x"}},
		{"after braces nothing", "print \"hp: {x} wor|", nil, []string{"x", "abs"}},
		{"expr yes", "if hp is |", []string{"yes", "no", "nothing", "left", "self", "mouse", "key"}, nil},
		{"other in touch", "when touch \"coin\"\n  print |", []string{"other"}, []string{"value"}},
		{"value in message", "when message \"hit\"\n  print |", []string{"value"}, []string{"other"}},
		{"no other elsewhere", "when start\n  print |", nil, []string{"other", "value"}},
		{"comment", "print 1 # abs|", nil, []string{"abs"}},
		{"else if", "if x\n  show\nelse |", []string{"if"}, nil},
		{"for each in", "for each i |", []string{"in"}, nil},
		{"repeat times", "repeat 3 |", []string{"times"}, nil},
		{"wait seconds", "wait 2 |", []string{"seconds"}, nil},
		{"var assign expr", "var a = |", []string{"abs", "yes"}, []string{"move"}},
		{"var new name", "var |", nil, []string{"move", "abs"}},
		{"unicode", "var счёт = 1\nwhen start\n  print сч|", []string{"счёт"}, nil},
		{"cursor mid line", "when start\n  print |x + 1", []string{"abs"}, nil},
		{"crlf", "when start\r\n  mo|\r\n", []string{"move"}, nil},
	}
	for _, c := range cases {
		got := cmLabels(c.src, cmEnv, t)
		if c.name == "when touch" {
			// после «touch » предлагаются метки в кавычках (Insert), метка без кавычек
			text, l, col := cmAt(t, c.src)
			var ins []string
			for _, x := range Complete(text, l, col, cmEnv) {
				ins = append(ins, x.Insert)
			}
			if !cmHas(ins, `"coin"`, `"wall"`, "Hero", "Door") {
				t.Errorf("%s: %v", c.name, ins)
			}
			continue
		}
		if c.has == nil && len(c.not) == 0 {
			continue
		}
		if !cmHas(got, c.has...) {
			t.Errorf("%s: нет %v в %v", c.name, c.has, got)
		}
		for _, n := range c.not {
			if cmHas(got, n) {
				t.Errorf("%s: лишнее %q в %v", c.name, n, got)
			}
		}
	}
}

func TestCompleteNothingInStringOrComment(t *testing.T) {
	for _, s := range []string{`print "hello |`, `print "a {x} b|`, `x = 1 # |`, `print "a\" |`} {
		if got := cmLabels(s, cmEnv, t); len(got) != 0 {
			t.Errorf("%q: %v", s, got)
		}
	}
}

func TestCompleteInsertAndDetail(t *testing.T) {
	text, l, c := cmAt(t, "when start\n  |")
	m := map[string]Completion{}
	for _, x := range Complete(text, l, c, nil) {
		m[x.Label] = x
	}
	if m["move"].Insert != "move " || m["move"].Kind != CompCommand || m["move"].Detail != "сдвинуть объект" {
		t.Errorf("move: %+v", m["move"])
	}
	if m["abs"].Insert != "abs(" || m["abs"].Kind != CompBuiltin {
		t.Errorf("abs: %+v", m["abs"])
	}
	if m["for each"].Insert != "for each " {
		t.Errorf("for each: %+v", m["for each"])
	}
	for _, x := range m {
		if x.Detail == "" {
			t.Errorf("пустой Detail: %+v", x)
		}
	}
}

func TestCompleteSortAndCase(t *testing.T) {
	text, l, c := cmAt(t, "when start\n  S|")
	got := Complete(text, l, c, nil)
	if len(got) == 0 || got[0].Label != "shuffle" && got[0].Label != "show" && got[0].Label != "slide" && got[0].Label != "sort" && got[0].Label != "stop music" && got[0].Label != "sin" && got[0].Label != "sqrt" && got[0].Label != "send" {
		t.Fatalf("%v", got)
	}
	// префиксные совпадения идут раньше «содержит»
	seenSub := false
	for _, x := range got {
		starts := strings.HasPrefix(strings.ToLower(x.Label), "s")
		if !starts {
			seenSub = true
		} else if seenSub {
			t.Errorf("префиксное %q после подстрочного", x.Label)
		}
	}
	for i := 1; i < len(got); i++ {
		a, b := strings.HasPrefix(strings.ToLower(got[i-1].Label), "s"), strings.HasPrefix(strings.ToLower(got[i].Label), "s")
		if a == b && (got[i-1].Kind == CompBuiltin) == (got[i].Kind == CompBuiltin) && got[i-1].Label > got[i].Label {
			t.Errorf("не по алфавиту: %q > %q", got[i-1].Label, got[i].Label)
		}
	}
}

func TestCompleteLimitAndBounds(t *testing.T) {
	env := &CompleteEnv{}
	for i := 0; i < 200; i++ {
		env.Objects = append(env.Objects, fmt.Sprintf("Obj%03d", i))
	}
	if got := Complete("print |", 0, 6, env); len(got) != completeLimit {
		t.Errorf("len = %d", len(got))
	}
	for _, p := range [][2]int{{-1, 0}, {5, 0}, {0, -3}, {0, 999}} {
		Complete("print 1", p[0], p[1], nil) // без паники
	}
	if got := Complete("", 0, 0, nil); len(got) != 5 {
		t.Errorf("top = %v", got)
	}
}

func TestCompleteUnicodeColumns(t *testing.T) {
	src := "var ёж = 1\nwhen start\n  print ёж + ё"
	got := Complete(src, 2, len([]rune("  print ёж + ё")), nil)
	if len(got) == 0 || got[0].Label != "ёж" {
		t.Errorf("%v", got)
	}
}

func FuzzComplete(f *testing.F) {
	for _, s := range []string{"", "when ", "print \"a{b", "when touch \"", "to f(a)\n  move ", "x = \"{\"{", "\xff"} {
		f.Add(s, 0, 3)
		f.Add(s, 1, 100)
	}
	f.Fuzz(func(t *testing.T, s string, line, col int) {
		res := Complete(s, line, col, cmEnv)
		if len(res) > completeLimit {
			t.Fatalf("слишком много: %d", len(res))
		}
	})
}
