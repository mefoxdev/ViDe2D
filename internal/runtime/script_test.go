package runtime

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type logSink struct{ msgs []string }

func (l *logSink) log(m string) { l.msgs = append(l.msgs, m) }
func (l *logSink) all() string  { return strings.Join(l.msgs, "\n") }
func (l *logSink) count(sub string) int {
	n := 0
	for _, m := range l.msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

// scriptWorld writes a project with the given sprites (name -> script source)
// and loads it with scripts on.
func scriptWorld(t *testing.T, rate int, scripts map[string]string, order []string) (*World, *InputState, *logSink) {
	t.Helper()
	root := t.TempDir()
	var kids []string
	for i, name := range order {
		extra := ""
		if src, ok := scripts[name]; ok {
			write(t, root, "scripts/"+name+".tdsl", src)
			extra = fmt.Sprintf(`,"script":"scripts/%s.tdsl"`, name)
		}
		kids = append(kids, sprite(name, i*100, 0, extra))
	}
	write(t, root, "project.vide.json", doc("scene", fmt.Sprintf(`"tick_rate":%d,`, rate), strings.Join(kids, ",")))
	in, sink := NewInputState(), &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: in, Log: sink.log, Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	return w, in, sink
}

func find(w *World, name string) *Entity {
	var out *Entity
	w.Each(func(e *Entity) {
		if e.Name == name {
			out = e
		}
	})
	return out
}

func steps(t *testing.T, w *World, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := w.Step(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScriptMovesWithKeys(t *testing.T) {
	w, in, sink := scriptWorld(t, 60, map[string]string{"hero": `var speed = 60
when key right held
  move right by speed per second
when key left held
  move left by speed per second
  flip = yes
when key space pressed
  y += 10
`}, []string{"hero"})
	hero := find(w, "hero")
	steps(t, w, 5)
	if hero.X != 0 {
		t.Fatalf("moved without input: %v", hero.X)
	}
	in.Press("right")
	steps(t, w, 30)
	if hero.X != 30 {
		t.Fatalf("x = %v, want 30", hero.X)
	}
	in.Release("right")
	in.Press("left")
	steps(t, w, 10)
	if hero.X != 20 || !hero.Flip {
		t.Fatalf("x = %v flip = %v", hero.X, hero.Flip)
	}
	if len(sink.msgs) != 0 {
		t.Fatalf("log: %s", sink.all())
	}
	if hero.PrevX != 21 {
		t.Fatalf("prev = %v (interpolation base)", hero.PrevX)
	}
}

func TestKeyEdgeIsSeenByExactlyOneStep(t *testing.T) {
	w, in, _ := scriptWorld(t, 60, map[string]string{"a": "when key space pressed\n  x += 1\nwhen key space released\n  y += 1\n"}, []string{"a"})
	a := find(w, "a")
	sim := NewSim(w)
	in.Press("space")
	// three steps happen in one display frame: the edge counts once
	if _, err := sim.Update(3 * time.Second / 60); err != nil {
		t.Fatal(err)
	}
	if a.X != 1 {
		t.Fatalf("pressed fired %v times", a.X)
	}
	in.Release("space")
	if _, err := sim.Update(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if a.Y != 1 {
		t.Fatalf("released fired %v times", a.Y)
	}
	// a display frame without a step keeps the edge until a step takes it
	in.Press("space")
	if _, err := sim.Update(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if a.X != 1 {
		t.Fatal("no step ran, nothing should fire")
	}
	if _, err := sim.Update(16 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if a.X != 2 {
		t.Fatalf("edge lost: %v", a.X)
	}
}

func TestFrameOrderAcrossObjects(t *testing.T) {
	body := func(n string) string {
		return fmt.Sprintf("when update\n  print \"%s update\"\nwhen key space held\n  print \"%s input\"\nwhen start\n  print \"%s start\"\nwhen every 1 seconds\n  print \"%s timer\"\n", n, n, n, n)
	}
	w, in, sink := scriptWorld(t, 60, map[string]string{"a": body("A"), "b": body("B")}, []string{"a", "b"})
	in.Press("space")
	steps(t, w, 1)
	want := "A start\nB start\nA input\nB input\nA update\nB update"
	if got := sink.all(); got != want {
		t.Fatalf("frame 1:\n%s\nwant:\n%s", got, want)
	}
	sink.msgs = nil
	steps(t, w, 58) // the timer fires on step 60 (step 1 is done)
	sink.msgs = nil
	steps(t, w, 1)
	want = "A input\nB input\nA update\nB update\nA timer\nB timer"
	if got := sink.all(); got != want {
		t.Fatalf("frame 60:\n%s\nwant:\n%s", got, want)
	}
}

func TestBrokenScriptRunsWithoutItAndOthersWork(t *testing.T) {
	w, _, sink := scriptWorld(t, 60, map[string]string{
		"bad":  "when update\n  x += lives\n", // lives: unknown
		"good": "when update\n  x += 1\n",
		"gone": "when update\n  x += 1\n",
	}, []string{"bad", "good", "gone", "plain"})
	steps(t, w, 3)
	if find(w, "good").X != 100+3 || find(w, "bad").X != 0 {
		t.Fatalf("good=%v bad=%v", find(w, "good").X, find(w, "bad").X)
	}
	if sink.count("Скрипт scripts/bad.tdsl не запущен") != 1 || !strings.Contains(sink.all(), "TD101") {
		t.Fatalf("log: %s", sink.all())
	}
	// missing file
	w2, _, sink2 := scriptWorld(t, 60, nil, []string{"a"})
	find(w2, "a").Script = "scripts/missing.tdsl"
	w2.scripts.attach(find(w2, "a"))
	steps(t, w2, 2)
	if sink2.count("не запущен") != 1 {
		t.Fatalf("log: %s", sink2.all())
	}
}

func TestRuntimeErrorsAreLoggedOnceWithHandlerAndGameContinues(t *testing.T) {
	w, _, sink := scriptWorld(t, 60, map[string]string{
		"a": "var items = [1, 2]\nwhen update\n  x += 1\n  print items[0]\n",
	}, []string{"a"})
	steps(t, w, 100)
	if find(w, "a").X != 100 {
		t.Fatal("handler did not run each step")
	}
	if len(sink.msgs) != 3 { // first full report, then at 10 and 100 repeats
		t.Fatalf("log (%d):\n%s", len(sink.msgs), sink.all())
	}
	first := sink.msgs[0]
	for _, want := range []string{"Во время игры, scripts/a.tdsl, строка 4 (TD302)", "print items[0]", "Обработчик «when update» остановлен"} {
		if !strings.Contains(first, want) {
			t.Errorf("report lacks %q:\n%s", want, first)
		}
	}
}

func TestDestroyHappensAtEndOfFrame(t *testing.T) {
	w, in, sink := scriptWorld(t, 60, map[string]string{
		"a": "when key space pressed\n  destroy\n  print \"a still runs\"\n",
		"b": "when update\n  print \"b sees {x}\"\n",
	}, []string{"a", "b"})
	before := w.Len()
	in.Press("space")
	steps(t, w, 1)
	if sink.count("a still runs") != 1 || sink.count("b sees 100") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
	if w.Len() != before-1 || find(w, "a") != nil {
		t.Fatalf("len %d (was %d), a=%v", w.Len(), before, find(w, "a"))
	}
	steps(t, w, 2)
	if w.Len() != before-1 {
		t.Fatal("destroyed twice")
	}
}

func TestScriptsSeeDeterministicRandomFromWorldSeed(t *testing.T) {
	run := func() string {
		w, _, sink := scriptWorld(t, 60, map[string]string{"a": "when start\n  print random(1, 1000)\n  print random(1, 1000)\n"}, []string{"a"})
		steps(t, w, 1)
		return sink.all()
	}
	if a, b := run(), run(); a != b || a == "" {
		t.Fatalf("%q vs %q", a, b)
	}
}

func TestSameScriptSameResultAtAllTickRates(t *testing.T) {
	src := "var speed = 90\nwhen key right held\n  move right by speed per second\nwhen every 0.5 seconds\n  y += 1\n"
	for _, rate := range []int{60, 90, 120} {
		w, in, sink := scriptWorld(t, rate, map[string]string{"a": src}, []string{"a"})
		in.Press("right")
		sim := NewSim(w)
		for i := 0; i < 4000; i++ { // 4 s in 1 ms frames
			if _, err := sim.Update(time.Millisecond); err != nil {
				t.Fatal(err)
			}
		}
		a := find(w, "a")
		if d := a.X - 360; d > 1e-9 || d < -1e-9 || a.Y != 8 || len(sink.msgs) != 0 {
			t.Errorf("rate %d: x=%v y=%v log=%s", rate, a.X, a.Y, sink.all())
		}
	}
}

func TestPropertiesFromScripts(t *testing.T) {
	root := t.TempDir()
	write(t, root, "assets/hero.png", "x") // content never decoded in this test
	write(t, root, "scripts/a.tdsl", `when start
  text = "n{width}"
  tint = "#102030"
  tag = "good"
  font_size = 12.4
  image = "hero.png"
  visible = no
when update
  print image
  image = "missing.png"
`)
	write(t, root, "project.vide.json", doc("scene", "", sprite("a", 0, 0, `,"script":"scripts/a.tdsl"`)))
	sink := &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Log: sink.log})
	if err != nil {
		t.Fatal(err)
	}
	steps(t, w, 1)
	a := find(w, "a")
	if a.Text != "n16" || a.Color.R != 0x10 || a.Color.B != 0x30 || !a.HasTag("good") || a.FontSize != 12 || a.Texture != "assets/hero.png" || a.Visible {
		t.Fatalf("%+v", a)
	}
	if sink.count("assets/hero.png") != 1 || sink.count("TD304") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestKeyTableCoversTheSpec(t *testing.T) {
	for _, k := range []string{"left", "right", "up", "down", "space", "enter", "escape", "shift", "ctrl", "a", "m", "z", "1", "0", "f5"} {
		if !KnownKey(k) {
			t.Errorf("key %q unknown", k)
		}
	}
	if KnownKey("rigth") || KnownKey("") {
		t.Error("bad key accepted")
	}
	w, _, sink := scriptWorld(t, 60, map[string]string{"a": "when key spce pressed\n  x += 1\n"}, []string{"a"})
	steps(t, w, 1)
	if sink.count("не знаю клавишу «spce»") != 1 {
		t.Fatalf("log: %s", sink.all())
	}
}

func TestDemoProjectScriptsRun(t *testing.T) {
	root, err := filepath.Abs("../../examples/demo")
	if err != nil {
		t.Fatal(err)
	}
	in, sink := NewInputState(), &logSink{}
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: in, Log: sink.log})
	if err != nil {
		t.Fatal(err)
	}
	hero, score := find(w, "hero"), find(w, "Score")
	x0 := hero.X
	in.Press("right")
	steps(t, w, 60) // one second at speed 80
	if d := hero.X - x0 - 80; d > 1e-9 || d < -1e-9 {
		t.Fatalf("hero moved %v in a second, want 80", hero.X-x0)
	}
	in.Release("right")
	in.Press("left")
	steps(t, w, 30)
	if !hero.Flip {
		t.Fatal("flip not set")
	}
	if score.Text != "Coins: 0 of 1" {
		t.Fatalf("score text %q", score.Text)
	}
	// unsupported features are announced once, as warnings, and never crash
	all := sink.all()
	for _, want := range []string{"Предупреждение:", "touch", "message", "shared"} {
		if !strings.Contains(all, want) {
			t.Errorf("log lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "Во время игры") {
		t.Errorf("unexpected runtime error:\n%s", all)
	}
}

func TestFrameBudgetSkipsAreReportedOnce(t *testing.T) {
	scripts := map[string]string{}
	var order []string
	for i := 0; i < 8; i++ {
		n := fmt.Sprintf("s%d", i)
		order = append(order, n)
		scripts[n] = "when update\n  while yes\n    x += 1\n"
	}
	w, _, sink := scriptWorld(t, 60, scripts, order)
	steps(t, w, 2)
	// 50,000 steps each: four fill the 200,000 frame budget, four are skipped
	if sink.count("TD401") < 5 || sink.count("Пропущено запусков обработчиков: 4") != 1 {
		t.Fatalf("log:\n%s", sink.all())
	}
}

func TestInputStateEdges(t *testing.T) {
	s := NewInputState()
	s.Press("a")
	s.Press("a")
	if !s.Held("a") || !s.Pressed("a") || s.Released("a") {
		t.Fatal("after press")
	}
	s.Advance()
	if !s.Held("a") || s.Pressed("a") {
		t.Fatal("after advance")
	}
	s.Release("a")
	s.Release("a")
	if s.Held("a") || !s.Released("a") {
		t.Fatal("after release")
	}
	s.Advance()
	s.Press("b")
	s.Release("b") // both inside one gap
	if s.Held("b") || !s.Pressed("b") || !s.Released("b") {
		t.Fatal("tap")
	}
}
