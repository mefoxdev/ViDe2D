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
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vide2d/vide-studio/internal/assets"
	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
)

// Version is the current file format version.
//
// Version 1 stored texture paths relative to an assets/ folder and script
// paths relative to a scripts/ folder; version 2 stores both relative to the
// project folder. Version 1 files are upgraded on load.
//
// Version 3 adds the document type (scene, object or map), tile_size, the
// scene's map, instances of objects and map chunks. Older files are scenes.
const Version = 3

const (
	maxDepth = 64
	maxNodes = 100_000
	maxText  = 10_000
	maxTag   = 64

	maxChunkCoord = 4096
)

type file struct {
	Version   int    `json:"version"`
	Type      string `json:"type,omitempty"`
	ImageMode string `json:"image_mode,omitempty"`
	TileSize  int    `json:"tile_size,omitempty"`
	Map       string `json:"map,omitempty"`
	// Optimization is a map parameter; a map file without it is optimized.
	Optimization *bool `json:"optimization,omitempty"`
	Scene        *node `json:"scene"`
}

type node struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	X        int     `json:"x"`
	Y        int     `json:"y"`
	W        int     `json:"w,omitempty"`
	H        int     `json:"h,omitempty"`
	Color    string  `json:"color,omitempty"`
	Texture  string  `json:"texture,omitempty"`
	Tag      string  `json:"tag,omitempty"`
	Script   string  `json:"script,omitempty"`
	Template bool    `json:"template,omitempty"`
	Text     string  `json:"text,omitempty"`
	FontSize int     `json:"font_size,omitempty"`
	Ref      string  `json:"ref,omitempty"`
	Chunk    *[2]int `json:"chunk,omitempty"`
	Children []*node `json:"children,omitempty"`
}

// Marshal serializes a scene.
func Marshal(s *scene.Scene) ([]byte, error) {
	return json.MarshalIndent(file{Version: Version, Type: string(s.Type), ImageMode: string(s.ImageMode), TileSize: s.TileSize, Map: s.Map, Optimization: mapOpt(s), Scene: toNode(s.Root)}, "", "  ")
}

func mapOpt(s *scene.Scene) *bool {
	if s.Type != scene.DocMap {
		return nil
	}
	v := s.Optimization
	return &v
}

