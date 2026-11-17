package studio

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
)

// player runs the game in a separate vide-run process. At most one game runs
// at a time. The process is fully independent: if it crashes the studio only
// logs the exit.
type player struct {
	running bool
	out     *childOutput
	limit   lineLimiter

	pendingDropped int64     // lines dropped but not yet announced
	noticeAt       time.Time // when "N lines not shown" was last printed
}

// runnerPath finds vide-run next to the studio executable, then in PATH.
func runnerPath() (string, error) {
	const name = "vide-run"
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, nil
		}
	}
	return exec.LookPath(name)
}

// nestedProblem explains why the project root (the folder of the opened file)
// is unusable because the file is a scene, object or map inside game/; "" if
// all is well.
func (s *Studio) nestedProblem() string {
	if s.path == "" {
		return ""
	}
	return projfs.NestedHint(s.path)
}

// play saves the current scene if needed and starts it in the runtime.
func (s *Studio) play() {
	if s.scene.Type != scene.DocScene {
		s.Logf("Play runs scenes. Open a scene tab (this one is %s: it is used by a scene, not run on its own).", s.scene.Type)
		return
	}
	if s.game.running {
		s.Logf("A game is already running; close its window first.")
		return
	}
	if s.path == "" {
		s.Logf("Play: this scene has no file (saving is disabled).")
		return
	}
	if msg := s.nestedProblem(); msg != "" {
		s.Logf("Play: %s", msg) // the game would start with an empty project
		return
	}
	// The game reads every document from disk, so save all open tabs that
	// have changes: the scene itself and the objects and maps it uses.
	s.computeDirty()
	s.stash()
	for _, d := range s.docs {
		if d.path == "" || !d.dirty {
			continue
		}
		if err := s.saveDoc(d); err != nil {
			s.Logf("Play: cannot save %s (%v), not starting.", d.name(), err)
			return
		}
		s.Logf("Saved %s before starting.", d.name())
	}
	s.saved, s.dirty = s.docs[s.cur].saved, false
	exe, err := runnerPath()
	if err != nil {
		s.Logf("Play: vide-run not found next to the studio or in PATH (build it: go build ./cmd/vide-run).")
		return
	}
	cmd := exec.Command(exe, "-root", s.proj.dir, s.path)
	cmd.Dir = s.proj.dir
	out, err := startChild(cmd)
	if err != nil {
		s.Logf("Play: cannot start %s: %v", exe, err)
		return
	}
	s.game.running = true
	s.game.out = out
	s.game.limit = lineLimiter{}
	s.game.pendingDropped, s.game.noticeAt = 0, time.Time{}
	s.Logf("Game started (pid %d): %s", cmd.Process.Pid, filepath.Base(s.path))
}

// Limits for the game's console output.
const (
	maxLineRunes   = 300  // longer lines are cut
	maxLinesPerSec = 200  // more lines than this per second are dropped
	lineQueue      = 1024 // lines waiting for the studio between frames
)

// childOutput carries a running game's output and exit to the studio. The
// reader goroutines only write to channels and counters; the console is only
// touched from Update (pollPlay).
type childOutput struct {
	lines   chan string // sanitized lines, stdout and stderr, in arrival order per stream
	exited  chan string // exit report; sent after all output has been read
	dropped atomic.Int64
}

// drainTimeout is how long, after the child has exited, the studio still
// reads its output. A grandchild that inherited the pipes and keeps running
// must not keep the studio waiting: the pipes are closed after this time.
var drainTimeout = 500 * time.Millisecond

// startChild starts cmd and streams its stdout and stderr line by line. The
// pipes are our own (os.Pipe), so the end of the child does not depend on the
// end of the streams.
func startChild(cmd *exec.Cmd) (*childOutput, error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	err = cmd.Start()
	outW.Close() // the child has its own copies; EOF comes when all of them are gone
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return nil, err
	}
	o := &childOutput{lines: make(chan string, lineQueue), exited: make(chan string, 1)}
	errTail := &tailBuffer{max: 2000}
	var wg sync.WaitGroup
	for _, st := range []struct {
		r    io.Reader
		tail *tailBuffer
	}{{outR, nil}, {errR, errTail}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.read(st.r, st.tail)
		}()
	}
	go func() {
		waitErr := cmd.Wait()
		// The child is gone. Let the readers finish what is in the pipes, but
		// not wait forever for a grandchild that still holds them open.
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(drainTimeout):
			outR.Close() // unblocks the readers (they end when the pipe is closed)
			errR.Close()
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
			}
		}
		outR.Close()
		errR.Close()
		if waitErr != nil {
			msg := fmt.Sprintf("Game exited: %v", waitErr)
			if t := strings.TrimSpace(errTail.String()); t != "" {
				msg += "\n" + t
			}
			o.exited <- msg
		} else {
			o.exited <- "Game exited."
		}
	}()
	return o, nil
}

