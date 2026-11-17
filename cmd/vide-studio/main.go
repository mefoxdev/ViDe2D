// Command vide-studio launches the ViDe-studio editor.
package main

import (
	"log"
	"os"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/studio"
)

// usage: vide-studio [project.vide.json]
func main() {
	path := "project.vide.json"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	ebiten.SetWindowTitle("ViDe-studio")
	ebiten.SetWindowSize(1280, 760)
	ebiten.SetWindowSizeLimits(900, 560, -1, -1)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(studio.New(path)); err != nil {
		log.Fatal(err)
	}
}
