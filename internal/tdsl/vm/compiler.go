package vm

import (
	"fmt"
	"math"
	"strings"

	"github.com/vide2d/vide-studio/internal/tdsl"
)

// HandlerInfo describes one "when" handler of a script, in file order.
type HandlerInfo struct {
	Event     tdsl.EventKind
	Key       string        // for EventKey
	State     tdsl.KeyState // for EventKey
	Desc      string        // "when key space pressed", used in error messages
	Line      int           // source line of the "when"
	Supported bool          // false: the event does not exist in this version; the handler never runs
	fn        *Func
	period    *Func // for EventEvery: computes the period in seconds
	arg       *Func // for touch, stop touching and message: computes the argument
	argText   string
	argConst  bool // the argument is a plain text: argText, no computation
}

// SharedDef is a "shared" declaration of a script.
type SharedDef struct {
	Name string
	Line int
	Init *Func
}

// Lib is a parsed script whose "to" commands are linked into a program that
// says "use" for it. File and Src are kept for error messages.
type Lib struct {
	File, Src string
	Script    *tdsl.Script
}

// Program is a compiled script. It is immutable and may be shared by the
// Instances of all objects that use the script.
type Program struct {
	File     string
	Handlers []HandlerInfo
	// Warnings lists the places that use a feature this version does not
	// implement; each one stops the handler run that reaches it.
	Warnings []string
	Consts   []Value
	Funcs    []*Func // "to" commands
	Globals  []string
	// Shared lists the "shared" variables this script declares.
	Shared []SharedDef
	// Uses lists the paths of the "use" lines, as written.
	Uses []string
	// Inits[i] computes and stores the initial value of the object variable
	// Globals[i]; each is run on its own, so a failing one leaves only its
	// own variable at nothing.
	Inits []*Func
	fails []failure
	lines []string
}

// Compile turns a parsed and checked script into a Program. src is the source
// text, used only to quote lines in error messages. Compile never fails: what
// the VM cannot run becomes an OpFail instruction that reports an error when
// (and only if) it is reached.
func Compile(file, src string, s *tdsl.Script) *Program { return CompileLibs(file, src, s, nil) }

// CompileLibs is Compile with the scripts named by "use" linked in: their "to"
// commands become commands of the program. A command of the script itself wins
// over a library command of the same name; between libraries the first wins.
// Only "to" items of a library count.
func CompileLibs(file, src string, s *tdsl.Script, libs []Lib) *Program {
	c := &compiler{libs: libs,
		p:       &Program{File: file, lines: strings.Split(strings.TrimPrefix(src, "\xef\xbb\xbf"), "\n")},
		globals: map[string]int{}, shared: map[string]bool{}, actions: map[string]int{},
		constNum: map[uint64]int{}, constText: map[string]int{}, warned: map[string]bool{},
	}
	c.run(s)
	return c.p
}

// CompileSource parses, checks and compiles a script. Syntax and check errors
// are returned (with File set) and no Program is produced. env may be nil.
func CompileSource(file, src string, env *tdsl.Env) (*Program, []*tdsl.Error) {
	s, errs := tdsl.Parse(src)
	if len(errs) == 0 {
		errs = tdsl.Check(s, env)
	}
	if len(errs) > 0 {
		for _, e := range errs {
			e.File = file
		}
		return nil, errs
	}
	return Compile(file, src, s), nil
}

// Loader reads the script named in a "use" line (the path as written there).
type Loader func(path string) (file, src string, err error)

// MaxUses bounds the "use" lines a script may have.
const MaxUses = 16

// CompileSourceLibs is CompileSource that also links the scripts named by
// "use". A library that cannot be read, has errors, or holds anything but "to"
// commands is reported in Program.Warnings and (in the first two cases)
// skipped; what is wrong in it shows when a command from it is called.
func CompileSourceLibs(file, src string, env *tdsl.Env, load Loader) (*Program, []*tdsl.Error) {
	s, errs := tdsl.Parse(src)
	if len(errs) == 0 {
		errs = tdsl.Check(s, env)
	}
	if len(errs) > 0 {
		for _, e := range errs {
			e.File = file
		}
		return nil, errs
	}
	var libs []Lib
	var warns []string
	seen := map[string]bool{}
	for _, it := range s.Items {
		u, ok := it.(*tdsl.UseDecl)
		if !ok || load == nil || seen[u.Path] {
			continue
		}
		seen[u.Path] = true
		if len(seen) > MaxUses {
			warns = append(warns, fmt.Sprintf("%s, строка %d: слишком много строк «use» (не больше %d).", file, u.Line, MaxUses))
			break
		}
		lf, ls, err := load(u.Path)
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s, строка %d: не могу подключить «%s»: %v.", file, u.Line, u.Path, err))
			continue
		}
		lscript, lerrs := tdsl.Parse(ls)
		if len(lerrs) == 0 {
			lerrs = tdsl.Check(lscript, env)
		}
		if len(lerrs) > 0 {
			e := lerrs[0]
			warns = append(warns, fmt.Sprintf("%s, строка %d: в подключённом файле %s ошибка (%s): %s", file, u.Line, lf, e.Code, e.Message))
			continue
		}
		bad := false
		for _, li := range lscript.Items {
			if _, ok := li.(*tdsl.ActionDef); !ok {
				bad = true
			}
		}
		if bad {
			warns = append(warns, fmt.Sprintf("%s, строка %d: в подключаемом файле %s допустимы только команды «to»; остальное игнорируется.", file, u.Line, lf))
		}
		libs = append(libs, Lib{File: lf, Src: ls, Script: lscript})
	}
	p := CompileLibs(file, src, s, libs)
	p.Warnings = append(p.Warnings, warns...)
	return p, nil
}

