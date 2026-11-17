package runtime

import (
	"fmt"
	"image/color"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/tdsl"
	"github.com/vide2d/vide-studio/internal/tdsl/vm"
)

// maxScriptBytes bounds a script file (the parser refuses more than 2 MB anyway).
const maxScriptBytes = 2 << 20

// scriptRuntime glues the TrueDSL VM to the world: it compiles the scripts
// that entities name, gives each entity an Instance, and reports errors.
type scriptRuntime struct {
	w     *World
	eng   *vm.Engine
	progs map[string]*vm.Program // by script path; nil value = the script failed to compile
	seen  map[string]int         // runtime errors by place, to keep the log readable
	names []string               // object names for the checker, refreshed per compile
}

func newScriptRuntime(w *World) *scriptRuntime {
	s := &scriptRuntime{w: w, progs: map[string]*vm.Program{}, seen: map[string]int{}}
	s.eng = vm.NewEngine(&scriptHost{w})
	return s
}

func (s *scriptRuntime) beginFrame() { s.eng.BeginFrame() }

// endFrame reports handlers that were skipped because the frame ran out of steps.
func (s *scriptRuntime) endFrame() {
	if n := s.eng.Skipped(); n > 0 {
		s.logOnce("frame-budget", fmt.Sprintf(
			"Слишком много шагов за кадр (TD401): все скрипты вместе делают не больше %d шагов в кадре. Пропущено запусков обработчиков: %d. Игра продолжается.",
			s.eng.Limits.FrameSteps, n))
	}
}

func (w *World) logf(format string, args ...any) { w.logMsg(fmt.Sprintf(format, args...)) }

// cleanLog makes a message safe for a terminal or console: control characters
// (escape sequences, carriage returns, bells ...) become U+FFFD; newlines and
// tabs stay. Script text reaches the log through "print" and error messages,
// and a script must not be able to repaint or hijack the reader's terminal.
func cleanLog(s string) string {
	clean := true
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return '\uFFFD'
		}
		return r
	}, s)
}

