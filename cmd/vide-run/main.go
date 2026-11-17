// Command vide-run runs a ViDe2D game in its own process.
//
//	vide-run [-root dir] <project.vide.json | scene file>
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vide2d/vide-studio/internal/runtime"
)

func main() {
	root := flag.String("root", "", "project folder (default: derived from the scene file)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: vide-run [-root dir] <project.vide.json | scene file>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	w, src, err := runtime.Load(flag.Arg(0), *root, runtime.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "vide-run:", err)
		os.Exit(1)
	}
	if err := runtime.NewGame(w, src).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "vide-run:", err)
		os.Exit(1)
	}
}