type loopCtx struct {
	breaks, conts []int
}

type compiler struct {
	p         *Program
	globals   map[string]int
	shared    map[string]bool
	actions   map[string]int
	actionDef []*tdsl.ActionDef
	actionLib []*Lib // the library of each action, nil for the script's own
	libs      []Lib
	constNum  map[uint64]int
	constText map[string]int
	warned    map[string]bool

	fn     *Func
	scopes []map[string]int
	loops  []*loopCtx
}

func (c *compiler) run(s *tdsl.Script) {
	// Pass 1: names.
	for _, it := range s.Items {
		switch n := it.(type) {
		case *tdsl.VarDecl:
			if _, dup := c.globals[n.Name]; !dup {
				c.globals[n.Name] = len(c.p.Globals)
				c.p.Globals = append(c.p.Globals, n.Name)
			}
		case *tdsl.SharedDecl:
			c.shared[n.Name] = true
		case *tdsl.UseDecl:
			c.p.Uses = append(c.p.Uses, n.Path)
		case *tdsl.ActionDef:
			c.addAction(n, nil)
		}
	}
	for i := range c.libs {
		lib := &c.libs[i]
		for _, it := range lib.Script.Items {
			if n, ok := it.(*tdsl.ActionDef); ok {
				c.addAction(n, lib)
			}
		}
	}
	// Pass 2: bodies.
	for i, def := range c.actionDef {
		c.begin(c.p.Funcs[i])
		if lib := c.actionLib[i]; lib != nil {
			c.fn.File, c.fn.lines = lib.File, strings.Split(strings.TrimPrefix(lib.Src, "\xef\xbb\xbf"), "\n")
		}
		for _, prm := range def.Params {
			c.declare(prm.Name)
		}
		c.block(def.Body)
		c.end(def.Pos)
	}
	c.p.Inits = make([]*Func, len(c.p.Globals))
	for _, it := range s.Items {
		if n, ok := it.(*tdsl.VarDecl); ok {
			g := c.globals[n.Name]
			if c.p.Inits[g] != nil {
				continue
			}
			c.p.Inits[g] = &Func{Name: "<var " + n.Name + ">"}
			c.begin(c.p.Inits[g])
			c.expr(n.Value)
			c.emit(spanAt(n.NamePos, len(n.Name)), OpStoreGlobal, g, 0)
			c.end(tdsl.Pos{})
		}
	}
	for _, it := range s.Items {
		if n, ok := it.(*tdsl.SharedDecl); ok {
			init := &Func{Name: "<shared " + n.Name + ">"}
			c.begin(init)
			c.expr(n.Value)
			c.emit(exprSpan(n.Value), OpReturn, 0, 0)
			c.end(tdsl.Pos{})
			c.p.Shared = append(c.p.Shared, SharedDef{Name: n.Name, Line: n.Line, Init: init})
		}
	}
	for _, it := range s.Items {
		if h, ok := it.(*tdsl.Handler); ok {
			c.handler(h)
		}
	}
}

func (c *compiler) addAction(n *tdsl.ActionDef, lib *Lib) {
	if _, dup := c.actions[n.Name]; dup {
		return
	}
	c.actions[n.Name] = len(c.p.Funcs)
	c.p.Funcs = append(c.p.Funcs, &Func{Name: n.Name, NParams: len(n.Params)})
	c.actionDef = append(c.actionDef, n)
	c.actionLib = append(c.actionLib, lib)
}

