// Package client is the player-facing shell of ViDe2D: a library of games found
// in a folder, and launchers that start the selected game.
package client

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vide2d/vide-studio/internal/project"
	"github.com/vide2d/vide-studio/internal/projfs"
)

// ProjectFile is the main scene file of a project.
const ProjectFile = "project.vide.json"

// Limits keep a scan of a hostile or huge folder cheap.
const (
	maxGames   = 1000 // games listed
	maxScanned = 4000 // folder entries looked at (in the games folder and in each game/scenes)
)

// Entry is one game in the library.
type Entry struct {
	Name string // title shown to the player
	File string // absolute path of the scene file to run
	Root string // project folder; "" only for a loose scene file, whose root is its own folder
	Err  string // non-empty if the game cannot be opened (still listed)
}

// DefaultGamesDir is ~/ViDe2D/games, or "" if there is no home folder.
func DefaultGamesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "ViDe2D", "games")
}

// Scan lists the games directly inside dir. Every subfolder holding
// project.vide.json (or, failing that, a scene in game/scenes) is a game, and
// so is a loose *.vide.json file in dir itself. Unreadable games are listed
// with Err set rather than hidden. A missing dir yields an empty list.
// Symbolic links (and junctions) are never followed, at any level: all reading
// goes through projfs.Root, so a game cannot point the client elsewhere. The
// work is capped (maxGames, maxScanned) and documents larger than
// projfs.MaxDocBytes are listed with an error instead of being read.
func Scan(dir string) []Entry {
	top, err := projfs.OpenDir(dir)
	if err != nil {
		return nil
	}
	defer top.Close()
	items, err := top.ReadDirLimited(".", maxScanned)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, it := range items {
		if len(out) >= maxGames {
			break
		}
		path := filepath.Join(dir, it.Name())
		switch {
		case it.Type().IsDir():
			sub, err := top.OpenSub(it.Name())
			if err != nil {
				continue
			}
			if rel := findMain(sub); rel != "" {
				out = append(out, describe(sub, rel, filepath.Join(path, filepath.FromSlash(rel)), path, it.Name()))
			}
			sub.Close()
		case it.Type().IsRegular() && strings.HasSuffix(it.Name(), projfs.DocExt):
			out = append(out, describe(top, it.Name(), path, "", strings.TrimSuffix(it.Name(), projfs.DocExt)))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// findMain returns the project-relative scene file to run for a project
// folder, or "".
func findMain(folder *projfs.Root) string {
	if _, err := folder.Stat(ProjectFile); err == nil {
		return ProjectFile
	}
	scenes, err := folder.ReadDirLimited(projfs.ScenesDir, maxScanned) // refuses linked game/ or game/scenes
	if err != nil {
		return ""
	}
	for _, s := range scenes { // sorted by name: the first scene wins
		if s.Type().IsRegular() && strings.HasSuffix(s.Name(), projfs.DocExt) {
			return projfs.ScenesDir + "/" + s.Name()
		}
	}
	return ""
}

// describe reads the title of the document rel inside r.
func describe(r *projfs.Root, rel, file, root, fallback string) Entry {
	e := Entry{Name: fallback, File: file, Root: root}
	data, err := r.ReadFile(rel, projfs.MaxDocBytes)
	if err != nil {
		e.Err = err.Error()
		return e
	}
	sc, err := project.Unmarshal(data)
	if err != nil {
		e.Err = err.Error()
		return e
	}
	if sc.Root != nil && strings.TrimSpace(sc.Root.Name) != "" {
		e.Name = sc.Root.Name
	}
	return e
}
