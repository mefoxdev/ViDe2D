// Package clip is a minimal clipboard: it uses the system clipboard through
// the platform's command-line tool when one is installed (wl-clipboard, xclip,
// xsel, pbcopy/pbpaste, PowerShell) and falls back to an in-process buffer.
package clip

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

const (
	maxBytes = 1 << 20
	timeout  = 700 * time.Millisecond
)

type tool struct {
	copyCmd, pasteCmd []string
}

var (
	mu    sync.Mutex
	local string
	sys   *tool
	once  sync.Once
)

func detect() *tool {
	has := func(n string) bool { _, err := exec.LookPath(n); return err == nil }
	switch runtime.GOOS {
	case "darwin":
		if has("pbcopy") && has("pbpaste") {
			return &tool{[]string{"pbcopy"}, []string{"pbpaste"}}
		}
	case "windows":
		if has("powershell") {
			return &tool{[]string{"clip"}, []string{"powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw"}}
		}
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" && has("wl-copy") && has("wl-paste") {
			return &tool{[]string{"wl-copy"}, []string{"wl-paste", "--no-newline"}}
		}
		if has("xclip") {
			return &tool{[]string{"xclip", "-selection", "clipboard", "-i"}, []string{"xclip", "-selection", "clipboard", "-o"}}
		}
		if has("xsel") {
			return &tool{[]string{"xsel", "--clipboard", "--input"}, []string{"xsel", "--clipboard", "--output"}}
		}
	}
	return nil
}

// Set stores s in the clipboard (system clipboard when available).
func Set(s string) {
	if len(s) > maxBytes {
		s = s[:maxBytes]
	}
	mu.Lock()
	local = s
	mu.Unlock()
	once.Do(func() { sys = detect() })
	if sys == nil {
		return
	}
	cmd := exec.Command(sys.copyCmd[0], sys.copyCmd[1:]...)
	in, err := cmd.StdinPipe()
	if err != nil || cmd.Start() != nil {
		return
	}
	_, _ = in.Write([]byte(s))
	_ = in.Close()
	go cmd.Wait() // the tool may stay alive to serve the selection
}

// Get returns the clipboard contents.
func Get() string {
	once.Do(func() { sys = detect() })
	if sys != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if out, err := exec.CommandContext(ctx, sys.pasteCmd[0], sys.pasteCmd[1:]...).Output(); err == nil {
			if len(out) > maxBytes {
				out = out[:maxBytes]
			}
			return string(out)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return local
}