func (c *compiler) handler(h *tdsl.Handler) {
	info := HandlerInfo{Event: h.Event, Key: h.Key, State: h.State, Line: h.Line, Supported: true}
	switch h.Event {
	case tdsl.EventStart:
		info.Desc = "when start"
	case tdsl.EventUpdate:
		info.Desc = "when update"
	case tdsl.EventKey:
		key := h.Key
		if !isWord(key) {
			key = `"` + key + `"`
		}
		info.Desc = "when key " + key + " " + string(h.State)
	case tdsl.EventEvery:
		info.Desc = "when every " + h.Arg.String() + " seconds"
	case tdsl.EventClick:
		info.Desc = "when click"
	case tdsl.EventTouch:
		info.Desc = "when touch " + h.Arg.String()
	case tdsl.EventStopTouching:
		info.Desc = "when stop touching " + h.Arg.String()
	case tdsl.EventMessage:
		info.Desc = "when message " + h.Arg.String()
	}
	// The body of an unsupported handler is still compiled: it is the
	// documentation of what would run, and it keeps the compiler total.
	info.fn = &Func{Name: info.Desc}
	c.begin(info.fn)
	c.block(h.Body)
	c.end(h.Pos)
	if h.Event == tdsl.EventEvery && h.Arg != nil {
		info.period = &Func{Name: info.Desc + " (период)"}
		c.begin(info.period)
		c.expr(h.Arg)
		c.emit(exprSpan(h.Arg), OpReturn, 0, 0)
		c.end(h.Pos)
	}
	if (h.Event == tdsl.EventTouch || h.Event == tdsl.EventStopTouching || h.Event == tdsl.EventMessage) && h.Arg != nil {
		if txt, ok := h.Arg.(*tdsl.Text); ok {
			if lit, ok := txt.Literal(); ok {
				info.argText, info.argConst = lit, true
			}
		}
		if !info.argConst {
			info.arg = &Func{Name: info.Desc + " (аргумент)"}
			c.begin(info.arg)
			c.expr(h.Arg)
			c.emit(exprSpan(h.Arg), OpReturn, 0, 0)
			c.end(h.Pos)
		}
	}
	c.p.Handlers = append(c.p.Handlers, info)
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127) {
			return false
		}
	}
	return !(s[0] >= '0' && s[0] <= '9')
}

// ---- function building ----

func (c *compiler) begin(f *Func) {
	c.fn = f
	c.scopes = []map[string]int{{}}
	c.loops = nil
}

// end finishes the function: falling off the end returns nothing.
func (c *compiler) end(p tdsl.Pos) {
	sp := spanAt(p, 1)
	c.emit(sp, OpNothing, 0, 0)
	c.emit(sp, OpReturn, 0, 0)
}

func (c *compiler) emit(sp Span, op Op, a, b int) int {
	c.fn.Code = append(c.fn.Code, Instr{Op: op, A: int32(a), B: int32(b)})
	c.fn.Spans = append(c.fn.Spans, sp)
	return len(c.fn.Code) - 1
}

func (c *compiler) here() int { return len(c.fn.Code) }

func (c *compiler) patch(at, target int) { c.fn.Code[at].A = int32(target) }

func (c *compiler) push() { c.scopes = append(c.scopes, map[string]int{}) }
func (c *compiler) pop()  { c.scopes = c.scopes[:len(c.scopes)-1] }

// alloc reserves a local slot.
func (c *compiler) alloc() int {
	c.fn.NLocals++
	return c.fn.NLocals - 1
}

func (c *compiler) declare(name string) int {
	slot := c.alloc()
	c.scopes[len(c.scopes)-1][name] = slot
	return slot
}

func (c *compiler) constNumber(f float64) int {
	bits := math.Float64bits(f)
	if i, ok := c.constNum[bits]; ok {
		return i
	}
	c.p.Consts = append(c.p.Consts, Num(f))
	c.constNum[bits] = len(c.p.Consts) - 1
	return len(c.p.Consts) - 1
}

func (c *compiler) constString(s string) int {
	if i, ok := c.constText[s]; ok {
		return i
	}
	c.p.Consts = append(c.p.Consts, Text(s))
	c.constText[s] = len(c.p.Consts) - 1
	return len(c.p.Consts) - 1
}

func (c *compiler) addFail(code, msg, hint string) int {
	c.p.fails = append(c.p.fails, failure{code, msg, hint})
	return len(c.p.fails) - 1
}

func (c *compiler) warn(line int, feature string) {
	key := fmt.Sprintf("%d:%s", line, feature)
	if c.warned[key] {
		return
	}
	c.warned[key] = true
	c.p.Warnings = append(c.p.Warnings, fmt.Sprintf("%s, строка %d: %s пока не поддерживается в этой версии.", c.p.File, line, feature))
}

// unsupported emits an instruction that stops the run with the "not supported"
// error. Nothing after it in the same handler run executes.
func (c *compiler) unsupported(sp Span, feature string) {
	c.warn(int(sp.Line), feature)
	c.emit(sp, OpFail, c.addFail(CodeUnsupported,
		"Возможность «"+feature+"» пока не поддерживается в этой версии TrueDSL.",
		"Её можно будет использовать в следующих версиях; пока уберите эту строку."), 0)
}

