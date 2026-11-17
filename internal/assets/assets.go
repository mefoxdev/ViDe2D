// Package assets scans and safely decodes image assets from a project's
// assets folder. Asset paths are slash-separated and relative to that folder.
package assets

import (
	"errors"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Limits protect the editor from hostile or accidental huge files.
const (
	MaxSide      = 8192
	MaxPixels    = 16 << 20
	MaxFileBytes = 32 << 20
	MaxAssets    = 5000
)

// Info describes one image asset.
type Info struct {
	Path string // slash-separated, relative to the assets folder
	W, H int
}

// ValidPath reports whether p is a safe relative asset path: slash-separated,
// local (no "..", not absolute), no backslashes, and a .png file.
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

// Scan lists the valid PNG assets under root, sorted by path. A missing root
// yields an empty list. Unreadable or oversized images are skipped.
func Scan(root string) ([]Info, error) {
	if root == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []Info
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil // skip symlinks, devices, unreadable entries
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !ValidPath(rel) {
			return nil
		}
		if len(out) >= MaxAssets {
			return fs.SkipAll
		}
		if cfg, cerr := config(p); cerr == nil {
			out = append(out, Info{Path: rel, W: cfg.Width, H: cfg.Height})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
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

// Decode loads the asset rel from root after validating the path and limits.
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
