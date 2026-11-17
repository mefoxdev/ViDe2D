package runtime

import (
	"fmt"

	"github.com/vide2d/vide-studio/internal/projfs"
)

// ProjectFile is the name of a project's main scene file.
const ProjectFile = projfs.ProjectFile

// Load opens the scene document file and builds its world. The project root is
// explicitRoot if given, otherwise the folder of the file (projfs.FileRoot; nothing above it
// is ever looked at). Nothing may reach outside the root: the file
// must lie inside it, and no path may contain "..", be absolute or pass a
// symbolic link below the root.
func Load(file, explicitRoot string, opts Options) (*World, *Source, error) {
	root, rel, err := projfs.OpenRoot(file, explicitRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open %s: %w", file, err)
	}
	src := NewSource(root)
	sc, err := src.Doc(rel)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	w, err := NewWorld(src, sc, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	return w, src, nil
}