func toNode(n *scene.Node) *node {
	o := &node{Name: n.Name, Kind: string(n.Kind), X: n.X, Y: n.Y}
	o.Tag, o.Script, o.Template = n.Tag, n.Script, n.Template
	switch n.Kind {
	case scene.KindText:
		o.Text, o.FontSize = n.Text, n.FontSize
	case scene.KindInstance:
		o.Ref = n.Ref
	case scene.KindChunk:
		o.Chunk = &[2]int{n.CX, n.CY}
	}
	if n.HasBox() {
		o.W, o.H = n.W, n.H
		o.Texture = n.Texture
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
	switch t := scene.DocType(f.Type); t {
	case "":
	case scene.DocScene, scene.DocObject, scene.DocMap:
		s.Type = t
	default:
		return nil, fmt.Errorf("invalid project file: unknown type %q", f.Type)
	}
	if s.Type == scene.DocMap {
		s.Optimization = f.Optimization == nil || *f.Optimization // default: optimized
	} else if f.Optimization != nil {
		return nil, errors.New("invalid project file: only maps have the optimization parameter")
	}
	if f.TileSize != 0 {
		if f.TileSize < 1 || f.TileSize > 256 {
			return nil, fmt.Errorf("invalid project file: bad tile_size %d", f.TileSize)
		}
		s.TileSize = f.TileSize
	}
	if f.Map != "" {
		if s.Type != scene.DocScene {
			return nil, errors.New("invalid project file: only scenes can use a map")
		}
		if !validDocRef(f.Map, projfs.MapsDir) {
			return nil, fmt.Errorf("invalid project file: bad map path %q", f.Map)
		}
		s.Map = f.Map
	}
	switch m := scene.ImageMode(f.ImageMode); m {
	case "":
	case scene.ImagePixel, scene.ImageNormal:
		s.ImageMode = m
	default:
		return nil, fmt.Errorf("invalid project file: unknown image_mode %q", f.ImageMode)
	}
	count := 1
	root := f.Scene
	s.Root.X, s.Root.Y = root.X, root.Y
	if root.Template {
		return nil, errors.New("invalid project file: the scene root cannot be a template")
	}
	if f.Version == 1 {
		upgradeV1(root, 0)
	}
	if err := applyMeta(s.Root, root); err != nil {
		return nil, err
	}
	if err := build(s, s.Root, root.Children, 1, &count); err != nil {
		return nil, err
	}
	if err := validateStructure(s); err != nil {
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
		switch kind {
		case scene.KindNode, scene.KindSprite, scene.KindText, scene.KindInstance, scene.KindChunk:
		default:
			return fmt.Errorf("invalid project file: unknown node kind %q", k.Kind)
		}
		if k.Name == "" {
			return errors.New("invalid project file: node without a name")
		}
		n := s.Add(parent, kind, k.Name)
		n.X, n.Y = k.X, k.Y
		if err := applyMeta(n, k); err != nil {
			return err
		}
		if kind == scene.KindText {
			if utf8.RuneCountInString(k.Text) > maxText {
				return fmt.Errorf("invalid project file: text of %q is too long", k.Name)
			}
			n.Text = k.Text
			if k.FontSize == 0 {
				k.FontSize = 10
			}
			if k.FontSize < 1 || k.FontSize > 256 {
				return fmt.Errorf("invalid project file: text %q has a bad font_size", k.Name)
			}
			n.FontSize = k.FontSize
		}
		switch kind {
		case scene.KindInstance:
			if !validDocRef(k.Ref, projfs.ObjectsDir) {
				return fmt.Errorf("invalid project file: %q has a bad object reference %q", k.Name, k.Ref)
			}
			if len(k.Children) > 0 {
				return fmt.Errorf("invalid project file: instance %q cannot have children", k.Name)
			}
			n.Ref = k.Ref
		case scene.KindChunk:
			if k.Chunk == nil || abs(k.Chunk[0]) > maxChunkCoord || abs(k.Chunk[1]) > maxChunkCoord {
				return fmt.Errorf("invalid project file: chunk %q has bad coordinates", k.Name)
			}
			n.CX, n.CY = k.Chunk[0], k.Chunk[1]
			n.X, n.Y = s.ChunkOrigin(n)
		}
		if kind == scene.KindSprite || kind == scene.KindText {
			if k.W < 1 || k.H < 1 {
				return fmt.Errorf("invalid project file: %q has a non-positive size", k.Name)
			}
			n.W, n.H = k.W, k.H
			if k.Texture != "" && kind != scene.KindSprite {
				return fmt.Errorf("invalid project file: %q: only sprites have textures", k.Name)
			}
			if k.Texture != "" {
				if !assets.ValidPath(k.Texture) {
					return fmt.Errorf("invalid project file: sprite %q: bad texture path %q", k.Name, k.Texture)
				}
				n.Texture = k.Texture
			}
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

// upgradeV1 rewrites version 1 paths to project-relative ones.
func upgradeV1(n *node, depth int) {
	if n == nil || depth > maxDepth+1 {
		return
	}
	if n.Texture != "" {
		n.Texture = "assets/" + n.Texture
	}
	if n.Script != "" {
		n.Script = "scripts/" + n.Script
	}
	for _, c := range n.Children {
		upgradeV1(c, depth+1)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// validDocRef reports whether ref is a project-relative path to a document
// ("….vide.json") inside folder dir.
func validDocRef(ref, dir string) bool {
	return projfs.ValidRel(ref) && strings.HasPrefix(ref, dir+"/") && strings.HasSuffix(strings.ToLower(ref), projfs.DocExt)
}

// validateStructure checks rules that depend on the document type: maps hold
// only chunks (each at a unique spot, content inside the chunk), and other
// documents have no chunks.
func validateStructure(s *scene.Scene) error {
	if s.Type != scene.DocMap {
		var bad *scene.Node
		s.Root.Walk(func(n *scene.Node, _ int) {
			if n.Kind == scene.KindChunk && bad == nil {
				bad = n
			}
		})
		if bad != nil {
			return fmt.Errorf("invalid project file: chunk %q is only allowed in maps", bad.Name)
		}
		return nil
	}
	var nested *scene.Node
	s.Root.Walk(func(n *scene.Node, _ int) {
		if n.Kind == scene.KindChunk && n.Parent != s.Root && nested == nil {
			nested = n
		}
	})
	if nested != nil {
		return fmt.Errorf("invalid project file: chunk %q must be placed directly in the map", nested.Name)
	}
	seen := map[[2]int]bool{}
	size := s.ChunkSize()
	for _, c := range s.Root.Children {
		if c.Kind != scene.KindChunk {
			if s.Optimization {
				return fmt.Errorf("invalid project file: an optimized map can only contain chunks, found %q", c.Name)
			}
			continue
		}
		key := [2]int{c.CX, c.CY}
		if seen[key] {
			return fmt.Errorf("invalid project file: two chunks at %d,%d", c.CX, c.CY)
		}
		seen[key] = true
		if s.Optimization {
			if msg := scene.ChunkOverflow(c, size); msg != "" {
				return fmt.Errorf("invalid project file: %s", msg)
			}
		}
	}
	return nil
}

// applyMeta validates and copies the tag, script and template fields.
func applyMeta(n *scene.Node, k *node) error {
	if utf8.RuneCountInString(k.Tag) > maxTag || strings.ContainsFunc(k.Tag, unicode.IsControl) {
		return fmt.Errorf("invalid project file: %q has a bad tag", k.Name)
	}
	if k.Script != "" && !assets.ValidRelPath(k.Script, ".tdsl") {
		return fmt.Errorf("invalid project file: %q has a bad script path %q", k.Name, k.Script)
	}
	n.Tag, n.Script, n.Template = k.Tag, k.Script, k.Template
	return nil
}

func parseColor(s string) (color.RGBA, error) {
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil || len(s) != 7 {
		return color.RGBA{}, fmt.Errorf("bad color %q", s)
	}
	return color.RGBA{r, g, b, 0xff}, nil
}

// Save writes data plus a trailing newline to path atomically.
func Save(path string, data []byte) error { return WriteAtomic(path, append(data, '\n')) }

// WriteAtomic writes data to path atomically (temp file + rename).
func WriteAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vide-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
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
