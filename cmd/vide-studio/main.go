// Command vide-studio launches the ViDe-studio editor.
package main

import (
	"log"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/vide2d/vide-studio/internal/studio"
)

func main() {
	ebiten.SetWindowTitle("ViDe-studio")
	ebiten.SetWindowSize(1280, 760)
	ebiten.SetWindowSizeLimits(900, 560, -1, -1)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(studio.New()); err != nil {
		log.Fatal(err)
	}
}
