package tdsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkSrc(t *testing.T, src string, env *Env) []*Error {
	t.Helper()
	s, perrs := Parse(src)
	if len(perrs) > 0 {
		t.Fatalf("синтаксическая ошибка в тесте: %v\n%s", perrs[0], src)
	}
	return Check(s, env)
}

func codes(errs []*Error) string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Code)
	}
	return strings.Join(out, ",")
}

// TestCheckExamples: все программы из testdata корректны и проходят без ошибок.
func TestCheckExamples(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.tdsl")
	if len(files) == 0 {
		t.Fatal("нет файлов testdata")
	}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		s, perrs := Parse(string(src))
		if len(perrs) > 0 {
			t.Fatalf("%s: %v", f, perrs[0])
		}
		for _, e := range Check(s, nil) {
			t.Errorf("%s: %s", f, e.Format(string(src)))
		}
	}
}

var projEnv = &Env{
	Objects:        []string{"Hero", "Coin", "Door"},
	Assets:         []string{"hero.png", "jump.wav", "theme.ogg"},
	Scripts:        []string{"helpers.tdsl"},
	Templates:      []string{"Star"},
	Scenes:         []string{"menu", "level2"},
	SharedDeclared: map[string]string{"lives": "game.tdsl"},
}

func TestCheckCases(t *testing.T) {
	cases := []struct {
		name string
		src  string
		env  *Env
		want string // коды через запятую в порядке позиций
	}{
		// TD101
		{"101 ok", "var hp = 1\nwhen start\n  hp += 1\n  print Hero.x\n  print lives\n", projEnv, ""},
		{"101 typo", "var coins = 0\nwhen start\n  Coins += 1\n", projEnv, "TD101"},
		{"101 object typo", "when start\n  print Hreo.x\n", projEnv, "TD101"},
		{"101 no env silent", "when start\n  Coins += 1\n", nil, ""},
		{"101 block scope ok", "when start\n  if yes\n    var a = 1\n    print a\n  if yes\n    var a = 2\n    print a\n", projEnv, ""},
		{"101 block scope end", "when start\n  if yes\n    var a = 1\n  print a\n", projEnv, "TD101"},
		{"101 use before decl", "when start\n  print a\n  var a = 1\n", projEnv, "TD101"},
		{"101 param and for", "to f(n)\n  for each i in 1 to n\n    print i\n", projEnv, ""},
		{"101 for var scope", "when start\n  for each i in 1 to 3\n    print i\n  print i\n", projEnv, "TD101"},
		{"101 other in touch", "when touch \"coin\"\n  destroy other\n", projEnv, ""},
		{"101 other in start", "when start\n  destroy other\n", projEnv, "TD101"},
		{"101 value in message", "when message \"hit\"\n  print value\n", projEnv, ""},
		{"101 value in start", "when start\n  print value\n", projEnv, "TD101"},
		{"101 mouse and props", "when click\n  x = mouse.x\n  y = mouse.y\n  visible = mouse.held\n", projEnv, ""},
		{"101 unknown call", "when start\n  foo(1)\n", nil, "TD101"},
		{"101 unknown call with use", "use \"helpers.tdsl\"\nwhen start\n  foo(1)\n", nil, ""},
		{"101 library mode silent", "to heal(a)\n  hp += a\n", projEnv, ""},
		// TD102
		{"102 dup top", "var a = 1\nvar a = 2\nwhen start\n  a = 3\n", nil, "TD102"},
		{"102 outer block", "when start\n  var a = 1\n  if yes\n    var a = 2\n", nil, "TD102"},
		{"102 top vs local", "var a = 1\nwhen start\n  var a = 2\n", nil, "TD102"},
		{"102 param", "to f(a)\n  var a = 1\n", nil, "TD102"},
		{"102 dup action", "to f()\n  print 1\nto f()\n  print 2\n", nil, "TD102"},
		{"102 sibling ok", "when start\n  if yes\n    var a = 1\n  else\n    var a = 2\n", nil, ""},
		// TD103
		{"103 prop", "var x = 5\nwhen start\n  print x\n", nil, "TD103"},
		{"103 builtin", "var min = 5\n", nil, "TD103"},
		{"103 param", "to f(text)\n  print 1\n", nil, "TD103"},
		{"103 action", "to random(a, b)\n  print 1\n", nil, "TD103"},
		{"103 mouse", "when start\n  var mouse = 1\n", nil, "TD103"},
		{"103 ok", "var speed = 5\n", nil, ""},
		// TD104
		{"104 update wait", "when update\n  wait 1 second\n", nil, "TD104"},
		{"104 held slide nested", "when key space held\n  if yes\n    slide to 1, 2 in 1 seconds\n", nil, "TD104"},
		{"104 every ok", "when every 1 second\n  wait 1 second\n", nil, ""},
		{"104 pressed ok", "when key space pressed\n  wait 1 second\n  slide to 1, 2 in 1 seconds\n", nil, ""},
		{"104 transitive", "to a()\n  b()\nto b()\n  wait 1 second\nwhen update\n  a()\n", nil, "TD104"},
		{"104 transitive in expr", "to a()\n  wait 1 second\n  return 1\nwhen update\n  var q = a()\n  print q\n", nil, "TD104"},
		{"104 call ok", "to a()\n  print 1\nwhen update\n  a()\n", nil, ""},
		{"104 call from start ok", "to a()\n  wait 1 second\nwhen start\n  a()\n", nil, ""},
		// TD105
		{"105 twice", "shared coins = 0\nshared coins = 1\n", nil, "TD105"},
		{"105 other script", "shared lives = 3\nwhen start\n  print lives\n", projEnv, "TD105"},
		{"105 ok", "shared coins = 0\nwhen start\n  coins += 1\n", projEnv, ""},
		// TD107
		{"107 name", "when start\n  name = \"x\"\n", nil, "TD107"},
		{"107 member name", "when start\n  Hero.name = \"x\"\n", projEnv, "TD107"},
		{"107 mouse", "when start\n  mouse.x = 1\n", nil, "TD107"},
		{"107 ok", "when start\n  text = \"x\"\n  Hero.tag = \"a\"\n", projEnv, ""},
		// TD201
		{"201 bad", "var score = 0\nwhen start\n  if score is \"10\"\n    print 1\n", nil, "TD201"},
		{"201 ok", "var score = 0\nwhen start\n  if score is 10 and name is \"a\"\n    print 1\n", nil, ""},
		{"201 nothing ok", "when start\n  if find(\"a\") is nothing\n    print 1\n", nil, ""},
		{"201 lt", "when start\n  if x < \"a\"\n    print 1\n", nil, "TD201"},
		// TD202
		{"202 num", "var score = 1\nwhen start\n  if score\n    print 1\n", nil, "TD202"},
		{"202 while text", "when start\n  while \"a\"\n    print 1\n", nil, "TD202"},
		{"202 and", "when start\n  if x and visible\n    print 1\n", nil, "TD202"},
		{"202 ok", "when start\n  if visible and not flip or touching(\"a\")\n    print 1\n  while key space is held\n    print 2\n", nil, ""},
		// TD203
		{"203 builtin", "when start\n  print random(1)\n", nil, "TD203"},
		{"203 touching ok", "when start\n  print touching(\"a\")\n  print touching(\"a\", down)\n", nil, ""},
		{"203 touching bad", "when start\n  print touching()\n  print touching(1, 2, 3)\n", nil, "TD203,TD203"},
		{"203 own", "to f(a, b)\n  print a\nwhen start\n  f(1)\n", nil, "TD203"},
		{"203 own ok", "to f(a, b)\n  print a\nwhen start\n  f(1, 2)\n", nil, ""},
		// TD204
		{"204 bad", "var score = 0\nwhen start\n  print \"Счёт: \" + score\n", nil, "TD204"},
		{"204 += ", "when start\n  text += 5\n", nil, "TD204"},
		{"204 ok", "var score = 0\nwhen start\n  print \"Счёт: {score}\" + \"!\"\n  print score + 1\n", nil, ""},
		// TD205
		{"205 move", "when start\n  move right by \"5\"\n", nil, "TD205"},
		{"205 move dir var", "var heading = 1\nwhen start\n  move heading by 5\n", nil, "TD205"},
		{"205 move dir ok", "var heading = left\nwhen start\n  move heading by 5\n", nil, ""},
		{"205 builtin arg", "when start\n  print abs(\"a\")\n", nil, "TD205"},
		{"205 prop assign", "when start\n  x = \"a\"\n", nil, "TD205"},
		{"205 minus", "when start\n  print \"a\" - 1\n", nil, "TD205"},
		{"205 wait", "when start\n  wait \"a\" seconds\n", nil, "TD205"},
		{"205 unknown silent", "to f(a)\n  move right by a\n  wait a seconds\n  x = a\n", nil, ""},
		{"205 retype var silent", "var a = 0\nwhen start\n  a = \"x\"\n  print a + \"y\"\n", nil, ""},
		{"205 in", "when start\n  if 5 in \"abc\"\n    print 1\n", nil, "TD205"},
		{"205 list", "when start\n  add 1 to 5\n", nil, "TD205"},
		// TD304 / TD305
		{"304 sound", "when start\n  play sound \"no.wav\"\n", projEnv, "TD304"},
		{"304 ok", "when start\n  play sound \"jump.wav\"\n  play music \"theme\"\n  image = \"hero.png\"\n", projEnv, ""},
		{"304 no env", "when start\n  play sound \"no.wav\"\n", nil, ""},
		{"304 use", "use \"nothere.tdsl\"\n", projEnv, "TD304"},
		{"304 use ok", "use \"helpers.tdsl\"\n", projEnv, ""},
		{"304 spawn", "when start\n  spawn \"Moon\" at 1, 2\n", projEnv, "TD304"},
		{"304 spawn ok", "when start\n  spawn \"Star\" at 1, 2\n", projEnv, ""},
		{"305 scene", "when start\n  go to scene \"lvl9\"\n", projEnv, "TD305"},
		{"305 ok", "when start\n  go to scene \"menu\"\n", projEnv, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkSrc(t, tc.src, tc.env)
			if got := codes(errs); got != tc.want {
				for _, e := range errs {
					t.Log(e.Format(tc.src))
				}
				t.Fatalf("коды: получено %q, ожидалось %q", got, tc.want)
			}
			for _, e := range errs {
				if e.Line < 1 || e.Col < 1 || e.EndCol <= e.Col || e.Message == "" {
					t.Errorf("некорректная ошибка: %+v", e)
				}
			}
		})
	}
}