func (c *compiler) mistake(sp Span, code, msg string) {
	c.emit(sp, OpFail, c.addFail(code, msg, ""), 0)
}

// ---- spans ----

func spanAt(p tdsl.Pos, width int) Span {
	return Span{int32(p.Line), int32(p.Col), int32(p.Col + max(width, 1))}
}

func exprSpan(e tdsl.Expr) Span {
	p := e.Position()
	l, col := exprEnd(e)
	if l != p.Line || col <= p.Col {
		col = p.Col + 1
	}
	return Span{int32(p.Line), int32(p.Col), int32(col)}
}

func joinSpan(a, b Span) Span {
	if a.Line == b.Line && b.EndCol > a.Col {
		return Span{a.Line, a.Col, b.EndCol}
	}
	return a
}

func runeLen(s string) int { return len([]rune(s)) }

// exprEnd estimates the position just after the last character of e.
func exprEnd(e tdsl.Expr) (line, col int) {
	p := e.Position()
	switch n := e.(type) {
	case *tdsl.Number:
		if n.Len > 0 {
			return p.Line, p.Col + n.Len
		}
		return p.Line, p.Col + runeLen(n.String())
	case *tdsl.Text:
		if n.Len > 0 {
			return p.Line, p.Col + n.Len
		}
	case *tdsl.Bool:
		return p.Line, p.Col + runeLen(n.String())
	case *tdsl.Nothing:
		return p.Line, p.Col + 7
	case *tdsl.Dir:
		return p.Line, p.Col + runeLen(n.Name)
	case *tdsl.Self:
		return p.Line, p.Col + 4
	case *tdsl.Ident:
		return p.Line, p.Col + runeLen(n.Name)
	case *tdsl.Unary:
		return exprEnd(n.X)
	case *tdsl.Binary:
		return exprEnd(n.R)
	case *tdsl.PerSecond:
		l, c := exprEnd(n.X)
		return l, c + 11
	case *tdsl.Member:
		l, c := exprEnd(n.X)
		return l, c + 1 + runeLen(n.Name)
	case *tdsl.Index:
		l, c := exprEnd(n.I)
		return l, c + 1
	case *tdsl.Call:
		if len(n.Args) == 0 {
			return p.Line, p.Col + runeLen(n.Name) + 2
		}
		l, c := exprEnd(n.Args[len(n.Args)-1])
		return l, c + 1
	case *tdsl.List:
		if len(n.Elems) == 0 {
			return p.Line, p.Col + 2
		}
		l, c := exprEnd(n.Elems[len(n.Elems)-1])
		return l, c + 1
	}
	return p.Line, p.Col + 1
}

// ---- names ----

type refKind int

const (
	refUnknown refKind = iota
	refLocal
	refGlobal
	refProp
	refName // a shared variable or a scene object, decided when the script runs
)

type ref struct {
	kind refKind
	idx  int
	prop Prop
}

func (c *compiler) resolve(name string) ref {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if slot, ok := c.scopes[i][name]; ok {
			return ref{kind: refLocal, idx: slot}
		}
	}
	if g, ok := c.globals[name]; ok {
		return ref{kind: refGlobal, idx: g}
	}
	if p, ok := propByName[name]; ok {
		return ref{kind: refProp, prop: p}
	}
	return ref{kind: refName}
}

// ---- statements ----

func (c *compiler) block(body []tdsl.Stmt) {
	c.push()
	for _, s := range body {
		c.stmt(s)
	}
	c.pop()
}

var assignOps = map[string]Op{"+=": OpAdd, "-=": OpSub, "*=": OpMul, "/=": OpDiv}

