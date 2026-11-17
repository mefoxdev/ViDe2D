package studio

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vide2d/vide-studio/internal/scene"
)

// TestHelperChild is the fake game: it is run as a subprocess of the test.
func TestHelperChild(t *testing.T) {
	mode := os.Getenv("VIDE_TEST_CHILD")
	if mode == "" {
		t.Skip("helper process")
	}
	switch mode {
	case "lines":
		fmt.Fprintln(os.Stdout, "out one")
		fmt.Fprintln(os.Stderr, "err \x1b[31mred\x1b[0m\ttab")
		fmt.Fprint(os.Stderr, strings.Repeat("x", 5000)+"\n")
		fmt.Fprint(os.Stderr, "last without newline")
	case "flood":
		for i := 0; i < 5000; i++ {
			fmt.Fprintf(os.Stderr, "line %d\n", i)
		}
	case "grandchild":
		// a grandchild inherits the pipes and outlives this process
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperChild$")
		cmd.Env = append(os.Environ(), "VIDE_TEST_CHILD=sleeper")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(4)
		}
		fmt.Fprintln(os.Stdout, "parent done")
	case "sleeper":
		time.Sleep(3 * time.Second)
	case "fail":
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(3)
	}
	os.Exit(0)
}

func runFake(t *testing.T, mode string) []string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperChild$")
	cmd.Env = append(os.Environ(), "VIDE_TEST_CHILD="+mode)
	s := &Studio{}
	o, err := startChild(cmd)
	if err != nil {
		t.Fatal(err)
	}
	s.game.running, s.game.out = true, o
	deadline := time.Now().Add(20 * time.Second)
	for s.game.running {
		if time.Now().After(deadline) {
			t.Fatal("game did not exit")
		}
		s.pollPlay()
		time.Sleep(time.Millisecond)
	}
	return consoleLines(s)
}

func consoleLines(s *Studio) []string {
	var out []string
	for _, l := range s.log {
		out = append(out, l)
	}
	return out
}

