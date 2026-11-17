// Package assets safely decodes image files of a project. Paths are
// slash-separated and relative to the project folder.
package assets

import (
	"errors"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder
	"os"
	"path"
	"path/filepath"
	"strings"
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

func config(file string) (image.Config, error) {
	st, err := os.Lstat(file)
	if err != nil {
		return image.Config{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxFileBytes {
		return image.Config{}, errors.New("not a regular file or too large")
	}
	f, err := os.Open(file)
	if err != nil {
		return image.Config{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return image.Config{}, err
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxSide || cfg.Height > MaxSide || cfg.Width*cfg.Height > MaxPixels {
		return image.Config{}, fmt.Errorf("image size %dx%d out of limits", cfg.Width, cfg.Height)
	}
	return cfg, nil
}

// Decode loads the image rel from the project folder root after validating the path and limits.
func Decode(root, rel string) (image.Image, error) {
	if root == "" || !ValidPath(rel) {
		return nil, fmt.Errorf("bad asset path %q", rel)
	}
	file := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := config(file); err != nil {
		return nil, err
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
