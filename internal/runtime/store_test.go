package runtime

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

func TestStoreRoundTripThroughAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "saves")
	root := filepath.Join(t.TempDir(), "My Game")
	os.MkdirAll(root, 0o755)
	var lines []string
	log := func(m string) { lines = append(lines, m) }
	s := NewFileStore(dir, root, log)
	vals := map[string]vm.Value{
		"n": vm.Num(0.1 + 0.2), "big": vm.Num(1e21), "neg": vm.Num(-3), "t": vm.Text("привет \"мир\"\n"), "yes": vm.Bool(true), "no": vm.Bool(false),
		"list": vm.NewList(vm.Num(1), vm.Text("two"), vm.Bool(true), vm.NewList(vm.Num(3))), "empty": vm.NewList(),
	}
	for k, v := range vals {
		if _, err := s.Set(k, v); err != nil {
			t.Fatal(k, err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s2 := NewFileStore(dir, root, log)
	for k, want := range vals {
		got, ok, _ := s2.Get(k)
		if !ok || got.String() != want.String() || got.K != want.K {
			t.Errorf("%s: %v %v", k, got, ok)
		}
	}
	if got, _, _ := s2.Get("n"); math.Float64bits(got.N) != math.Float64bits(0.1+0.2) {
		t.Error("floats must round-trip exactly")
	}
	if len(lines) != 0 {
		t.Fatalf("%v", lines)
	}
	// the file is deterministic (sorted keys) and written atomically: no temp files remain
	b1, _ := os.ReadFile(s.Path())
	s2.dirty = true
	s2.Flush()
	b2, _ := os.ReadFile(s.Path())
	if string(b1) != string(b2) || !strings.Contains(string(b1), `"version":1`) {
		t.Fatalf("%s\n%s", b1, b2)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("%v", ents)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("saves folder mode %v", fi.Mode().Perm())
	}
}

func TestStoreValuesAreCopies(t *testing.T) {
	m := NewMemStore()
	l := vm.NewList(vm.Num(1), vm.NewList(vm.Num(2)))
	m.Set("l", l)
	l.L.Items[0] = vm.Num(99)
	got, _, _ := m.Get("l")
	if got.L.Items[0].N != 1 {
		t.Fatal("the store shares the list with the script")
	}
	got.L.Items[1].L.Items[0] = vm.Num(77)
	got2, _, _ := m.Get("l")
	if got2.L.Items[1].L.Items[0].N != 2 {
		t.Fatal("what recall returned is the store's own list")
	}
}

func TestStoreRejectsWhatCannotBeStored(t *testing.T) {
	m := NewMemStore()
	cyc := vm.NewList()
	cyc.L.Items = append(cyc.L.Items, cyc)
	deep := vm.NewList(vm.Num(1))
	for i := 0; i < 10; i++ {
		deep = vm.NewList(deep)
	}
	for name, v := range map[string]vm.Value{"nothing": {}, "dir": vm.DirValue(vm.Left), "obj": vm.Obj(3), "nan": vm.Num(math.NaN()),
		"inf": vm.Num(math.Inf(1)), "cycle": cyc, "deep": deep, "inlist": vm.NewList(vm.Obj(1))} {
		if _, err := m.Set("k", v); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, key := range []string{"", strings.Repeat("k", MaxStoreKeyChars+1)} {
		if _, err := m.Set(key, vm.Num(1)); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
	if m.Len() != 0 {
		t.Fatal("a failed Set changed the store")
	}
}

func TestStoreLimits(t *testing.T) {
	m := NewMemStore()
	for i := 0; i < MaxStoreKeys; i++ {
		if _, err := m.Set(fmt.Sprintf("k%d", i), vm.Num(float64(i))); err != nil {
			t.Fatal(i, err)
		}
	}
	_, err := m.Set("one more", vm.Num(1))
	he, ok := err.(*vm.HostError)
	if !ok || he.Code != "TD402" || !strings.Contains(he.Message, "1000") {
		t.Fatalf("%v", err)
	}
	if _, err := m.Set("k5", vm.Num(55)); err != nil { // replacing an existing key is fine at the limit
		t.Fatal(err)
	}
	// size: a text of 10 000 characters is 10 KB; about 100 fit in 1 MB
	m = NewMemStore()
	text := vm.Text(strings.Repeat("я", 5000)) // 10 KB in UTF-8 (and escaped in JSON as is)
	n := 0
	for ; n < 200; n++ {
		if _, err := m.Set(fmt.Sprintf("t%d", n), text); err != nil {
			he := err.(*vm.HostError)
			if he.Code != "TD402" || !strings.Contains(he.Message, "1 МБ") {
				t.Fatal(err)
			}
			break
		}
	}
	if n < 90 || n > 110 {
		t.Fatalf("stored %d texts of 10 KB before the 1 MB limit", n)
	}
	// a replaced value frees its space
	if _, err := m.Set("t0", vm.Num(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Set("again", text); err != nil {
		t.Fatalf("space of the replaced value was not given back: %v", err)
	}
}

func TestStoreFileIsReadSafely(t *testing.T) {
	dir := t.TempDir()
	root := "/some/project"
	name := StoreID(root) + ".json"
	var lines []string
	log := func(m string) { lines = append(lines, m) }
	for _, content := range []string{"{ nope", `{"version":2,"values":{}}`, `[]`, ""} {
		lines = nil
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
		s := NewFileStore(dir, root, log)
		if s.Len() != 0 || len(lines) != 1 {
			t.Errorf("%q: len %d, log %v", content, s.Len(), lines)
		}
	}
	// bad entries are dropped, good ones kept, the keys over the limit are dropped in key order
	lines = nil
	os.WriteFile(filepath.Join(dir, name), []byte(`{"version":1,"values":{"a":1,"b":null,"c":{"x":1},"d":[1,[2]],"e":"t"}}`), 0o600)
	s := NewFileStore(dir, root, log)
	if s.Len() != 3 || len(lines) != 1 || !strings.Contains(lines[0], "2 записи") {
		t.Fatalf("%v %v", s.Keys(), lines)
	}
	// a huge file is refused without reading it all
	lines = nil
	os.WriteFile(filepath.Join(dir, name), []byte(`{"version":1,"values":{"a":"`+strings.Repeat("x", maxStoreFile)+`"}}`), 0o600)
	s = NewFileStore(dir, root, log)
	if s.Len() != 0 || len(lines) != 1 {
		t.Fatalf("%d %v", s.Len(), lines)
	}
	// a symbolic link in place of the file is not followed
	lines = nil
	outside := filepath.Join(t.TempDir(), "o.json")
	os.WriteFile(outside, []byte(`{"version":1,"values":{"stolen":1}}`), 0o600)
	os.Remove(filepath.Join(dir, name))
	if err := os.Symlink(outside, filepath.Join(dir, name)); err == nil {
		s = NewFileStore(dir, root, log)
		if s.Len() != 0 {
			t.Fatal("followed a link")
		}
	}
}

func TestStoreIDIsStableAndTellsProjectsApart(t *testing.T) {
	a, b := StoreID("/home/u/games/space"), StoreID("/home/u/other/space")
	if a == b || !strings.HasPrefix(a, "space-") || a != StoreID("/home/u/games/space") || a != StoreID("/home/u/games/space/") {
		t.Fatalf("%q %q", a, b)
	}
	weird := StoreID("/x/../etc/Мой игра: ?*/")
	if strings.ContainsAny(weird, `/\:*?. `) {
		t.Fatalf("%q", weird)
	}
	if id := StoreID("/"); !strings.HasPrefix(id, "game-") {
		t.Fatalf("%q", id)
	}
	if long := StoreID("/" + strings.Repeat("n", 300)); len(long) > 60 {
		t.Fatalf("%q", long)
	}
}

func TestRememberAndRecallFromScripts(t *testing.T) {
	g := newGP(t)
	g.script("a", "when key a pressed\n  remember \"score\" as [10, \"x\", yes]\n  remember \"name\" as \"Лис\"\nwhen key b pressed\n  print recall(\"score\", 0)\n  print recall(\"name\", \"?\")\n  print recall(\"none\", 42)\n")
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	g.in.Press("a")
	simSteps(t, sim, 1)
	g.in.Release("a")
	// "a new run of the game" shares only the store
	sim2 := g.sim()
	g.in.Press("b")
	simSteps(t, sim2, 1)
	if got := g.sink.join(""); got != `[10, "x", yes]|Лис|42` {
		t.Fatalf("%q", got)
	}
	mustNoErrors(t, g.sink)
}

func TestRememberLimitsFromScripts(t *testing.T) {
	g := newGP(t)
	g.script("a", "when start\n  var i = 0\n  repeat 1100 times\n    i += 1\n    remember \"k{i}\" as i\n")
	g.scene("", 60, 320, 180, sprite("a", 0, 0, scr("a")))
	sim := g.sim()
	simSteps(t, sim, 1)
	if n := g.sink.count("TD402"); n != 1 || g.store.(*MemStore).Len() != MaxStoreKeys {
		t.Fatalf("%d errors, %d keys\n%s", n, g.store.(*MemStore).Len(), g.sink.all())
	}
}

func TestMemoryIsSavedOncePerSecondAndOnTheWay(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	write(t, root, "scripts/a.tdsl", "when start\n  remember \"x\" as 1\nwhen key a pressed\n  remember \"y\" as 2\n  go to scene \"two\"\n")
	write(t, root, "project.vide.json", doc("scene", `"tick_rate":60,`, sprite("a", 0, 0, scr("a"))))
	write(t, root, "game/scenes/two.vide.json", doc("scene", "", ""))
	st := NewFileStore(dir, root, nil)
	in := NewInputState()
	w, _, err := Load(filepath.Join(root, "project.vide.json"), root, Options{Scripts: true, Input: in, Store: st, Log: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	sim := NewSim(w)
	simSteps(t, sim, 59)
	if _, err := os.Stat(st.Path()); err == nil {
		t.Fatal("saved before a second passed")
	}
	simSteps(t, sim, 1)
	if NewFileStore(dir, root, nil).Len() != 1 {
		t.Fatal("not saved after a second")
	}
	in.Press("a")
	simSteps(t, sim, 1) // go to scene: saved at once
	if NewFileStore(dir, root, nil).Len() != 2 {
		t.Fatal("not saved on the scene change")
	}
	_ = w
}
