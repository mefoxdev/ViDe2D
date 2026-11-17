package studio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vide2d/vide-studio/internal/projfs"
	"github.com/vide2d/vide-studio/internal/scene"
)

// newTestStudio builds a Studio without a window: one scene document in a temp project.
func newTestStudio(t *testing.T) *Studio {
	t.Helper()
	dir := t.TempDir()
	s := &Studio{scene: scene.New("Main"), zoom: 2, path: filepath.Join(dir, "project.vide.json")}
	s.proj.dir = dir
	s.tex = newTextures(dir)
	s.sel = s.scene.Root
	s.docs = []*document{{}}
	s.stash()
	return s
}

func TestNewDocFileCreatesInStandardFolders(t *testing.T) {
	s := newTestStudio(t)
	for typ, dir := range map[scene.DocType]string{scene.DocObject: "game/objects", scene.DocScene: "game/scenes", scene.DocMap: "game/maps"} {
		if err := s.newDocFile(typ, "My "+string(typ)); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		want := filepath.Join(s.proj.dir, filepath.FromSlash(dir), "My_"+string(typ)+".vide.json")
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("%s not created: %v", typ, err)
		}
		if s.scene.Type != typ {
			t.Fatalf("current doc is %s, want %s", s.scene.Type, typ)
		}
	}
	// a new map starts with one chunk
	if len(s.scene.Root.Children) != 1 || s.scene.Root.Children[0].Kind != scene.KindChunk {
		t.Fatal("new map should have a chunk")
	}
	if len(s.docs) != 4 {
		t.Fatalf("tabs: %d", len(s.docs))
	}
	// duplicates and bad names are refused
	if s.newDocFile(scene.DocMap, "My map") == nil || s.newDocFile(scene.DocObject, "  ") == nil {
		t.Fatal("expected errors")
	}
}

func TestSwitchingTabsKeepsEachDocumentsState(t *testing.T) {
	s := newTestStudio(t)
	s.scene.Add(nil, scene.KindNode, "InMain")
	if err := s.newDocFile(scene.DocObject, "Coin"); err != nil {
		t.Fatal(err)
	}
	s.scene.Add(nil, scene.KindSprite, "Body")
	s.activate(0)
	if s.scene.Type != scene.DocScene || len(s.scene.Root.Children) != 1 || s.scene.Root.Children[0].Name != "InMain" {
		t.Fatalf("main doc state lost: %+v", s.scene.Root.Children)
	}
	s.activate(1)
	if s.scene.Type != scene.DocObject || s.scene.Root.Children[0].Name != "Body" {
		t.Fatal("object doc state lost")
	}
	// opening an already-open document switches to its tab instead of duplicating it
	s.openDoc("game/objects/Coin.vide.json")
	if len(s.docs) != 2 {
		t.Fatalf("duplicate tab: %d", len(s.docs))
	}
}

func TestCloseDocSavesAndKeepsLast(t *testing.T) {
	s := newTestStudio(t)
	if err := s.newDocFile(scene.DocObject, "Coin"); err != nil {
		t.Fatal(err)
	}
	s.scene.Add(nil, scene.KindSprite, "Body")
	s.computeDirty()
	if !s.dirty {
		t.Fatal("should be dirty")
	}
	s.closeDoc(1)
	if len(s.docs) != 1 || s.scene.Type != scene.DocScene {
		t.Fatal("close failed")
	}
	// the change was saved before closing
	s.openDoc("game/objects/Coin.vide.json")
	if len(s.scene.Root.Children) != 1 {
		t.Fatal("unsaved change lost on close")
	}
	s.closeDoc(0)
	s.closeDoc(0)
	if len(s.docs) != 1 {
		t.Fatal("the last document must stay open")
	}
}

func TestInstanceCyclesAreRefused(t *testing.T) {
	s := newTestStudio(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.newDocFile(scene.DocObject, "A"))
	a := "game/objects/A.vide.json"
	must(s.newDocFile(scene.DocObject, "B"))
	b := "game/objects/B.vide.json"
	// in B (current): instance of A is fine
	if !s.canInstance(a) {
		t.Fatal("B may contain A")
	}
	s.addInstance(a)
	must2 := func(rel string) {
		s.save()
		s.openDoc(rel)
	}
	must2(a)
	// in A: B contains A, so A cannot contain B, nor itself
	if s.canInstance(b) || s.canInstance(a) {
		t.Fatal("cycle not detected")
	}
	n := len(s.scene.Root.Children)
	s.addInstance(b)
	if len(s.scene.Root.Children) != n {
		t.Fatal("cyclic instance was added")
	}
}

func TestFlattenInstancesAndMap(t *testing.T) {
	s := newTestStudio(t)
	if err := s.newDocFile(scene.DocObject, "Coin"); err != nil {
		t.Fatal(err)
	}
	sp := s.scene.Add(nil, scene.KindSprite, "Body")
	sp.X, sp.Y, sp.W, sp.H = 4, 6, 10, 12
	s.save()
	s.activate(0)

	s.sel = s.scene.Root
	s.addInstance("game/objects/Coin.vide.json")
	inst := s.sel
	inst.X, inst.Y = 100, 50

	var gotInst, gotBody *flat
	fl := s.flatten(false)
	for i := range fl {
		switch fl[i].n.Name {
		case "Coin":
			gotInst = &fl[i]
		case "Body":
			gotBody = &fl[i]
		}
	}
	if gotInst == nil || gotBody == nil {
		t.Fatalf("flatten missing items: %+v", fl)
	}
	if gotInst.x != 104 || gotInst.y != 56 || gotInst.w != 10 || gotInst.h != 12 {
		t.Fatalf("instance bounds: %+v", *gotInst)
	}
	if gotBody.x != 104 || gotBody.y != 56 || !gotBody.inside || gotBody.owner != inst {
		t.Fatalf("instance content: %+v", *gotBody)
	}
	// clicking the content selects the instance
	if s.hit(106, 58) != inst {
		t.Fatal("hit should return the instance")
	}
	if s.hit(0, 0) != nil && s.hit(0, 0) == inst {
		t.Fatal("hit outside the instance")
	}
}

func TestMapChunkOverflowBlocksSave(t *testing.T) {
	s := newTestStudio(t)
	if err := s.newDocFile(scene.DocMap, "World"); err != nil {
		t.Fatal(err)
	}
	chunk := s.scene.Root.Children[0]
	sp := s.scene.Add(chunk, scene.KindSprite, "Tree")
	sp.X, sp.Y = scene.ChunkTiles*16-4, 0 // sticks out by 12 px
	if s.saveDoc(s.docs[s.cur]) == nil {
		t.Fatal("saving a map with an object outside its chunk must fail")
	}
	fl := s.flatten(false)
	bad := false
	for _, f := range fl {
		bad = bad || (f.n == sp && f.bad)
	}
	if !bad {
		t.Fatal("overflowing sprite should be flagged")
	}
	sp.X = 0
	if err := s.saveDoc(s.docs[s.cur]); err != nil {
		t.Fatalf("valid map should save: %v", err)
	}
}

func TestListObjectsAndClassification(t *testing.T) {
	s := newTestStudio(t)
	_ = s.newDocFile(scene.DocObject, "Coin")
	_ = s.newDocFile(scene.DocScene, "Level1")
	objs := s.listObjects()
	if len(objs) != 1 || objs[0] != "game/objects/Coin.vide.json" {
		t.Fatalf("objects: %v", objs)
	}
	if k := projfs.ClassifyPath("game/scenes/Level1.vide.json"); k != projfs.KindScene {
		t.Fatalf("kind %v", k)
	}
}