// read splits r into lines of at most maxLineRunes (the rest of a long line is
// skipped) and queues them without ever blocking: when the queue is full the
// line is counted as dropped, so a chatty game cannot stall itself on the pipe.
func (o *childOutput) read(r io.Reader, tail *tailBuffer) {
	br := bufio.NewReaderSize(r, 4096)
	for {
		raw, more, err := readLimited(br, maxLineRunes*4)
		if len(raw) > 0 || more || err == nil {
			line := sanitizeLine(raw, more)
			if tail != nil {
				tail.Write([]byte(line + "\n"))
			}
			select {
			case o.lines <- line:
			default:
				o.dropped.Add(1)
			}
		}
		if err != nil {
			return
		}
	}
}

// readLimited reads one line, keeping at most limit bytes of it. more reports
// that the line was longer. err is non-nil at the end of the stream.
func readLimited(br *bufio.Reader, limit int) (line []byte, more bool, err error) {
	for {
		chunk, isPrefix, e := br.ReadLine()
		if room := limit - len(line); room > 0 {
			if len(chunk) > room {
				chunk, more = chunk[:room], true
			}
			line = append(line, chunk...)
		} else if len(chunk) > 0 {
			more = true
		}
		if e != nil {
			return line, more, e
		}
		if !isPrefix {
			return line, more, nil
		}
	}
}

// sanitizeLine makes a child's output safe to show: invalid UTF-8 and control
// characters (including escape sequences' ESC) become spaces or U+FFFD,
// bidirectional override characters are removed, and the line is cut to
// maxLineRunes.
func sanitizeLine(raw []byte, cut bool) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.ToValidUTF8(string(raw), "\uFFFD") {
		if n >= maxLineRunes {
			cut = true
			break
		}
		if isBidi(r) {
			continue // could reorder the console text (spoofing)
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	if cut {
		b.WriteString("…")
	}
	return b.String()
}

// isBidi reports the invisible bidirectional formatting characters: embeddings
// and overrides U+202A-202E, isolates U+2066-2069, and the marks U+200E, U+200F
// and U+061C.
func isBidi(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F || r == 0x061C
}

// lineLimiter allows at most maxLinesPerSec lines per one-second window.
type lineLimiter struct {
	start time.Time
	n     int
}

func (l *lineLimiter) allow(now time.Time) bool {
	if now.Sub(l.start) >= time.Second || now.Before(l.start) {
		l.start, l.n = now, 0
	}
	if l.n >= maxLinesPerSec {
		return false
	}
	l.n++
	return true
}

// pollPlay shows the game's output and reports a finished game; call once per
// frame.
func (s *Studio) pollPlay() {
	if !s.game.running {
		return
	}
	g := &s.game
	o := g.out
	// The "not shown" notice is printed at most once per second (and at the
	// end), so it cannot itself flood the console.
	flush := func(force bool) {
		g.pendingDropped += o.dropped.Swap(0)
		now := time.Now()
		if g.pendingDropped > 0 && (force || now.Sub(g.noticeAt) >= time.Second || now.Before(g.noticeAt)) {
			s.Logf("[game] %d lines not shown (too much output)", g.pendingDropped)
			g.pendingDropped, g.noticeAt = 0, now
		}
	}
	drain := func() {
		for {
			select {
			case line := <-o.lines:
				if g.limit.allow(time.Now()) {
					s.Logf("[game] %s", line)
				} else {
					g.pendingDropped++
				}
			default:
				return
			}
		}
	}
	drain()
	select {
	case msg := <-o.exited:
		drain() // everything was queued before the exit report
		flush(true)
		g.running = false
		g.out = nil
		s.Logf("%s", msg)
	default:
		flush(false)
	}
}

// tailBuffer keeps the last max bytes written to it (the child's stderr).
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
