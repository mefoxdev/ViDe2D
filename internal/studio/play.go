package studio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vide2d/vide-studio/internal/scene"
)

// player runs the game in a separate vide-run process. At most one game runs
// at a time. The process is fully independent: if it crashes the studio only
// logs the exit.
type player struct {
	running bool
	exited  chan string // exit report from the waiting goroutine
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
	errTail := &tailBuffer{max: 2000}
	cmd.Stderr = errTail
	if err := cmd.Start(); err != nil {
		s.Logf("Play: cannot start %s: %v", exe, err)
		return
	}
	s.game.running = true
	s.game.exited = make(chan string, 1)
	done := s.game.exited
	go func() {
		err := cmd.Wait()
		if err != nil {
			msg := fmt.Sprintf("Game exited: %v", err)
			if t := strings.TrimSpace(errTail.String()); t != "" {
				msg += "\n" + t
			}
			done <- msg
		} else {
			done <- "Game exited."
		}
	}()
	s.Logf("Game started (pid %d): %s", cmd.Process.Pid, filepath.Base(s.path))
}

// pollPlay reports a finished game to the console; call once per frame.
func (s *Studio) pollPlay() {
	if !s.game.running {
		return
	}
	select {
	case msg := <-s.game.exited:
		s.game.running = false
		s.Logf("%s", msg)
	default:
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
