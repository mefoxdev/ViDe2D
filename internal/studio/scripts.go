package studio

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/vide2d/vide-studio/internal/assets"
	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
)

const scriptExt = ".tdsl"

// marks returns small badges shown after a node's name.
func marks(n *scene.Node) string {
	m := ""
	if n.Script != "" {
		m += " ≡"
	}
	if n.Template {
		m += " ◊"
	}
	return m
}

// scriptFile resolves a project-relative script path to a file path. It
// returns "" if the path is not a safe relative .tdsl path.
func (s *Studio) scriptFile(rel string) string {
	if !assets.ValidRelPath(rel, scriptExt) {
		return ""
	}
	if projfs.NoLinks(s.proj.dir, rel) != nil { // no links on the way
		return ""
	}
	return projfs.Resolve(s.proj.dir, rel)
}

// readScript reads the script rel (check and open in one step, at most max bytes).
func (s *Studio) readScript(rel string, max int64) ([]byte, error) {
	if !assets.ValidRelPath(rel, scriptExt) {
		return nil, fmt.Errorf("not a valid script path %q", rel)
	}
	return projfs.ReadFile(s.proj.dir, rel, max)
}

// scriptExists reports whether rel names an existing regular file.
func (s *Studio) scriptExists(rel string) bool {
	f := s.scriptFile(rel)
	return f != "" && projfs.IsRegular(f)
}

// safeFileName turns a node name into a file name stem: letters, digits, '_'
// and '-' are kept, everything else becomes '_'.
func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "script"
	}
	return out
}

// newScript creates a starter script for n and attaches it.
func (s *Studio) newScript(n *scene.Node) error {
	if s.proj.dir == "" {
		return errors.New("this project has no folder, so scripts cannot be created")
	}
	stem := safeFileName(n.Name)
	content := fmt.Sprintf("# Script for %s\n\nwhen start\n  print \"{name} is ready\"\n", strings.NewReplacer("\n", " ", "\r", " ").Replace(n.Name))
	for i := 1; i < 1000; i++ {
		rel := stem + scriptExt
		if i > 1 {
			rel = fmt.Sprintf("%s%d%s", stem, i, scriptExt)
		}
		err := projfs.CreateNew(s.proj.dir, "scripts/"+rel, []byte(content)) // checked, exclusive, inside the project
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		n.Script = "scripts/" + rel
		return nil
	}
	return errors.New("too many scripts with this name")
}

// validTag reports whether t is acceptable as a tag: at most 64 characters,
// no control characters.
func validTag(t string) bool {
	n := 0
	for _, r := range t {
		if unicode.IsControl(r) {
			return false
		}
		n++
	}
	return n <= 64
}
