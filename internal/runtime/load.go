package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vide2d/vide-studio/internal/projfs"
)

// ProjectFile is the name of a project's main scene file.
const ProjectFile = "project.vide.json"

// ProjectRoot guesses the project folder of a document file: for a scene in
// <root>/game/scenes/ it is <root>, otherwise the file's folder. This is the
// folder all project-relative paths are resolved against.
func ProjectRoot(file string) string {
	dir := filepath.Dir(file)
	slash := filepath.ToSlash(dir)
	if strings.HasSuffix(slash, "/"+projfs.ScenesDir) {
		return filepath.Dir(filepath.Dir(dir))
	}
	return dir
}

// Load opens the scene document file and builds its world. root is the project
// folder; if empty it is derived by ProjectRoot.
func Load(file, root string, opts Options) (*World, *Source, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, nil, err
	}
	st, err := os.Lstat(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open %s: %w", file, unwrapPath(err))
	}
	if !st.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular file", file)
	}
	if root == "" {
		root = ProjectRoot(abs)
	}
	sc, err := readDoc(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	src := NewSource(root)
	w, err := NewWorld(src, sc, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	return w, src, nil
}