func (c *compiler) stmt(s tdsl.Stmt) {
	switch n := s.(type) {
	case *tdsl.VarDecl:
		c.expr(n.Value)
		slot := c.declare(n.Name)
		c.emit(spanAt(n.NamePos, runeLen(n.Name)), OpStoreLocal, slot, 0)
	case *tdsl.Assign:
		c.assign(n)
	case *tdsl.If:
		var ends []int
		for _, b := range n.Branches {
			c.expr(b.Cond)
			jf := c.emit(exprSpan(b.Cond), OpJumpIfFalse, 0, 0)
			c.block(b.Body)
			ends = append(ends, c.emit(spanAt(b.Pos, 2), OpJump, 0, 0))
			c.patch(jf, c.here())
		}
		c.block(n.Else)
		for _, e := range ends {
			c.patch(e, c.here())
		}
	case *tdsl.While:
		top := c.here()
		c.expr(n.Cond)
		jf := c.emit(exprSpan(n.Cond), OpJumpIfFalse, 0, 0)
		c.loopBody(n.Body)
		c.emit(spanAt(n.Pos, 5), OpJump, top, 0)
		c.endLoop(jf, top, c.here())
	case *tdsl.Repeat:
		c.repeat(n)
	case *tdsl.For:
		c.forEach(n)
	case *tdsl.StopRepeating:
		if len(c.loops) == 0 {
			c.mistake(spanAt(n.Pos, 4), CodeWrongType, "«stop repeating» можно писать только внутри цикла.")
			return
		}
		l := c.loops[len(c.loops)-1]
		l.breaks = append(l.breaks, c.emit(spanAt(n.Pos, 4), OpJump, 0, 0))
	case *tdsl.NextRound:
		if len(c.loops) == 0 {
			c.mistake(spanAt(n.Pos, 4), CodeWrongType, "«next round» можно писать только внутри цикла.")
			return
		}
		l := c.loops[len(c.loops)-1]
		l.conts = append(l.conts, c.emit(spanAt(n.Pos, 4), OpJump, 0, 0))
	case *tdsl.Return:
		if n.Value != nil {
			c.expr(n.Value)
		} else {
			c.emit(spanAt(n.Pos, 6), OpNothing, 0, 0)
		}
		c.emit(spanAt(n.Pos, 6), OpReturn, 0, 0)
	case *tdsl.ExprStmt:
		c.expr(n.X)
		c.emit(exprSpan(n.X), OpPop, 0, 0)
	case *tdsl.Move:
		sp := spanAt(n.Pos, 4)
		c.expr(n.Dir)
		c.expr(n.By)
		if n.Avoiding != nil {
			c.expr(n.Avoiding)
			c.emit(sp, OpMoveAvoid, 0, 0)
			return
		}
		c.emit(sp, OpMove, 0, 0)
	case *tdsl.MoveTo:
		c.expr(n.X)
		c.expr(n.Y)
		c.emit(spanAt(n.Pos, 4), OpMoveTo, 0, 0)
	case *tdsl.Turn:
		sp := spanAt(n.Pos, 4)
		c.emit(sp, OpSelf, 0, 0)
		c.emit(sp, OpSelf, 0, 0)
		c.emit(sp, OpGetProp, int(PropAngle), 0)
		c.expr(n.By)
		c.emit(sp, OpAdd, 0, 0)
		c.emit(sp, OpSetProp, int(PropAngle), 0)
	case *tdsl.Show:
		c.setBool(spanAt(n.Pos, 4), PropVisible, true)
	case *tdsl.Hide:
		c.setBool(spanAt(n.Pos, 4), PropVisible, false)
	case *tdsl.Destroy:
		sp := spanAt(n.Pos, 7)
		if n.Target == nil {
			c.emit(sp, OpSelf, 0, 0)
		} else {
			c.expr(n.Target)
		}
		c.emit(sp, OpDestroy, 0, 0)
	case *tdsl.Print:
		c.expr(n.Value)
		c.emit(spanAt(n.Pos, 5), OpPrint, 0, 0)
	case *tdsl.ListCmd:
		c.listCmd(n)
	case *tdsl.SlideTo:
		c.unsupported(spanAt(n.Pos, 5), "slide (плавное движение)")
	case *tdsl.Wait:
		c.unsupported(spanAt(n.Pos, 4), "wait (ожидание)")
	case *tdsl.Play:
		c.unsupported(spanAt(n.Pos, 4), "play (звуки и музыка)")
	case *tdsl.StopMusic:
		c.unsupported(spanAt(n.Pos, 4), "stop music (звуки и музыка)")
	case *tdsl.Send:
		c.expr(n.Message)
		if n.With != nil {
			c.expr(n.With)
		} else {
			c.emit(spanAt(n.Pos, 4), OpNothing, 0, 0)
		}
		hasTo := 0
		if n.To != nil {
			c.expr(n.To)
			hasTo = 1
		} else {
			c.emit(spanAt(n.Pos, 4), OpNothing, 0, 0)
		}
		c.emit(spanAt(n.Pos, 4), OpSend, 0, hasTo)
	case *tdsl.GoToScene:
		c.unsupported(spanAt(n.Pos, 2), "go to scene (переход между сценами)")
	case *tdsl.RestartScene:
		c.unsupported(spanAt(n.Pos, 7), "restart scene (перезапуск сцены)")
	case *tdsl.Remember:
		c.unsupported(spanAt(n.Pos, 8), "remember (память игры)")
	default:
		c.unsupported(spanAt(s.Position(), 1), fmt.Sprintf("команда %T", s))
	}
}

func (c *compiler) setBool(sp Span, p Prop, v bool) {
	c.emit(sp, OpSelf, 0, 0)
	if v {
		c.emit(sp, OpTrue, 0, 0)
	} else {
		c.emit(sp, OpFalse, 0, 0)
	}
	c.emit(sp, OpSetProp, int(p), 0)
}