func TestCheckHints(t *testing.T) {
	errs := checkSrc(t, "var coins = 0\nwhen start\n  Coins += 1\n", projEnv)
	if len(errs) != 1 || !strings.Contains(errs[0].Hint, "coins") || errs[0].Line != 3 {
		t.Fatalf("%v", errs)
	}
	errs = checkSrc(t, "when start\n  play sound \"jump.wva\"\n", projEnv)
	if len(errs) != 1 || !strings.Contains(errs[0].Hint, "jump.wav") {
		t.Fatalf("%v", errs)
	}
	errs = checkSrc(t, "var score = 0\nwhen start\n  print \"Счёт: \" + score\n", nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Hint, "{") {
		t.Fatalf("%v", errs)
	}
}

func TestCheckRecursion(t *testing.T) {
	src := "to a()\n  b()\nto b()\n  a()\nto c()\n  c()\n  wait 1 second\nwhen update\n  a()\n  c()\n"
	errs := checkSrc(t, src, nil)
	if codes(errs) != "TD104" {
		t.Fatalf("ожидался один TD104 (только c), получено %s", codes(errs))
	}
	if errs[0].Line != 10 {
		t.Fatalf("строка %d", errs[0].Line)
	}
}

func TestCheckLimitAndOrder(t *testing.T) {
	var b strings.Builder
	b.WriteString("when start\n")
	for i := 0; i < 80; i++ {
		b.WriteString("  print nope" + strings.Repeat("x", 1) + "\n")
	}
	errs := checkSrc(t, b.String(), &Env{Objects: []string{}})
	if len(errs) != 50 {
		t.Fatalf("ошибок %d", len(errs))
	}
	for i := 1; i < len(errs); i++ {
		if errs[i].Line < errs[i-1].Line {
			t.Fatal("не отсортированы")
		}
	}
}