func (w *World) logMsg(msg string) {
	msg = cleanLog(msg)
	if w.opts.Log != nil {
		w.opts.Log(msg)
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

// logOnce logs msg the first time and then only when the count reaches 10,
// 100, 1000 ... (a bug in "when update" must not print 60 lines a second).
func (s *scriptRuntime) logOnce(key, msg string) {
	s.seen[key]++
	n := s.seen[key]
	switch {
	case n == 1:
		s.w.logMsg(msg)
	case n == 10 || n == 100 || n == 1000 || n == 10000 || n == 100000:
		s.w.logf("%s (повторилось %d раз)", firstLine(msg), n)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (s *scriptRuntime) report(errs []*vm.Error) {
	for _, e := range errs {
		key := fmt.Sprintf("%s:%d:%s", e.File, e.Line, e.Code)
		s.logOnce(key, strings.TrimRight(e.Format(), "\n"))
	}
}

// attach gives the entity a script behavior. Nothing is read yet: the script
// is compiled on the entity's first step, when all objects of the scene exist
// and the checker can know their names.
func (s *scriptRuntime) attach(e *Entity) {
	e.Behavior = &scriptBehavior{rt: s, ent: e}
}

// program returns the compiled script at path rel, compiling it once.
func (s *scriptRuntime) program(rel string) *vm.Program {
	if p, ok := s.progs[rel]; ok {
		return p
	}
	p := s.compile(rel)
	s.progs[rel] = p
	return p
}

func (s *scriptRuntime) compile(rel string) *vm.Program {
	file, err := s.w.src.SafeFile(rel)
	if err != nil {
		s.w.logf("Скрипт %s не запущен: %v. Игра идёт без него.", rel, err)
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		s.w.logf("Скрипт %s не запущен: %v. Игра идёт без него.", rel, unwrapPath(err))
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxScriptBytes+1))
	if err != nil || len(data) > maxScriptBytes {
		s.w.logf("Скрипт %s не запущен: файл не читается или больше %d байт. Игра идёт без него.", rel, maxScriptBytes)
		return nil
	}
	src := string(data)
	prog, errs := vm.CompileSource(rel, src, &tdsl.Env{Objects: s.objectNames()})
	if len(errs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Скрипт %s не запущен: в нём %d %s. Игра идёт без него.\n", rel, len(errs), plural(len(errs), "ошибка", "ошибки", "ошибок"))
		for _, e := range errs {
			b.WriteString("\n" + e.Format(src))
		}
		s.w.logMsg(strings.TrimRight(b.String(), "\n"))
		return nil
	}
	for _, h := range prog.Handlers {
		if h.Event == tdsl.EventKey && !KnownKey(h.Key) {
			s.w.logf("%s, строка %d: не знаю клавишу «%s», обработчик «%s» не сработает. Клавиши: left, right, up, down, space, enter, escape, shift, ctrl, a…z; остальные — в кавычках, например \"1\".",
				rel, h.Line, h.Key, h.Desc)
		}
	}
	for _, msg := range prog.Warnings {
		s.w.logMsg("Предупреждение: " + msg)
	}
	return prog
}

// objectNames lists the names of every object the scene can ever contain:
// the scene itself, the map (all chunks, loaded or not) and the object
// documents they instance. It does not depend on which chunks are loaded now,
// so a script checks the same way whenever it is first compiled.
func (s *scriptRuntime) objectNames() []string {
	if s.names != nil {
		return s.names
	}
	w := s.w
	seen := map[string]bool{}
	names := []string{}
	refs := map[string]bool{}
	var walk func(n *scene.Node)
	walk = func(n *scene.Node) {
		if !seen[n.Name] && n.Kind != scene.KindChunk {
			seen[n.Name] = true
			names = append(names, n.Name)
		}
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
	sort.Strings(names)
	s.names = names
	return names
}

func plural(n int, one, few, many string) string {
	m10, m100 := n%10, n%100
	switch {
	case m10 == 1 && m100 != 11:
		return one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		return few
	}
	return many
}

// scriptBehavior runs one entity's script.
type scriptBehavior struct {
	rt       *scriptRuntime
	ent      *Entity
	inst     *vm.Instance
	prepared bool
	pending  *vm.InstanceState // restored from a chunk reload, applied when the instance is created
}

// SnapshotState implements Stateful: the instance's variables and timers.
func (b *scriptBehavior) SnapshotState() any {
	if b.inst == nil {
		if b.pending != nil {
			return b.pending
		}
		return nil
	}
	return b.inst.Snapshot()
}

// RestoreState implements Stateful.
func (b *scriptBehavior) RestoreState(state any) {
	if st, ok := state.(*vm.InstanceState); ok {
		b.pending = st
	}
}

// Step does nothing: scripts run in the phases (RunPhase).
func (b *scriptBehavior) Step(*World, *Entity) {}

// RunPhase runs the handlers of the phase.
func (b *scriptBehavior) RunPhase(_ *World, e *Entity, p Phase) {
	if !b.prepared {
		b.prepared = true
		if prog := b.rt.program(e.Script); prog != nil {
			b.inst = b.rt.eng.NewInstance(prog, vm.ObjID(e.ID))
			if b.pending != nil {
				b.inst.Restore(b.pending)
				b.pending = nil
			}
		}
	}
	if b.inst == nil {
		return
	}
	var ph vm.Phase
	switch p {
	case PhaseStart:
		ph = vm.PhaseStart
	case PhaseInput:
		ph = vm.PhaseInput
	case PhaseUpdate:
		ph = vm.PhaseUpdate
	default:
		ph = vm.PhaseTimers
	}
	b.rt.report(b.inst.RunPhase(ph))
}

// ---- the host the VM talks to ----

type scriptHost struct{ w *World }

func (h *scriptHost) TickRate() int   { return h.w.TickRate }
func (h *scriptHost) Seed() uint64    { return h.w.opts.Seed }
func (h *scriptHost) Log(text string) { h.w.logMsg(text) }

func (h *scriptHost) Key(name string, st tdsl.KeyState) bool {
	in := h.w.Input
	if in == nil {
		return false
	}
	switch st {
	case tdsl.KeyHeld:
		return in.Held(name)
	case tdsl.KeyPressed:
		return in.Pressed(name)
	}
	return in.Released(name)
}

func (h *scriptHost) ent(id vm.ObjID) (*Entity, error) {
	if e := h.w.byID[int(id)]; e != nil {
		return e, nil
	}
	return nil, vm.ErrGone
}

// MoveBy shifts the object (and its group members) by (dx, dy) pixels.
func (h *scriptHost) MoveBy(id vm.ObjID, dx, dy float64) error {
	e, err := h.ent(id)
	if err != nil {
		return err
	}
	// explicit conversions: no fused multiply-add
	nx, ny := float64(e.X+dx), float64(e.Y+dy)
	if err := checkCoord(nx, ny); err != nil {
		return err
	}
	h.w.Place(e, nx, ny, false)
	return nil
}

// MoveTo puts the object at (x, y) relative to its parent group. Like "x = ..."
// it glides (no teleport) unless the object was just created.
func (h *scriptHost) MoveTo(id vm.ObjID, x, y float64) error {
	e, err := h.ent(id)
	if err != nil {
		return err
	}
	if err := checkCoord(x, y); err != nil {
		return err
	}
	wx, wy := e.WorldFromLocal(x, y)
	if err := checkCoord(wx, wy); err != nil {
		return err
	}
	h.w.Place(e, wx, wy, false)
	return nil
}

// checkCoord rejects NaN, infinities and numbers beyond vm.MaxCoord.
func checkCoord(vals ...float64) error {
	for _, f := range vals {
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > vm.MaxCoord {
			return &vm.HostError{Code: vm.CodeWrongType, Message: "Число вне допустимых пределов: положение объекта не может быть таким.",
				Hint: "Допустимы числа по модулю не больше " + vm.FormatNum(vm.MaxCoord) + "."}
		}
	}
	return nil
}

func (h *scriptHost) Destroy(id vm.ObjID) error {
	e, err := h.ent(id)
	if err != nil {
		return err
	}
	h.w.Destroy(e)
	return nil
}

func (h *scriptHost) GetProp(id vm.ObjID, p vm.Prop) (vm.Value, error) {
	e, err := h.ent(id)
	if err != nil {
		return vm.Value{}, err
	}
	switch p {
	case vm.PropX:
		x, _ := e.LocalPos()
		return vm.Num(x), nil
	case vm.PropY:
		_, y := e.LocalPos()
		return vm.Num(y), nil
	case vm.PropWidth:
		return vm.Num(e.W), nil
	case vm.PropHeight:
		return vm.Num(e.H), nil
	case vm.PropAngle:
		return vm.Num(e.Angle), nil
	case vm.PropVisible:
		return vm.Bool(e.Visible), nil
	case vm.PropImage:
		return vm.Text(e.Texture), nil
	case vm.PropTint:
		return vm.Text(colorText(e.Color)), nil
	case vm.PropFlip:
		return vm.Bool(e.Flip), nil
	case vm.PropLayer:
		return vm.Num(e.Layer), nil
	case vm.PropTag:
		if len(e.Tags) > 0 {
			return vm.Text(e.Tags[0]), nil
		}
		return vm.Text(""), nil
	case vm.PropName:
		return vm.Text(e.Name), nil
	case vm.PropText:
		return vm.Text(e.Text), nil
	case vm.PropFontSize:
		return vm.Num(float64(e.FontSize)), nil
	}
	return vm.Value{}, fmt.Errorf("unknown property %d", p)
}

func (h *scriptHost) SetProp(id vm.ObjID, p vm.Prop, v vm.Value) error {
	e, err := h.ent(id)
	if err != nil {
		return err
	}
	if v.K == vm.KNum {
		if err := checkCoord(v.N); err != nil {
			return err
		}
	}
	switch p {
	case vm.PropX, vm.PropY:
		lx, ly := e.LocalPos()
		if p == vm.PropX {
			lx = v.N
		} else {
			ly = v.N
		}
		wx, wy := e.WorldFromLocal(lx, ly)
		if err := checkCoord(wx, wy); err != nil {
			return err
		}
		h.w.Place(e, wx, wy, false)
	case vm.PropWidth:
		e.W = v.N
	case vm.PropHeight:
		e.H = v.N
	case vm.PropAngle:
		e.Angle = v.N
	case vm.PropVisible:
		e.Visible = v.IsTrue()
	case vm.PropFlip:
		e.Flip = v.IsTrue()
	case vm.PropLayer:
		e.Layer = v.N
	case vm.PropText:
		e.Text = v.S
	case vm.PropFontSize:
		e.FontSize = clampInt(v.N)
	case vm.PropTag:
		e.Tags = nil
		if v.S != "" {
			e.Tags = []string{v.S}
		}
	case vm.PropTint:
		c, ok := parseColor(v.S)
		if !ok {
			return &vm.HostError{Code: vm.CodeWrongType, Message: "Не знаю цвет «" + v.S + "».",
				Hint: "Цвет — имя (red, green, blue, yellow, orange, purple, pink, brown, white, black, gray, cyan) или код вроде \"#ff8800\"."}
		}
		e.Color = c
	case vm.PropImage:
		return h.setImage(e, v.S)
	}
	return nil
}

// setImage resolves an image name ("hero.png" or "assets/hero.png") to a
// project-relative path and checks that the file is there.
func (h *scriptHost) setImage(e *Entity, name string) error {
	if name == "" {
		e.Texture = ""
		return nil
	}
	rel := name
	if !strings.Contains(name, "/") {
		rel = "assets/" + name
	}
	if _, err := h.w.src.SafeFile(rel); err != nil {
		return &vm.HostError{Code: "TD304", Message: "Не нашёл картинку «" + name + "» в проекте.",
			Hint: "Пишите имя файла из папки assets, например \"hero.png\"."}
	}
	e.Texture = rel
	return nil
}

func clampInt(f float64) int {
	if math.IsNaN(f) {
		return 0
	}
	return int(math.Round(math.Max(-1e9, math.Min(1e9, f))))
}

var colorNames = map[string]color.RGBA{
	"red": {0xff, 0x00, 0x00, 0xff}, "green": {0x00, 0xc8, 0x00, 0xff}, "blue": {0x00, 0x50, 0xff, 0xff},
	"yellow": {0xff, 0xe1, 0x00, 0xff}, "orange": {0xff, 0x8c, 0x00, 0xff}, "purple": {0x8c, 0x00, 0xc8, 0xff},
	"pink": {0xff, 0x80, 0xb4, 0xff}, "brown": {0x8c, 0x5a, 0x28, 0xff}, "white": {0xff, 0xff, 0xff, 0xff},
	"black": {0x00, 0x00, 0x00, 0xff}, "gray": {0x80, 0x80, 0x80, 0xff}, "cyan": {0x00, 0xdc, 0xdc, 0xff},
}

func parseColor(s string) (color.RGBA, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if c, ok := colorNames[s]; ok {
		return c, true
	}
	if !strings.HasPrefix(s, "#") || (len(s) != 7 && len(s) != 9) {
		return color.RGBA{}, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	if len(s) == 7 {
		return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}, true
	}
	return color.RGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

func colorText(c color.RGBA) string {
	if c.A == 0xff {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}
