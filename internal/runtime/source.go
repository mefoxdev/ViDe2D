package runtime

import (
	"fmt"
	"os"

	"github.com/vide2d/vide-studio/internal/project"
	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
)

// Limits for what a project may make the runtime do.
const (
	MaxDocBytes      = 16 << 20 // one .vide.json file
	MaxInstanceDepth = 8        // nesting of object references
	MaxEntities      = 200_000  // live entities in the world
	MaxChunks        = 4096     // chunks in one map
)

// Source reads documents of one project folder and caches them. A cached
// document is never modified, so it is safe to expand it many times.
type Source struct {
	Root string
	// Main is the project path of the main scene (the one the game started
	// with), "" if unknown. "go to scene" can reach it by the name "main".
	Main string
	docs map[string]*scene.Scene
}

// NewSource returns a source for the project folder root.
func NewSource(root string) *Source {
	return &Source{Root: root, docs: map[string]*scene.Scene{}}
}

// SafeFile resolves the project-relative path rel to a file below the root and
// checks that it is a regular file reached without any symbolic link.
func (s *Source) SafeFile(rel string) (string, error) { return projfs.SafeFile(s.Root, rel) }

func unwrapPath(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}

// Doc loads the document at the project-relative path rel.
func (s *Source) Doc(rel string) (*scene.Scene, error) {
	if sc, ok := s.docs[rel]; ok {
		return sc, nil
	}
	sc, err := s.readDoc(rel)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	s.docs[rel] = sc
	return sc, nil
}

// readDoc checks and reads the file in one step (no gap for a link swap).
func (s *Source) readDoc(rel string) (*scene.Scene, error) {
	data, err := projfs.ReadFile(s.Root, rel, MaxDocBytes)
	if err != nil {
		return nil, err
	}
	return project.Unmarshal(data)
}

// OpenFile opens the project file rel for reading with the same rules as
// SafeFile, checking and opening in one step.
func (s *Source) OpenFile(rel string) (*os.File, error) { return projfs.OpenFile(s.Root, rel) }