func FuzzCheck(f *testing.F) {
	files, _ := filepath.Glob("testdata/*.tdsl")
	for _, name := range files {
		if src, err := os.ReadFile(name); err == nil {
			f.Add(string(src))
		}
	}
	for _, seed := range []string{
		"when update\n  wait 1 second\n", "to a()\n  a()\n  wait 1 seconds\n", "var x = 1\nvar x = 2\n",
		"when touch other\n  other = 1\n", "when start\n  print \"a\" + 1 < [1]\n", "shared a = a\n",
	} {
		f.Add(seed)
	}
	env := &Env{Objects: []string{"Hero"}, Assets: []string{"a.png"}, Scripts: []string{"h.tdsl"},
		Templates: []string{"S"}, Scenes: []string{"menu"}, SharedDeclared: map[string]string{"q": "f"}}
	f.Fuzz(func(t *testing.T, src string) {
		s, _ := Parse(src)
		for _, e := range []*Env{nil, env} {
			errs := Check(s, e)
			if len(errs) > maxCheckErrors {
				t.Fatal("слишком много ошибок")
			}
			for _, er := range errs {
				if er.Line < 1 || er.Col < 1 || er.EndCol <= er.Col || er.Code == "" || er.Message == "" {
					t.Fatalf("некорректная ошибка: %+v", er)
				}
				_ = er.Format(src)
			}
		}
	})
}
