package client

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/runtime"
)

// Launcher starts a game from the library.
//
// An external launcher starts the game elsewhere (a child process) and returns
// a nil game: the shell stays on screen. An in-process launcher returns the
// game, and the shell hands the window over to it until it ends.
type Launcher interface {
	Launch(e Entry) (ebiten.Game, error)
}

// RuntimeName is the runtime executable the ProcessLauncher starts.
const RuntimeName = "vide-run"

// ProcessLauncher runs the game in a child vide-run process (desktop).
type ProcessLauncher struct {
	// Exe is the runtime executable; empty means look it up with FindRuntime.
	Exe string
	// Start runs the command; tests replace it. Default: cmd.Start plus a
	// goroutine reaping the child.
	Start func(cmd *exec.Cmd) error
}

// Launch starts vide-run for e. It does not wait for the game to end.
func (p *ProcessLauncher) Launch(e Entry) (ebiten.Game, error) {
	if e.Err != "" {
		return nil, errors.New(e.Err)
	}
	exe := p.Exe
	if exe == "" {
		var err error
		if exe, err = FindRuntime(os.Executable, exec.LookPath, goruntime.GOOS); err != nil {
			return nil, err
		}
	}
	args := []string{}
	if e.Root != "" {
		args = append(args, "-root", e.Root)
	}
	args = append(args, "--", e.File) // "--": a file name can never be taken for a flag
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	start := p.Start
	if start == nil {
		start = func(c *exec.Cmd) error {
			if err := c.Start(); err != nil {
				return err
			}
			go c.Wait()
			return nil
		}
	}
	if err := start(cmd); err != nil {
		return nil, fmt.Errorf("cannot start %s: %w", exe, err)
	}
	return nil, nil
}

// FindRuntime looks for vide-run next to the client executable, then in PATH.
// The lookups are parameters so the rule can be tested.
func FindRuntime(self func() (string, error), lookPath func(string) (string, error), goos string) (string, error) {
	name := RuntimeName
	if goos == "windows" {
		name += ".exe"
	}
	if p, err := self(); err == nil {
		cand := filepath.Join(filepath.Dir(p), name)
		if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
			return cand, nil
		}
	}
	if p, err := lookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found next to the client or in PATH", name)
}

// InProcessLauncher loads the game into this process (mobile, where a child
// process is not possible).
type InProcessLauncher struct {
	Log func(string)
}

// Launch loads e and returns the runtime game for the shell to run.
func (l InProcessLauncher) Launch(e Entry) (ebiten.Game, error) {
	if e.Err != "" {
		return nil, errors.New(e.Err)
	}
	w, src, err := runtime.Load(e.File, e.Root, runtime.Options{Scripts: true, Seed: uint64(time.Now().UnixNano()), Log: l.Log})
	if err != nil {
		return nil, err
	}
	return runtime.NewGame(w, src), nil
}

// ForPlatform picks the launcher: in-process on Android and iOS, a child
// process elsewhere.
func ForPlatform(goos string) Launcher {
	if goos == "android" || goos == "ios" {
		return InProcessLauncher{}
	}
	return &ProcessLauncher{}
}