func (c *compiler) listCmd(n *tdsl.ListCmd) {
	sp := spanAt(n.Pos, runeLen(n.Op))
	if n.Item != nil {
		c.expr(n.Item)
	}
	c.expr(n.List)
	op := map[string]Op{"add": OpListAppend, "remove": OpListRemove, "clear": OpListClear,
		"sort": OpListSort, "shuffle": OpListShuffle}[n.Op]
	c.emit(sp, op, 0, 0)
}

// loopBody compiles a loop body with break/continue bookkeeping.
func (c *compiler) loopBody(body []tdsl.Stmt) {
	c.loops = append(c.loops, &loopCtx{})
	c.block(body)
}

// endLoop closes the loop opened by loopBody: breaks go to exit, continues to
// cont; jf (if >= 0) is the condition's exit jump.
func (c *compiler) endLoop(jf, cont, exit int) {
	l := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	if jf >= 0 {
		c.patch(jf, exit)
	}
	for _, b := range l.breaks {
		c.patch(b, exit)
	}
	for _, k := range l.conts {
		c.patch(k, cont)
	}
}

func (c *compiler) repeat(n *tdsl.Repeat) {
	sp := spanAt(n.Pos, 6)
	if n.Count == nil { // repeat forever
		top := c.here()
		c.loopBody(n.Body)
		c.emit(sp, OpJump, top, 0)
		c.endLoop(-1, top, c.here())
		return
	}
	lim, i := c.alloc(), c.alloc()
	c.expr(n.Count)
	c.emit(exprSpan(n.Count), OpCheckNum, c.constString("Число повторений в «repeat … times»"), 0)
	c.emit(sp, OpStoreLocal, lim, 0)
	c.emit(sp, OpConst, c.constNumber(1), 0)
	c.emit(sp, OpStoreLocal, i, 0)
	top := c.here()
	c.emit(sp, OpLoadLocal, i, 0)
	c.emit(sp, OpLoadLocal, lim, 0)
	c.emit(sp, OpLe, 0, 0)
	jf := c.emit(sp, OpJumpIfFalse, 0, 0)
	c.loopBody(n.Body)
	cont := c.here()
	c.step(sp, i)
	c.emit(sp, OpJump, top, 0)
	c.endLoop(jf, cont, c.here())
}

// step emits "slot = slot + 1".
func (c *compiler) step(sp Span, slot int) {
	c.emit(sp, OpLoadLocal, slot, 0)
	c.emit(sp, OpConst, c.constNumber(1), 0)
	c.emit(sp, OpAdd, 0, 0)
	c.emit(sp, OpStoreLocal, slot, 0)
}

func (c *compiler) forEach(n *tdsl.For) {
	sp := spanAt(n.Pos, 3)
	cur, lim := c.alloc(), c.alloc()
	var jf int
	var top int
	if n.List != nil {
		// cur is the 1-based index, lim the list.
		c.expr(n.List)
		c.emit(exprSpan(n.List), OpCheckList, c.constString("После «in»"), 0)
		c.emit(sp, OpStoreLocal, lim, 0)
		c.emit(sp, OpConst, c.constNumber(1), 0)
		c.emit(sp, OpStoreLocal, cur, 0)
		top = c.here()
		c.emit(sp, OpLoadLocal, cur, 0)
		c.emit(sp, OpLoadLocal, lim, 0)
		c.emit(sp, OpListLen, 0, 0)
		c.emit(sp, OpLe, 0, 0)
		jf = c.emit(sp, OpJumpIfFalse, 0, 0)
		c.push()
		v := c.declare(n.Var)
		c.emit(sp, OpLoadLocal, lim, 0)
		c.emit(sp, OpLoadLocal, cur, 0)
		c.emit(sp, OpIndex, -1, 0)
		c.emit(sp, OpStoreLocal, v, 0)
	} else {
		c.expr(n.To)
		c.emit(exprSpan(n.To), OpCheckNum, c.constString("Конец диапазона"), 0)
		c.emit(sp, OpStoreLocal, lim, 0)
		c.expr(n.From)
		c.emit(exprSpan(n.From), OpCheckNum, c.constString("Начало диапазона"), 0)
		c.emit(sp, OpStoreLocal, cur, 0)
		top = c.here()
		c.emit(sp, OpLoadLocal, cur, 0)
		c.emit(sp, OpLoadLocal, lim, 0)
		c.emit(sp, OpLe, 0, 0)
		jf = c.emit(sp, OpJumpIfFalse, 0, 0)
		c.push()
		v := c.declare(n.Var)
		c.emit(sp, OpLoadLocal, cur, 0)
		c.emit(sp, OpStoreLocal, v, 0)
	}
	c.loopBody(n.Body)
	c.pop()
	cont := c.here()
	c.step(sp, cur)
	c.emit(sp, OpJump, top, 0)
	c.endLoop(jf, cont, c.here())
}

