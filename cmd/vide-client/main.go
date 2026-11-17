// Command vide-client is the player-facing library of ViDe2D games.
//
//	vide-client [-games dir]
//
// It lists the games in the folder (default ~/ViDe2D/games) and starts the
// selected one with vide-run, found next to this executable or in PATH.
package main

import (
	"flag"
	"fmt"
	"os"
	goruntime "runtime"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/client"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	dir := flag.String("games", client.DefaultGamesDir(), "folder with games")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("vide-client", version)
		return
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "vide-client: no games folder; use -games dir")
		os.Exit(2)
	}
	ebiten.SetWindowTitle("ViDe2D")
	ebiten.SetWindowSize(960, 600)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	sh := client.NewShell(*dir, client.ForPlatform(goruntime.GOOS))
	if err := ebiten.RunGame(sh); err != nil {
		fmt.Fprintln(os.Stderr, "vide-client:", err)
		os.Exit(1)
	}
}
