// Package projfs scans a project folder and classifies its files by what the
// game can use them for: code, images, sounds and scenes.
package projfs

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Kind says what a file is good for in a game.
type Kind int

const (
	KindOther Kind = iota // a file the game cannot use
	KindDir
	KindCode   // TrueDSL script (.tdsl)
	KindImage  // .png
	KindSound  // .wav .ogg .mp3
	KindScene  // .vide.json (a scene: the finished product)
	KindObject // .vide.json under game/objects/ (building block, importable)
	KindMap    // .vide.json under game/maps/ (map made of chunks)
)

// Standard folders inside a project for game documents.
const (
	ObjectsDir = "game/objects"
	ScenesDir  = "game/scenes"
	MapsDir    = "game/maps"
	DocExt     = ".vide.json"
)

func (k Kind) String() string {
	return [...]string{"file", "folder", "code", "image", "sound", "scene", "object", "map"}[k]
}

// Usable reports whether the game can use files of this kind.
func (k Kind) Usable() bool { return k >= KindCode }

// Limits keep scans fast on huge folders.
const (
	MaxEntries = 5000
	MaxDepth   = 8
)

// Entry is a file or folder. Path is slash-separated and relative to the root.
type Entry struct {
	Name     string
	Path     string
	Kind     Kind
	Size     int64
	Depth    int // 0 for the root's children
	Children []*Entry
}

// Classify returns the kind of a regular file by its name alone; every
// ".vide.json" counts as a scene. Use ClassifyPath when the folder is known.
func Classify(name string) Kind {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, DocExt) {
		return KindScene
	}
	switch path.Ext(lower) {
	case ".tdsl":
		return KindCode
	case ".png":
		return KindImage
	case ".wav", ".ogg", ".mp3":
		return KindSound
	}
	return KindOther
}

// ClassifyPath classifies a file by its project-relative path: documents under
// game/objects are objects, under game/maps are maps, other ".vide.json" files
// are scenes.
func ClassifyPath(rel string) Kind {
	k := Classify(path.Base(rel))
	if k == KindScene {
		switch {
		case strings.HasPrefix(rel, ObjectsDir+"/"):
			return KindObject
		case strings.HasPrefix(rel, MapsDir+"/"):
			return KindMap
		}
	}
	return k
}

// Scan lists root's contents as a tree. Hidden entries (names starting with
// '.'), symbolic links and special files are skipped. Folders come first, then
// files, each sorted by name ignoring case. A missing root gives an empty tree.
// The second result reports whether the limits cut the listing short.
func Scan(root string) (top []*Entry, truncated bool) {
	if root == "" {
		return nil, false
	}
	count := 0
	var walk func(dir, rel string, depth int) []*Entry
	walk = func(dir, rel string, depth int) []*Entry {
		des, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var out []*Entry
		for _, de := range des {
			name := de.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if count >= MaxEntries {
				truncated = true
				break
			}
			e := &Entry{Name: name, Path: path.Join(rel, name), Depth: depth}
			switch {
			case de.IsDir():
				e.Kind = KindDir
				count++
				if depth+1 >= MaxDepth {
					truncated = true
				} else {
					e.Children = walk(filepath.Join(dir, name), e.Path, depth+1)
				}
			case de.Type().IsRegular():
				e.Kind = ClassifyPath(e.Path)
				if info, err := de.Info(); err == nil {
					e.Size = info.Size()
				}
				count++
			default:
				continue // symlinks, devices, sockets
			}
			out = append(out, e)
		}
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if (a.Kind == KindDir) != (b.Kind == KindDir) {
				return a.Kind == KindDir
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
		return out
	}
	return walk(root, "", 0), truncated
}

// Walk visits entries depth-first in display order.
func Walk(top []*Entry, fn func(*Entry)) {
	for _, e := range top {
		fn(e)
		Walk(e.Children, fn)
	}
}

// Files returns every usable file of the given kinds (all usable kinds if none given).
func Files(top []*Entry, kinds ...Kind) []*Entry {
	var out []*Entry
	Walk(top, func(e *Entry) {
		if e.Kind == KindDir || !e.Kind.Usable() {
			return
		}
		if len(kinds) == 0 {
			out = append(out, e)
			return
		}
		for _, k := range kinds {
			if e.Kind == k {
				out = append(out, e)
				return
			}
		}
	})
	return out
}

// ValidRel reports whether p is a safe project-relative path: slash-separated,
// local (no "..", not absolute), no backslashes or NUL, already clean.
func ValidRel(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	return path.Clean(p) == p && filepath.IsLocal(filepath.FromSlash(p))
}

// Resolve turns a project-relative path into a file path under root. It
// returns "" for unsafe paths.
func Resolve(root, rel string) string {
	if root == "" || !ValidRel(rel) {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// IsRegular reports whether file exists and is a regular file (not a symlink).
func IsRegular(file string) bool {
	st, err := os.Lstat(file)
	return err == nil && st.Mode().IsRegular()
}