func (c *compiler) assign(n *tdsl.Assign) {
	op, compound := assignOps[n.Op]
	whole := joinSpan(exprSpan(n.Target), exprSpan(n.Value))
	switch t := n.Target.(type) {
	case *tdsl.Ident:
		r := c.resolve(t.Name)
		tsp := exprSpan(t)
		switch r.kind {
		case refLocal, refGlobal:
			load, store := OpLoadLocal, OpStoreLocal
			if r.kind == refGlobal {
				load, store = OpLoadGlobal, OpStoreGlobal
			}
			if compound {
				c.emit(tsp, load, r.idx, 0)
			}
			c.expr(n.Value)
			if compound {
				c.emit(whole, op, 0, 0)
			}
			c.emit(tsp, store, r.idx, 0)
		case refProp:
			c.emit(tsp, OpSelf, 0, 0)
			if compound {
				c.emit(tsp, OpSelf, 0, 0)
				c.emit(tsp, OpGetProp, int(r.prop), 0)
			}
			c.expr(n.Value)
			if compound {
				c.emit(whole, op, 0, 0)
			}
			c.emit(tsp, OpSetProp, int(r.prop), 0)
		default: // a shared variable
			if compound {
				c.emit(tsp, OpName, c.constString(t.Name), 0)
			}
			c.expr(n.Value)
			if compound {
				c.emit(whole, op, 0, 0)
			}
			c.emit(tsp, OpSetName, c.constString(t.Name), 0)
		}
	case *tdsl.Member:
		tsp := exprSpan(t)
		c.expr(t.X)
		if compound {
			c.emit(tsp, OpDup, 0, 0)
			c.emit(tsp, OpMember, c.constString(t.Name), int(propByName[t.Name]))
		}
		c.expr(n.Value)
		if compound {
			c.emit(whole, op, 0, 0)
		}
		c.emit(tsp, OpSetMember, c.constString(t.Name), int(propByName[t.Name]))
	case *tdsl.Index:
		tsp := exprSpan(t)
		name := -1
		if id, ok := t.X.(*tdsl.Ident); ok {
			name = c.constString(id.Name)
		}
		c.expr(t.X)
		c.expr(t.I)
		if compound {
			c.emit(tsp, OpDup2, 0, 0)
			c.emit(tsp, OpIndex, name, 0)
		}
		c.expr(n.Value)
		if compound {
			c.emit(whole, op, 0, 0)
		}
		c.emit(tsp, OpSetIndex, name, 0)
	default:
		c.mistake(whole, CodeWrongType, "Этому нельзя ничего присвоить.")
	}
}

// ---- expressions ----

var binOps = map[string]Op{
	"+": OpAdd, "-": OpSub, "*": OpMul, "/": OpDiv, "%": OpMod,
	"is": OpEq, "is not": OpNe, "<": OpLt, ">": OpGt, "<=": OpLe, ">=": OpGe,
	"in": OpIn, "not in": OpNotIn,
}

