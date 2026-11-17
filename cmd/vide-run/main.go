// Command vide-run runs a ViDe2D game in its own process.
//
//	vide-run [-root dir] [-seed n] <project.vide.json | scene file>
//
// TrueDSL scripts run; "print" output and script errors go to stderr. Escape
// is an ordinary key for the scripts; quit with Ctrl+Q or by closing the window.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/runtime"
)

func main() {
	root := flag.String("root", "", "project root; must contain the scene file (default: the scene file's folder)")
	seed := flag.Uint64("seed", 0, "random seed for scripts, 0 is a valid seed (default: taken from the clock and printed, so a game can be replayed)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vide-run [-root dir] [-seed n] <project.vide.json | scene file>")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "quit: Ctrl+Q or close the window (Escape is passed to the scripts)")
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if *root == "" {
		// Without -root the scene's folder is the root. For a scene inside
		// game/scenes that would silently give a project without its objects:
		// refuse with a clear message instead.
		if msg := projfs.NestedHint(flag.Arg(0)); msg != "" {
			fmt.Fprintln(os.Stderr, "vide-run:", msg)
			os.Exit(2)
		}
	}
	seedGiven := false
	flag.Visit(func(f *flag.Flag) { seedGiven = seedGiven || f.Name == "seed" })
	if !seedGiven {
		// The clock is read here, at the edge, never inside the script engine.
		*seed = uint64(time.Now().UnixNano())
		fmt.Fprintln(os.Stderr, "vide-run: random seed", *seed)
	}
	w, src, err := runtime.Load(flag.Arg(0), *root, runtime.Options{
		Scripts: true,
		Seed:    *seed,
		Log:     func(msg string) { fmt.Fprintln(os.Stderr, msg) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "vide-run:", err)
		os.Exit(1)
	}
	if err := runtime.NewGame(w, src).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "vide-run:", err)
		os.Exit(1)
	}
}
