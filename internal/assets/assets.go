// Package assets safely decodes image files of a project. Paths are
// slash-separated and relative to the project folder.
package assets

import (
	"errors"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder
	"io"
	"path"
	"path/filepath"
	"strings"

	"github.com/vide2d/vide-studio/internal/projfs"
)

// Limits protect the editor from hostile or accidental huge files.
const (
	MaxSide      = 8192
	MaxPixels    = 16 << 20
	MaxFileBytes = 32 << 20
)

// ValidPath reports whether p is a safe relative path to a .png file:
// slash-separated, local (no "..", not absolute), no backslashes, already clean.
func ValidPath(p string) bool { return ValidRelPath(p, ".png") }

// ValidRelPath is like ValidPath for any of the given file extensions.
func ValidRelPath(p string, exts ...string) bool {
	if p == "" || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	okExt := false
	for _, e := range exts {
		okExt = okExt || strings.EqualFold(path.Ext(p), e)
	}
	return okExt && path.Clean(p) == p && filepath.IsLocal(filepath.FromSlash(p))
}

// Decode loads the image rel from the project folder root after validating the
// path and limits. The file is opened once, through os.Root (no links on the
// way, cannot leave root); the same handle serves the size check, the header
// check and the decode, so it cannot be swapped in between.
func Decode(root, rel string) (image.Image, error) {
	if root == "" || !ValidPath(rel) {
		return nil, fmt.Errorf("bad asset path %q", rel)
	}
	f, err := projfs.OpenFile(root, rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > MaxFileBytes {
		return nil, errors.New("not a regular file or too large")
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxSide || cfg.Height > MaxSide || cfg.Width*cfg.Height > MaxPixels {
		return nil, fmt.Errorf("image size %dx%d out of limits", cfg.Width, cfg.Height)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(io.LimitReader(f, MaxFileBytes+1))
	return img, err
}
