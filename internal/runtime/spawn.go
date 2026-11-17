package runtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// Templates and spawning. A node marked Template is a prototype: it never
// enters the running world, but "spawn" can make copies of it (with its whole
// subtree, its script and its tag). Names are looked up in the scene, the map
// and the objects they instance, in that order, the first one wins.

// templateRegistry returns the template nodes by name.
func (w *World) templateRegistry() map[string]*scene.Node {
	if w.templates == nil {
		w.templates = map[string]*scene.Node{}
		for _, n := range w.allNodes() {
			if _, dup := w.templates[n.Name]; n.Template && !dup {
				w.templates[n.Name] = n
			}
		}
	}
	return w.templates
}

// TemplateNames lists the names of the project's templates, sorted.
func (w *World) TemplateNames() []string {
	var out []string
	for n := range w.templateRegistry() {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// spawn makes a copy of the template at world position (x, y). The new objects
// are alive for scripts at once but join the world (iteration, collisions,
// "when start") at the end of the step.
func (w *World) spawn(name string, x, y float64, limit int) ([]*Entity, error) {
	node := w.templateRegistry()[name]
	if node == nil {
		names := w.TemplateNames()
		hint := "Шаблон — объект сцены с галочкой Template."
		if len(names) > 0 {
			hint += " Есть шаблоны: " + strings.Join(names[:min(len(names), 8)], ", ") + "."
		} else {
			hint += " В проекте шаблонов пока нет."
		}
		return nil, &vm.HostError{Code: "TD304", Message: "Не нашёл шаблон «" + name + "».", Hint: hint}
	}
	if w.spawnLive >= limit {
		return nil, spawnLimitError(limit)
	}
	cp := *node
	cp.Template, cp.X, cp.Y, cp.Parent = false, 0, 0, nil
	ents, err := w.expandNode(&cp, 0, 0, nil, 0, nil, scope{})
	if err != nil {
		return nil, &vm.HostError{Code: "TD304", Message: "Не удалось создать «" + name + "»: " + err.Error() + "."}
	}
	if w.spawnLive+len(ents) > limit {
		w.discard(ents)
		return nil, spawnLimitError(limit)
	}
	for _, e := range ents {
		e.spawned, e.pending = true, true
	}
	w.spawnLive += len(ents)
	w.Place(ents[0], x, y, true)
	w.pending = append(w.pending, ents...)
	return ents, nil
}

func spawnLimitError(limit int) error {
	return &vm.HostError{Code: vm.CodeObjects,
		Message: fmt.Sprintf("Слишком много объектов: «spawn» создаёт не больше %d живых объектов.", limit),
		Hint:    "Удаляйте ненужные объекты командой «destroy» (например, пули, которые улетели за экран)."}
}

// allNodes lists every node the scene can ever contain: the scene itself, the
// map (all chunks, loaded or not) and the object documents they instance, in
// scene-tree order. It does not depend on which chunks are loaded now.
func (w *World) allNodes() []*scene.Node {
	if w.nodes != nil {
		return w.nodes
	}
	w.nodes = []*scene.Node{}
	refs := map[string]bool{}
	var walk func(n *scene.Node)
	walk = func(n *scene.Node) {
		w.nodes = append(w.nodes, n)
		for _, c := range n.Children {
			walk(c)
		}
		if n.Kind == scene.KindInstance && !refs[n.Ref] {
			refs[n.Ref] = true
			if doc, err := w.src.Doc(n.Ref); err == nil && doc.Type == scene.DocObject {
				for _, c := range doc.Root.Children {
					walk(c)
				}
			}
		}
	}
	if w.scene != nil {
		walk(w.scene.Root)
	}
	if w.mapDoc != nil {
		walk(w.mapDoc.Root)
	}
	return w.nodes
}
