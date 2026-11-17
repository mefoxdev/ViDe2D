// Package mobile is the entry point for ebitenmobile (Android, iOS): it runs
// the client shell in-process, because mobile has no child processes.
//
// ebitenmobile requires an exported function in the bound package, and the
// game to be registered from init. The host app calls SetGamesDir before the
// Ebitengine view starts, with a folder inside its private storage.
package mobile

import (
	goruntime "runtime"

	"github.com/hajimehoshi/ebiten/v2/mobile"

	"github.com/vide2d/vide-studio/internal/client"
)

var shell = client.NewShell("", client.ForPlatform(goruntime.GOOS))

func init() { mobile.SetGame(shell) }

// SetGamesDir tells the client where the games are. The host app owns the
// folder (on Android: getFilesDir()/games).
func SetGamesDir(dir string) { shell.Dir = dir }

// Dummy forces the package to be linked; ebitenmobile needs an exported function.
func Dummy() {}