func TestPlayStreamsOutput(t *testing.T) {
	got := strings.Join(runFake(t, "lines"), "\n")
	for _, want := range []string{"[game] out one", "[game] err ", "red", "[game] last without newline", "Game exited."} {
		if !strings.Contains(got, want) {
			t.Errorf("console lacks %q:\n%s", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b\t") {
		t.Errorf("control characters leaked: %q", got)
	}
	if strings.Contains(got, strings.Repeat("x", maxLineRunes+1)) || !strings.Contains(got, "…") {
		t.Errorf("long line not cut")
	}
	if strings.LastIndex(got, "Game exited.") < strings.LastIndex(got, "last without newline") {
		t.Errorf("exit reported before the output:\n%s", got)
	}
}

func TestPlayFailureReport(t *testing.T) {
	got := strings.Join(runFake(t, "fail"), "\n")
	if !strings.Contains(got, "[game] boom") || !strings.Contains(got, "Game exited: exit status 3\nboom") {
		t.Errorf("console:\n%s", got)
	}
}

// The console keeps 200 entries, so the drop message pushes out the oldest line.
func TestPlayFloodIsBounded(t *testing.T) {
	o := &childOutput{lines: make(chan string, lineQueue), exited: make(chan string, 1)}
	for i := 0; i < 1000; i++ {
		o.lines <- fmt.Sprintf("line %d", i)
	}
	o.dropped.Store(7) // dropped by the reader because the queue was full
	s := &Studio{}
	s.game.running, s.game.out = true, o
	s.pollPlay()
	shown, dropMsg := 0, ""
	for _, l := range s.log {
		if strings.HasPrefix(l, "[game] line ") {
			shown++
		}
		if strings.Contains(l, "lines not shown") {
			dropMsg = l
		}
	}
	if shown < maxLinesPerSec-1 || dropMsg != "[game] 807 lines not shown (too much output)" {
		t.Errorf("shown %d, drop message %q", shown, dropMsg)
	}
}

// A real chatty child must finish and report its exit even though the studio
// shows only part of the output.
func TestPlayFloodChildExits(t *testing.T) {
	lines := runFake(t, "flood")
	if lines[len(lines)-1] != "Game exited." {
		t.Errorf("last console line %q", lines[len(lines)-1])
	}
}

func TestSanitizeLine(t *testing.T) {
	if got := sanitizeLine([]byte("a\x00b\x1b[2Jc\r\xff"), false); strings.ContainsAny(got, "\x00\x1b\r") || !strings.HasPrefix(got, "a b") {
		t.Errorf("%q", got)
	}
}

func TestLineLimiter(t *testing.T) {
	var l lineLimiter
	t0 := time.Now()
	n := 0
	for i := 0; i < 1000; i++ {
		if l.allow(t0) {
			n++
		}
	}
	if n != maxLinesPerSec || !l.allow(t0.Add(time.Second)) {
		t.Errorf("allowed %d", n)
	}
}

func TestStudioRefusesLinkedDocs(t *testing.T) {
	dir := t.TempDir()
	out := t.TempDir()
	s := &Studio{}
	s.proj.dir = dir
	if err := os.MkdirAll(dir+"/game/objects", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+"/o.vide.json", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, dir+"/game/objects/ext"); err != nil {
		t.Skip(err)
	}
	if _, err := s.loadDoc("game/objects/ext/o.vide.json"); err == nil || !strings.Contains(err.Error(), "symbolic") {
		t.Fatal(err)
	}
	if _, err := s.loadDoc("../o.vide.json"); err == nil {
		t.Fatal("escape accepted")
	}
	if s.scriptFile("game/objects/ext/x.tdsl") != "" {
		t.Fatal("script through a link accepted")
	}
	if got := projDirFor(dir + "/game/scenes/l.vide.json"); got != dir+"/game/scenes" {
		t.Fatal(got)
	}
}

func TestSanitizeLineStripsBidi(t *testing.T) {
	in := "a\u202Eb\u202Ac\u202D\u2066d\u2069e\u200Ff\u061C"
	if got := sanitizeLine([]byte(in), false); got != "abcdef" {
		t.Errorf("%q", got)
	}
}

// The notice about dropped lines appears at most once per second, but the
// final flush at exit always reports what is left.
func TestPlayDropNoticeRateLimited(t *testing.T) {
	o := &childOutput{lines: make(chan string, lineQueue), exited: make(chan string, 1)}
	s := &Studio{}
	s.game.running, s.game.out = true, o
	count := func() int {
		n := 0
		for _, l := range s.log {
			if strings.Contains(l, "lines not shown") {
				n++
			}
		}
		return n
	}
	for frame := 0; frame < 50; frame++ {
		o.dropped.Add(3)
		s.pollPlay()
	}
	if n := count(); n != 1 {
		t.Fatalf("%d notices in one second, want 1", n)
	}
	if s.game.pendingDropped != 49*3 {
		t.Fatalf("pending %d", s.game.pendingDropped)
	}
	s.game.noticeAt = time.Now().Add(-2 * time.Second) // a second later
	s.pollPlay()
	if n := count(); n != 2 || s.game.pendingDropped != 0 {
		t.Fatalf("notices %d pending %d", n, s.game.pendingDropped)
	}
	o.dropped.Add(5)
	o.exited <- "Game exited."
	s.pollPlay() // exit: forced flush
	last := s.log[len(s.log)-2]
	if count() != 3 || !strings.Contains(last, "5 lines not shown") || s.game.running {
		t.Fatalf("log %v", s.log)
	}
}

// A grandchild keeping the pipes open must not keep the game "running".
func TestPlayGrandchildHoldsPipe(t *testing.T) {
	old := drainTimeout
	drainTimeout = 200 * time.Millisecond
	defer func() { drainTimeout = old }()
	start := time.Now()
	got := strings.Join(runFake(t, "grandchild"), "\n")
	if !strings.Contains(got, "parent done") || !strings.Contains(got, "Game exited.") {
		t.Errorf("console:\n%s", got)
	}
	if time.Since(start) > 2500*time.Millisecond {
		t.Errorf("waited for the grandchild: %v", time.Since(start))
	}
}

func TestNestedSceneRefusesPlay(t *testing.T) {
	s := &Studio{path: "/p/game/scenes/x.vide.json", scene: scene.New("x")}
	s.proj.dir = projDirFor(s.path)
	s.play()
	got := strings.Join(s.log, "\n")
	if !strings.Contains(got, "game/scenes") || !strings.Contains(got, "-root /p") || s.game.running {
		t.Errorf("log:\n%s", got)
	}
}

func TestStudioDocSizeLimitAndLinkedWrites(t *testing.T) {
	dir, out := t.TempDir(), t.TempDir()
	s := &Studio{}
	s.proj.dir = dir
	big := dir + "/big.vide.json"
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(16<<20 + 1) // sparse
	f.Close()
	if _, err := s.loadDoc("big.vide.json"); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatal("oversize document accepted:", err)
	}
	if _, err := s.loadMain(big); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatal("oversize main document accepted:", err)
	}
	if _, err := s.loadMain(dir + "/none.vide.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing main must report ErrNotExist:", err)
	}
	// new documents and scripts never go through a link
	if err := os.Symlink(out, dir+"/game"); err != nil {
		t.Skip(err)
	}
	if err := s.newDocFile(scene.DocObject, "Evil"); err == nil {
		t.Fatal("document created through a link")
	}
	if err := os.Symlink(out, dir+"/scripts"); err != nil {
		t.Skip(err)
	}
	if err := s.newScript(scene.New("n").Root); err == nil {
		t.Fatal("script created through a link")
	}
	if des, _ := os.ReadDir(out); len(des) != 0 {
		t.Fatalf("something was written outside the project: %v", des)
	}
}
