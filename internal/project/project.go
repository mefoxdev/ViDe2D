// Package project reads and writes ViDe-studio scene files (JSON, versioned).
//
// This is an editor-side format; the runtime contract lives in pkg/formats.
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"github.com/vide2d/vide-studio/internal/scene"
)

// Version is the current file format version.
const Version = 1

const (
	maxDepth = 64
	maxNodes = 100_000
)

type file struct {
	Version int   `json:"version"`
	Scene   *node `json:"scene"`
}

type node struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	X        int     `json:"x"`
	Y        int     `json:"y"`
	W        int     `json:"w,omitempty"`
	H        int     `json:"h,omitempty"`
	Color    string  `json:"color,omitempty"`
	Children []*node `json:"children,omitempty"`
}

// Marshal serializes a scene.
func Marshal(s *scene.Scene) ([]byte, error) {
	return json.MarshalIndent(file{Version: Version, Scene: toNode(s.Root)}, "", "  ")
}

func toNode(n *scene.Node) *node {
	o := &node{Name: n.Name, Kind: string(n.Kind), X: n.X, Y: n.Y}
	if n.Kind == scene.KindSprite {
		o.W, o.H = n.W, n.H
		o.Color = fmt.Sprintf("#%02x%02x%02x", n.Color.R, n.Color.G, n.Color.B)
	}
	for _, c := range n.Children {
		o.Children = append(o.Children, toNode(c))
	}
	return o
}

// Unmarshal parses and validates a scene file.
func Unmarshal(data []byte) (*scene.Scene, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid project file: %w", err)
	}
	if f.Version < 1 {
		return nil, errors.New("invalid project file: missing version")
	}
	if f.Version > Version {
		return nil, fmt.Errorf("project file version %d is newer than supported (%d)", f.Version, Version)
	}
	if f.Scene == nil || f.Scene.Name == "" {
		return nil, errors.New("invalid project file: missing scene root")
	}
	s := scene.New(f.Scene.Name)
	count := 1
	root := f.Scene
	s.Root.X, s.Root.Y = root.X, root.Y
	if err := build(s, s.Root, root.Children, 1, &count); err != nil {
		return nil, err
	}
	return s, nil
}

func build(s *scene.Scene, parent *scene.Node, kids []*node, depth int, count *int) error {
	if depth > maxDepth {
		return errors.New("invalid project file: scene is nested too deeply")
	}
	for _, k := range kids {
		if k == nil {
			return errors.New("invalid project file: null node")
		}
		if *count++; *count > maxNodes {
			return errors.New("invalid project file: too many nodes")
		}
		kind := scene.Kind(k.Kind)
		if kind != scene.KindNode && kind != scene.KindSprite {
			return fmt.Errorf("invalid project file: unknown node kind %q", k.Kind)
		}
		if k.Name == "" {
			return errors.New("invalid project file: node without a name")
		}
		n := s.Add(parent, kind, k.Name)
		n.X, n.Y = k.X, k.Y
		if kind == scene.KindSprite {
			if k.W < 1 || k.H < 1 {
				return fmt.Errorf("invalid project file: sprite %q has a non-positive size", k.Name)
			}
			n.W, n.H = k.W, k.H
			if k.Color != "" {
				c, err := parseColor(k.Color)
				if err != nil {
					return fmt.Errorf("invalid project file: sprite %q: %w", k.Name, err)
				}
				n.Color = c
			}
		}
		if err := build(s, n, k.Children, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func parseColor(s string) (color.RGBA, error) {
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil || len(s) != 7 {
		return color.RGBA{}, fmt.Errorf("bad color %q", s)
	}
	return color.RGBA{r, g, b, 0xff}, nil
}

// Save writes data to path atomically (temp file + rename).
func Save(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vide-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads and validates a scene file.
func Load(path string) (*scene.Scene, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Unmarshal(data)
}
