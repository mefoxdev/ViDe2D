package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// Scenes. A game is a set of scene documents: the main scene (the file the
// game started from) and the files in game/scenes. "go to scene "name"" names
// a file of game/scenes without ".vide.json", or the main scene ("main" or the
// name of its root object).

// maxSceneName bounds the name of a scene in a script.
const maxSceneName = 100

// sceneListLimit is how many scene names a hint lists.
const sceneListLimit = 8

// SceneRequest is a scene change a script asked for. It is applied after the
// current step is over (World.Next).
type SceneRequest struct {
	// Restart: start the current scene anew, with the "shared" variables reset.
	Restart bool
	// Rel is the project path of the scene to go to; Name is how the script called it.
	Rel, Name string
}

func sceneNameOK(name string) bool {
	if name == "" || len(name) > maxSceneName || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

// ResolveScene finds the scene document a script name means: the file
// game/scenes/<name>.vide.json, else the main scene. It fails with a
// HostError (TD305) for an unknown name or a document that is not a scene.
func (s *Source) ResolveScene(name string) (string, error) {
	if sceneNameOK(name) {
		rel := projfs.ScenesDir + "/" + name + projfs.DocExt
		if _, err := s.SafeFile(rel); err == nil {
			doc, err := s.Doc(rel)
			if err != nil {
				return "", s.sceneError(name, "файл сцены не открывается: "+unwrapPath(err).Error())
			}
			if doc.Type != scene.DocScene {
				return "", s.sceneError(name, "это не сцена, а "+string(doc.Type))
			}
			return rel, nil
		}
		if s.Main != "" {
			if name == "main" {
				return s.Main, nil
			}
			if doc, err := s.Doc(s.Main); err == nil && doc.Root != nil && doc.Root.Name == name {
				return s.Main, nil
			}
		}
	}
	return "", s.sceneError(name, "")
}

// sceneNames lists the scenes of the project, sorted, for hints.
func (s *Source) sceneNames() []string {
	r, err := projfs.OpenDir(s.Root)
	if err != nil {
		return nil
	}
	defer r.Close()
	ents, err := r.ReadDirLimited(projfs.ScenesDir, 200)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, projfs.DocExt) {
			out = append(out, strings.TrimSuffix(n, projfs.DocExt))
		}
	}
	sort.Strings(out)
	return out
}

func (s *Source) sceneError(name, why string) error {
	msg := "Нет сцены «" + name + "»."
	if why != "" {
		msg = "Сцена «" + name + "» не открывается: " + why + "."
	}
	hint := "Сцены лежат в папке game/scenes: имя сцены — имя файла без «.vide.json»; главная сцена — \"main\"."
	if names := s.sceneNames(); len(names) > 0 {
		hint += " Есть сцены: " + strings.Join(names[:min(len(names), sceneListLimit)], ", ") + "."
	}
	return &vm.HostError{Code: vm.CodeNoScene, Message: msg, Hint: hint}
}

// SceneRel is the project path of the scene the world was built from, "" if
// it is not known (a world made directly from a document).
func (w *World) SceneRel() string { return w.rel }

// SceneRequest is the scene change a script asked for in the steps so far, or
// nil. Sim applies it after the step; a caller that steps the world itself
// can do so with Next.
func (w *World) SceneRequest() *SceneRequest { return w.sceneReq }

// Next builds the world the request leads to, from the same source and with
// the same options. The new world starts at tick 0 with its own tick rate and
// view size. What goes along:
//
//   - the memory of the game (Store), the sound state (music keeps playing on
//     "go to scene"), the keyboard and mouse;
//   - for "go to scene", the "shared" variables (objects in them become
//     nothing, they belong to the old world); a "restart scene" starts from
//     the initial values and silences all sounds.
//
// Everything else (objects, handler runs that wait, queued messages, the
// chunks) belongs to the old world and goes with it.
func (w *World) Next(req *SceneRequest) (*World, error) {
	if req == nil {
		return nil, fmt.Errorf("no scene request")
	}
	doc, rel := w.scene, w.rel
	if !req.Restart {
		var err error
		if doc, err = w.src.Doc(req.Rel); err != nil {
			return nil, err
		}
		rel = req.Rel
	}
	opts := w.opts
	if w.Input != nil {
		opts.Input = w.Input
	}
	opts.shared = nil
	if w.scripts != nil && !req.Restart {
		opts.shared = w.scripts.eng.ExportShared()
	}
	if err := opts.Store.Flush(); err != nil {
		w.logOnceKey("store-flush", "Память игры не сохранилась: "+err.Error()+". Игра продолжается.")
	}
	nw, err := NewWorld(w.src, doc, opts)
	if err != nil {
		return nil, err
	}
	nw.rel = rel
	if req.Restart {
		opts.mixer.StopAll()
	}
	return nw, nil
}

// saveMemory writes the memory of the game once a second of game time.
func (w *World) saveMemory() {
	if w.Tick%uint64(w.TickRate) != 0 {
		return
	}
	if err := w.opts.Store.Flush(); err != nil {
		w.logOnceKey("store-flush", "Память игры не сохранилась: "+err.Error()+". Игра продолжается.")
	}
}

// Flush writes the memory of the game now (call it when the game ends).
func (w *World) Flush() error { return w.opts.Store.Flush() }

// StopSound silences the game's sounds (call it when the game ends).
func (w *World) StopSound() { w.opts.mixer.StopAll() }

// SetAudio chooses where the sounds of the game play. It also applies to the
// scenes that follow.
func (w *World) SetAudio(a Audio) {
	if a == nil {
		a = NullAudio{}
	}
	w.opts.Audio = a
	w.opts.mixer.audio = a
}

// logOnceKey logs a message the first time (see scriptRuntime.logOnce).
func (w *World) logOnceKey(key, msg string) {
	if w.scripts != nil {
		w.scripts.logOnce(key, msg)
		return
	}
	w.logMsg(msg)
}

// ---- the host side of the services ----

// PlaySound implements vm.Host.
func (h *scriptHost) PlaySound(name string) error {
	h.w.scripts.eng.Charge(8)
	return h.w.opts.mixer.PlaySound(name)
}

// PlayMusic implements vm.Host.
func (h *scriptHost) PlayMusic(name string) error {
	h.w.scripts.eng.Charge(8)
	return h.w.opts.mixer.PlayMusic(name)
}

// StopMusic implements vm.Host.
func (h *scriptHost) StopMusic() { h.w.opts.mixer.StopMusic() }

// GoToScene implements vm.Host: the name is checked now, the change happens
// after the step. If a change was already asked for in this step, the first
// one wins.
func (h *scriptHost) GoToScene(name string) error {
	h.w.scripts.eng.Charge(32) // looking for a file is not free
	rel, err := h.w.src.ResolveScene(name)
	if err != nil {
		return err
	}
	if h.w.sceneReq == nil {
		h.w.sceneReq = &SceneRequest{Rel: rel, Name: name}
	}
	return nil
}

// RestartScene implements vm.Host.
func (h *scriptHost) RestartScene() {
	if h.w.sceneReq == nil {
		h.w.sceneReq = &SceneRequest{Restart: true}
	}
}

// Remember implements vm.Host.
func (h *scriptHost) Remember(key string, v vm.Value) (int, error) {
	return h.w.opts.Store.Set(key, v)
}

// Recall implements vm.Host.
func (h *scriptHost) Recall(key string) (vm.Value, bool, int) {
	return h.w.opts.Store.Get(key)
}
