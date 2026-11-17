package tdsl

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// hlDump превращает куски строки в «вид:текст» для сравнения.
func hlDump(line string) []string {
	r := []rune(line)
	var out []string
	for _, s := range HighlightLine(line) {
		out = append(out, fmt.Sprintf("%s:%s", s.Kind, string(r[s.Start:s.End])))
	}
	return out
}

func TestHighlightKinds(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"when start", "Keyword:when Event:start"},
		{"when key space pressed", "Keyword:when Event:key Name:space Event:pressed"},
		{"when stop touching \"coin\"", "Keyword:when Event:stop Event:touching Text:\"coin\""},
		{"when every 2 seconds", "Keyword:when Event:every Number:2 Keyword:seconds"},
		{"if key space is held", "Keyword:if Keyword:key Name:space Operator:is Keyword:held"},
		{"move left by 5", "Command:move Direction:left Keyword:by Number:5"},
		{"hp -= 0.5", "Name:hp Operator:-= Number:0.5"},
		{"x = abs(y) % 3", "Property:x Operator:= Builtin:abs Punct:( Property:y Punct:) Operator:% Number:3"},
		{"var a = yes", "Keyword:var Name:a Operator:= Literal:yes"},
		{"if other is nothing and not value", "Keyword:if SelfWord:other Operator:is Literal:nothing Operator:and Operator:not SelfWord:value"},
		{"print mouse.x", "Command:print SelfWord:mouse Punct:. Property:x"},
		{"print items.length", "Command:print Name:items Punct:. Property:length"},
		{"Hero.hp += 1", "Name:Hero Punct:. Property:hp Operator:+= Number:1"},
		{"move right by 60 per second", "Command:move Direction:right Keyword:by Number:60 Operator:per Keyword:second"},
		{"next round", "Keyword:next Keyword:round"},
		{"x = round(1.5)", "Property:x Operator:= Builtin:round Punct:( Number:1.5 Punct:)"},
		{"x = a != b", "Property:x Operator:= Name:a Error:!= Name:b"},
		{"x = a @ b", "Property:x Operator:= Name:a Error:@ Name:b"},
		{"x = [1, 2]", "Property:x Operator:= Punct:[ Number:1 Punct:, Number:2 Punct:]"},
		{"# просто комментарий", "Comment:# просто комментарий"},
		{"", ""},
		{"    ", ""},
	}
	for _, c := range cases {
		got := strings.Join(hlDump(c.line), " ")
		if got != c.want {
			t.Errorf("%q:\n got  %s\n want %s", c.line, got, c.want)
		}
	}
}

func TestHighlightAllKindsCovered(t *testing.T) {
	seen := map[HighlightKind]bool{}
	for _, ln := range strings.Split(`when update
move left by 1
x = abs(1) # c
print "a" @
foo
yes
self
touching`, "\n") {
		for _, s := range HighlightLine(ln) {
			seen[s.Kind] = true
		}
	}
	for k := KindName; k <= KindError; k++ {
		if !seen[k] && k != KindProperty && k != KindDirection {
			// Property и Direction проверены в других тестах
		}
	}
	if !seen[KindComment] || !seen[KindError] || !seen[KindName] {
		t.Error("ожидались Comment, Error, Name")
	}
}