func (c *compiler) expr(e tdsl.Expr) {
	sp := exprSpan(e)
	switch n := e.(type) {
	case *tdsl.Number:
		c.emit(sp, OpConst, c.constNumber(n.Value), 0)
	case *tdsl.Text:
		if lit, ok := n.Literal(); ok {
			c.emit(sp, OpConst, c.constString(lit), 0)
			return
		}
		for _, part := range n.Parts {
			if part.Expr != nil {
				c.expr(part.Expr)
			} else {
				c.emit(sp, OpConst, c.constString(part.Lit), 0)
			}
		}
		c.emit(sp, OpText, len(n.Parts), 0)
	case *tdsl.Bool:
		if n.Value {
			c.emit(sp, OpTrue, 0, 0)
		} else {
			c.emit(sp, OpFalse, 0, 0)
		}
	case *tdsl.Nothing:
		c.emit(sp, OpNothing, 0, 0)
	case *tdsl.Dir:
		d := map[string]Dir{"left": Left, "right": Right, "up": Up, "down": Down}[n.Name]
		c.emit(sp, OpDir, int(d), 0)
	case *tdsl.Self:
		c.emit(sp, OpSelf, 0, 0)
	case *tdsl.Other:
		c.emit(sp, OpOther, 0, 0)
	case *tdsl.ValueRef:
		c.emit(sp, OpValue, 0, 0)
	case *tdsl.Ident:
		r := c.resolve(n.Name)
		switch r.kind {
		case refLocal:
			c.emit(sp, OpLoadLocal, r.idx, 0)
		case refGlobal:
			c.emit(sp, OpLoadGlobal, r.idx, 0)
		case refProp:
			c.emit(sp, OpSelf, 0, 0)
			c.emit(sp, OpGetProp, int(r.prop), 0)
		default:
			c.emit(sp, OpName, c.constString(n.Name), 0)
		}
	case *tdsl.Unary:
		c.expr(n.X)
		if n.Op == "not" {
			c.emit(sp, OpNot, 0, 0)
		} else {
			c.emit(sp, OpNeg, 0, 0)
		}
	case *tdsl.Binary:
		c.binary(n, sp)
	case *tdsl.PerSecond:
		c.expr(n.X)
		c.emit(sp, OpPerSecond, 0, 0)
	case *tdsl.Member:
		if id, ok := n.X.(*tdsl.Ident); ok && id.Name == "mouse" && c.resolve("mouse").kind == refName {
			switch n.Name {
			case "x":
				c.emit(sp, OpMouse, 0, 0)
			case "y":
				c.emit(sp, OpMouse, 1, 0)
			case "held":
				c.emit(sp, OpMouse, 2, 0)
			default:
				c.mistake(sp, CodeNoProp, "У мыши нет свойства «"+n.Name+"»: есть mouse.x, mouse.y и mouse.held.")
			}
			return
		}
		c.expr(n.X)
		c.emit(sp, OpMember, c.constString(n.Name), int(propByName[n.Name]))
	case *tdsl.Index:
		name := -1
		if id, ok := n.X.(*tdsl.Ident); ok {
			name = c.constString(id.Name)
		}
		c.expr(n.X)
		c.expr(n.I)
		c.emit(sp, OpIndex, name, 0)
	case *tdsl.List:
		for _, x := range n.Elems {
			c.expr(x)
		}
		c.emit(sp, OpList, len(n.Elems), 0)
	case *tdsl.Call:
		c.call(n, sp)
	case *tdsl.KeyDown:
		c.emit(sp, OpKey, c.constString(n.Key), 0)
	case *tdsl.Spawn:
		c.spawn(n, sp)
	default:
		c.unsupported(sp, fmt.Sprintf("выражение %T", e))
	}
}

// spawn compiles "spawn template at x, y". A bare name stands for the template
// of that name (templates are not objects of the running scene).
func (c *compiler) spawn(n *tdsl.Spawn, sp Span) {
	if id, ok := n.Template.(*tdsl.Ident); ok && c.resolve(id.Name).kind == refName {
		c.emit(exprSpan(id), OpConst, c.constString(id.Name), 0)
	} else {
		c.expr(n.Template)
	}
	c.expr(n.X)
	c.expr(n.Y)
	c.emit(spanAt(n.Pos, 5), OpSpawn, 0, 0)
}

func (c *compiler) binary(n *tdsl.Binary, sp Span) {
	switch n.Op {
	case "and", "or":
		c.expr(n.L)
		op := OpAndJump
		if n.Op == "or" {
			op = OpOrJump
		}
		j := c.emit(exprSpan(n.L), op, 0, 0)
		c.expr(n.R)
		c.emit(exprSpan(n.R), OpCheckBool, 0, 0)
		c.patch(j, c.here())
		return
	}
	c.expr(n.L)
	c.expr(n.R)
	c.emit(sp, binOps[n.Op], 0, 0)
}

func (c *compiler) call(n *tdsl.Call, sp Span) {
	nameSp := spanAt(n.Pos, runeLen(n.Name))
	if fi, ok := c.actions[n.Name]; ok {
		def := c.actionDef[fi]
		if len(n.Args) != len(def.Params) {
			c.mistake(nameSp, "TD203", fmt.Sprintf("Команда «%s» принимает %d %s, а передано %d.",
				n.Name, len(def.Params), plural(len(def.Params), "значение", "значения", "значений"), len(n.Args)))
			return
		}
		for _, a := range n.Args {
			c.expr(a)
		}
		c.emit(nameSp, OpCall, fi, len(n.Args))
		return
	}
	if id, ok := builtinIDs[n.Name]; ok {
		if mx, ok := builtinMax[id]; (!ok && len(n.Args) != builtinArgs[id]) || (ok && (len(n.Args) < builtinArgs[id] || len(n.Args) > mx)) {
			c.mistake(nameSp, "TD203", fmt.Sprintf("Функция «%s» принимает %d %s, а передано %d.",
				n.Name, builtinArgs[id], plural(builtinArgs[id], "значение", "значения", "значений"), len(n.Args)))
			return
		}
		for _, a := range n.Args {
			c.expr(a)
		}
		c.emit(nameSp, OpBuiltin, id, len(n.Args))
		return
	}
	if what, ok := unsupportedBuiltins[n.Name]; ok {
		c.unsupported(nameSp, what)
		return
	}
	c.mistake(nameSp, CodeUnknownName, "Я не знаю команды или функции «"+n.Name+"».")
}
