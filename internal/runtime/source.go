package runtime

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	docs map[string]*scene.Scene
}

// NewSource returns a source for the project folder root.
func NewSource(root string) *Source {
	return &Source{Root: root, docs: map[string]*scene.Scene{}}
}

// SafeFile resolves the project-relative path rel to a file below the root and
// checks that it is a regular file reached without any symbolic link.
func (s *Source) SafeFile(rel string) (string, error) {
	file := projfs.Resolve(s.Root, rel)
	if file == "" {
		return "", fmt.Errorf("unsafe path %q", rel)
	}
	dir := s.Root
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		dir = filepath.Join(dir, p)
		st, err := os.Lstat(dir)
		if err != nil {
			return "", fmt.Errorf("%s: %w", rel, unwrapPath(err))
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s: symbolic links are not allowed", rel)
		}
		if i == len(parts)-1 && !st.Mode().IsRegular() {
			return "", fmt.Errorf("%s: not a regular file", rel)
		}
	}
	return file, nil
}

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
	file, err := s.SafeFile(rel)
	if err != nil {
		return nil, err
	}
	sc, err := readDoc(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	s.docs[rel] = sc
	return sc, nil
}

func readDoc(file string) (*scene.Scene, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, unwrapPath(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDocBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocBytes {
		return nil, fmt.Errorf("file is larger than %d bytes", MaxDocBytes)
	}
	return project.Unmarshal(data)
}