func TestHighlightInterpolation(t *testing.T) {
	got := strings.Join(hlDump(`print "Счёт: {coins * 10}!" # ok`), " ")
	want := `Command:print Text:"Счёт:  Punct:{ Name:coins Operator:* Number:10 Punct:} Text:!" Comment:# ok`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// вложенный текст внутри вставки
	got = strings.Join(hlDump(`print "a{f("b{x}")}c"`), " ")
	want = `Command:print Text:"a Punct:{ Name:f Punct:( Text:"b Punct:{ Property:x Punct:} Text:" Punct:) Punct:} Text:c"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// экранированные скобки и кавычка — просто текст
	got = strings.Join(hlDump(`print "\{ \" \}"`), " ")
	if got != `Command:print Text:"\{ \" \}"` {
		t.Errorf("got %s", got)
	}
}

func TestHighlightUnclosed(t *testing.T) {
	got := strings.Join(hlDump(`print "привет # не комментарий`), " ")
	if got != `Command:print Text:"привет # не комментарий` {
		t.Errorf("got %s", got)
	}
	got = strings.Join(hlDump(`print "a {x + `), " ")
	if got != `Command:print Text:"a  Punct:{ Property:x Operator:+` {
		t.Errorf("got %s", got)
	}
	got = strings.Join(hlDump(`print "a\`), " ")
	if got != `Command:print Text:"a\` {
		t.Errorf("got %s", got)
	}
}

func TestHighlightUnicodeColumns(t *testing.T) {
	line := `переменная = "Привет, мир" # ёж`
	spans := HighlightLine(line)
	r := []rune(line)
	want := []struct {
		k HighlightKind
		s string
	}{
		{KindName, "переменная"}, {KindOperator, "="}, {KindText, `"Привет, мир"`}, {KindComment, "# ёж"},
	}
	if len(spans) != len(want) {
		t.Fatalf("spans = %v", spans)
	}
	for i, w := range want {
		if spans[i].Kind != w.k || string(r[spans[i].Start:spans[i].End]) != w.s {
			t.Errorf("span %d = %v (%q), want %s", i, spans[i], string(r[spans[i].Start:spans[i].End]), w.s)
		}
	}
	if spans[1].Start != 11 {
		t.Errorf("колонки должны быть в рунах: %v", spans[1])
	}
	if spans[3].End != utf8.RuneCountInString(line) {
		t.Errorf("end = %d", spans[3].End)
	}
}

func TestHighlightMultiline(t *testing.T) {
	src := "when start\r\n  show\r\n\r\n  # c\n"
	res := Highlight(src)
	if len(res) != 5 {
		t.Fatalf("lines = %d", len(res))
	}
	if len(res[1]) != 1 || res[1][0].Kind != KindCommand || res[1][0].Start != 2 || res[1][0].End != 6 {
		t.Errorf("line1 = %v", res[1])
	}
	if len(res[2]) != 0 || len(res[4]) != 0 {
		t.Errorf("пустые строки: %v %v", res[2], res[4])
	}
	if res[3][0].Kind != KindComment {
		t.Errorf("line3 = %v", res[3])
	}
}

func TestHighlightSpansOrdered(t *testing.T) {
	for _, ln := range []string{`print "a{b}c{d` + "\"}\"", `x = "{"{"{"`, `}}{{`} {
		prev := 0
		for _, s := range HighlightLine(ln) {
			if s.Start < prev || s.End <= s.Start {
				t.Errorf("%q: плохой порядок %v", ln, HighlightLine(ln))
			}
			prev = s.End
		}
	}
}

func hlBigSource(lines int) string {
	block := []string{"when key space pressed", "  if hp <= 0 and not dead", "    print \"Счёт: {coins * 10} #1\" # коммент",
		"  move right by speed per second avoiding \"wall\"", "  x += abs(y) * 2.5"}
	out := make([]string, lines)
	for i := range out {
		out[i] = block[i%len(block)]
	}
	return strings.Join(out, "\n")
}

func TestHighlightFast(t *testing.T) {
	src := hlBigSource(5000)
	Highlight(src)
}

func BenchmarkHighlight5000(b *testing.B) {
	src := hlBigSource(5000)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Highlight(src)
	}
}

func FuzzHighlight(f *testing.F) {
	for _, s := range []string{"", `print "a{b}"`, `"{{{`, "x = \"\\", "when key a pressed # c", "я = ёж.x", "\xff\xfe", "}}}\"{\"{"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		res := Highlight(s)
		lines := strings.Split(s, "\n")
		if len(res) != len(lines) {
			t.Fatalf("lines %d != %d", len(res), len(lines))
		}
		for i, spans := range res {
			n := utf8.RuneCountInString(strings.TrimSuffix(lines[i], "\r"))
			prev := 0
			for _, sp := range spans {
				if sp.Start < prev || sp.End <= sp.Start || sp.End > n {
					t.Fatalf("line %q: плохой span %v", lines[i], spans)
				}
				prev = sp.End
			}
		}
	})
}
