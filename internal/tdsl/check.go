package tdsl

import (
	"fmt"
	"sort"
	"strings"
)

// Коды семантических ошибок (см. docs/truedsl/errors.md).
const (
	CodeUnknownName    = "TD101" // нет такой переменной
	CodeDuplicate      = "TD102" // такое имя уже есть
	CodeReservedName   = "TD103" // слово занято языком
	CodeWaitEveryFrame = "TD104" // wait/slide в обработчике «каждый кадр»
	CodeSharedTwice    = "TD105" // shared объявлен дважды
	CodeReadOnly       = "TD107" // нельзя изменить
	CodeCompare        = "TD201" // сравнение разных типов
	CodeCondition      = "TD202" // условие не «да/нет»
	CodeArgCount       = "TD203" // неверное число значений
	CodeAddTypes       = "TD204" // число + текст
	CodeWrongType      = "TD205" // ожидалось другое по смыслу
	CodeNoFile         = "TD304" // файл не найден
	CodeNoScene        = "TD305" // нет такой сцены
)

// maxCheckErrors — предел числа ошибок, возвращаемых Check.
const maxCheckErrors = 50

// Env описывает проект, в котором живёт скрипт. Все поля необязательны: nil означает
// «ничего не известно», и проверки, которым нужно знание проекта, пропускаются.
type Env struct {
	Objects   []string // имена объектов сцены
	Tags      []string // известные метки (сейчас не проверяются)
	Assets    []string // картинки, звуки, музыка
	Scripts   []string // доступные .tdsl
	Templates []string // шаблоны для spawn
	Scenes    []string // имена сцен
	// SharedDeclared — shared-переменные, уже объявленные в других скриптах проекта
	// (значение — имя файла).
	SharedDeclared map[string]string
}

type checker struct {
	env    *Env
	errs   []*Error
	quiet  bool // не сообщать об ошибках (вывод типов)
	scopes []map[string]bool
	top    map[string]bool // верхнеуровневые var и shared этого скрипта

	actions map[string]*ActionDef
	waits   map[string]bool // команды, внутри которых (транзитивно) есть wait/slide

	allVars map[string]bool  // все объявленные где-либо имена переменных
	vt      map[string]vtype // выведенные типы переменных (по имени)

	h          *Handler // текущий обработчик
	inAction   bool
	everyFrame bool // update и key ... held: wait/slide запрещены
	libMode    bool // нет обработчиков: библиотека команд, имена берутся у объекта-хозяина
	hasUse     bool
}

// Check выполняет статическую проверку разобранного скрипта: имена, области видимости,
// типы там, где они известны, число аргументов. Принцип: лучше промолчать, чем ошибиться.
// Возвращает не более 50 ошибок, отсортированных по позиции. env может быть nil.
func Check(s *Script, env *Env) []*Error {
	if s == nil {
		return nil
	}
	if env == nil {
		env = &Env{}
	}
	c := &checker{
		env: env, top: map[string]bool{}, actions: map[string]*ActionDef{},
		waits: map[string]bool{}, allVars: map[string]bool{}, vt: map[string]vtype{},
		libMode: true,
	}
	c.run(s)
	return c.finish()
}

func (c *checker) finish() []*Error {
	sort.SliceStable(c.errs, func(i, j int) bool {
		a, b := c.errs[i], c.errs[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	var out []*Error
	seen := map[string]bool{}
	for _, e := range c.errs {
		k := fmt.Sprintf("%d:%d:%s", e.Line, e.Col, e.Code)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
		if len(out) == maxCheckErrors {
			break
		}
	}
	return out
}

func (c *checker) addAt(code string, line, col, end int, msg, hint string) {
	if c.quiet {
		return
	}
	if col < 1 {
		col = 1
	}
	if end <= col {
		end = col + 1
	}
	c.errs = append(c.errs, &Error{Code: code, Line: line, Col: col, EndCol: end, Message: msg, Hint: hint})
}

// addName — ошибка под именем, начинающимся в позиции p.
func (c *checker) addName(code string, p Pos, name, msg, hint string) {
	c.addAt(code, p.Line, p.Col, p.Col+rl(name), msg, hint)
}

// addExpr — ошибка под всем выражением e.
func (c *checker) addExpr(code string, e Expr, msg, hint string) {
	p := e.Position()
	l, col := endOf(e)
	if l != p.Line {
		col = p.Col + 1
	}
	c.addAt(code, p.Line, p.Col, col, msg, hint)
}

func (c *checker) run(s *Script) {
	// 1. Команды и общие сведения.
	for _, it := range s.Items {
		switch n := it.(type) {
		case *Handler:
			c.libMode = false
		case *UseDecl:
			c.hasUse = true
		case *ActionDef:
			if n == nil {
				continue
			}
			// занятое имя всё равно запоминаем, чтобы проверять вызовы
			reserved := c.reserved(n.Name, n.NamePos)
			if _, dup := c.actions[n.Name]; dup {
				if !reserved {
					c.addName(CodeDuplicate, n.NamePos, n.Name,
						"Команда «"+n.Name+"» уже объявлена выше.", "Дайте второй команде другое имя.")
				}
				continue
			}
			c.actions[n.Name] = n
		}
	}
	// 2. Верхнеуровневые переменные.
	for _, it := range s.Items {
		switch n := it.(type) {
		case *VarDecl:
			c.declTop(n.Name, n.NamePos, false)
		case *SharedDecl:
			c.declTop(n.Name, n.NamePos, true)
		}
	}
	// 3. Все имена переменных и выведенные типы; команды, которые ждут.
	c.infer(s)
	c.computeWaits()
	// 4. Проверка тел.
	for _, it := range s.Items {
		switch n := it.(type) {
		case *VarDecl:
			c.expr(n.Value)
		case *SharedDecl:
			c.expr(n.Value)
		case *UseDecl:
			c.checkUse(n)
		case *ActionDef:
			c.checkAction(n)
		case *Handler:
			c.checkHandler(n)
		}
	}
}

// ---- Имена ----

// reserved сообщает TD103, если слово занято языком; возвращает true, если занято.
func (c *checker) reserved(name string, p Pos) bool {
	var why string
	switch {
	case objectProps[name] != tyUnknown:
		why = "это встроенное свойство объекта"
	case name == "mouse":
		why = "это встроенный объект мыши"
	case keywordSet[name]:
		why = "это слово языка"
	default:
		if _, ok := builtins[name]; ok {
			why = "это имя встроенной функции"
		}
	}
	if why == "" {
		return false
	}
	c.addName(CodeReservedName, p, name, "Слово «"+name+"» занято: "+why+".",
		"Выберите другое имя, например «my_"+name+"».")
	return true
}

func (c *checker) declTop(name string, p Pos, shared bool) {
	if c.reserved(name, p) {
		return
	}
	if shared {
		if file, ok := c.env.SharedDeclared[name]; ok {
			where := "в другом скрипте"
			if file != "" {
				where = "в скрипте " + file
			}
			c.addName(CodeSharedTwice, p, name, "Общая переменная «"+name+"» уже объявлена "+where+".",
				"Объявите её только в одном месте, в остальных скриптах просто используйте имя.")
			return
		}
	} else if _, ok := c.env.SharedDeclared[name]; ok {
		c.addName(CodeDuplicate, p, name, "Имя «"+name+"» уже занято общей переменной из другого скрипта.", "Выберите другое имя.")
		return
	}
	if c.top[name] {
		if shared {
			c.addName(CodeSharedTwice, p, name, "Общая переменная «"+name+"» объявлена дважды в этом скрипте.",
				"Оставьте одно объявление.")
		} else {
			c.addName(CodeDuplicate, p, name, "Имя «"+name+"» уже есть: его нельзя объявить второй раз.",
				"Чтобы изменить значение, напишите «"+name+" = ...» без «var».")
		}
		return
	}
	c.top[name] = true
}

func (c *checker) push() { c.scopes = append(c.scopes, map[string]bool{}) }
func (c *checker) pop()  { c.scopes = c.scopes[:len(c.scopes)-1] }

func (c *checker) declared(name string) bool {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if c.scopes[i][name] {
			return true
		}
	}
	if c.top[name] {
		return true
	}
	_, ok := c.env.SharedDeclared[name]
	return ok
}

// declareLocal объявляет имя в текущей области (var в блоке, параметр, переменная цикла).
func (c *checker) declareLocal(name string, p Pos) {
	if c.reserved(name, p) {
		return
	}
	if c.declared(name) {
		c.addName(CodeDuplicate, p, name, "Имя «"+name+"» уже есть: его нельзя объявить второй раз (в том числе внутри блока).",
			"Чтобы изменить значение, напишите «"+name+" = ...» без «var»; или выберите другое имя.")
	}
	if len(c.scopes) > 0 {
		c.scopes[len(c.scopes)-1][name] = true
	}
}

func (c *checker) candidates(withObjects bool) []string {
	var out []string
	for _, sc := range c.scopes {
		for n := range sc {
			out = append(out, n)
		}
	}
	for n := range c.top {
		out = append(out, n)
	}
	for n := range c.env.SharedDeclared {
		out = append(out, n)
	}
	if withObjects {
		out = append(out, c.env.Objects...)
	}
	out = append(out, propNames...)
	sort.Strings(out)
	return out
}

func (c *checker) canReportUnknown() bool {
	return !c.libMode && c.env.Objects != nil
}

// ---- Предварительный проход: типы переменных и «ждущие» команды ----

func (c *checker) infer(s *Script) {
	got := map[string]vtype{}
	poison := map[string]bool{}
	add := func(name string, t vtype, decl bool) {
		if decl {
			c.allVars[name] = true
		}
		if t == tyUnknown || t == tyNothing {
			poison[name] = true
			return
		}
		if old, ok := got[name]; ok && old != t {
			poison[name] = true
		}
		got[name] = t
	}
	c.quiet = true
	defer func() { c.quiet = false }()
	var walk func(body []Stmt)
	walk = func(body []Stmt) {
		eachStmt(body, func(st Stmt) {
			switch n := st.(type) {
			case *VarDecl:
				add(n.Name, c.expr(n.Value), true)
			case *Assign:
				if id, ok := n.Target.(*Ident); ok && n.Op == "=" {
					add(id.Name, c.expr(n.Value), false)
				}
			case *For:
				c.allVars[n.Var] = true
				poison[n.Var] = true
			}
		})
	}
	for _, it := range s.Items {
		switch n := it.(type) {
		case *VarDecl:
			add(n.Name, c.expr(n.Value), true)
		case *SharedDecl:
			add(n.Name, c.expr(n.Value), true)
		case *ActionDef:
			for _, p := range n.Params {
				c.allVars[p.Name] = true
				poison[p.Name] = true
			}
			walk(n.Body)
		case *Handler:
			walk(n.Body)
		}
	}
	for name, t := range got {
		if !poison[name] && c.allVars[name] {
			c.vt[name] = t
		}
	}
	for name := range c.env.SharedDeclared {
		c.allVars[name] = true
	}
}

func (c *checker) computeWaits() {
	callees := map[string][]string{}
	for name, act := range c.actions {
		eachStmt(act.Body, func(st Stmt) {
			switch st.(type) {
			case *Wait, *SlideTo:
				c.waits[name] = true
			}
			for _, e := range stmtExprs(st) {
				eachExpr(e, func(x Expr) {
					if call, ok := x.(*Call); ok {
						if _, ok := c.actions[call.Name]; ok {
							callees[name] = append(callees[name], call.Name)
						}
					}
				})
			}
		})
	}
	for changed, i := true, 0; changed && i <= len(c.actions); i++ {
		changed = false
		for name, cs := range callees {
			if c.waits[name] {
				continue
			}
			for _, to := range cs {
				if c.waits[to] {
					c.waits[name] = true
					changed = true
					break
				}
			}
		}
	}
}

// ---- Элементы верхнего уровня ----

func (c *checker) checkUse(n *UseDecl) {
	if c.env.Scripts == nil || n.Path == "" {
		return
	}
	if !matchPath(n.Path, c.env.Scripts) {
		c.addAt(CodeNoFile, n.Line, n.Col, n.Col+3+1+rl(n.Path)+2, "Не нашёл скрипт «"+n.Path+"» в проекте.",
			hintFrom(n.Path, c.env.Scripts))
	}
}

func (c *checker) checkAction(n *ActionDef) {
	c.inAction = true
	c.h = nil
	c.push()
	for _, p := range n.Params {
		c.declareLocal(p.Name, p.Pos)
	}
	c.block(n.Body)
	c.pop()
	c.inAction = false
}

func (c *checker) checkHandler(h *Handler) {
	c.h = h
	c.everyFrame = h.Event == EventUpdate || (h.Event == EventKey && h.State == KeyHeld)
	if h.Arg != nil {
		t := c.expr(h.Arg)
		switch h.Event {
		case EventEvery:
			c.want(h.Arg, t, tyNum, "Время в «when every»")
		case EventMessage:
			c.want(h.Arg, t, tyText, "Сообщение в «when message»")
		case EventTouch, EventStopTouching:
			if t != tyUnknown && t != tyText && t != tyObj {
				c.addExpr(CodeWrongType, h.Arg, "После «touch» ожидается метка в кавычках или объект, а тут "+typeNom[t]+".", "")
			}
		}
	}
	c.block(h.Body)
	c.h = nil
	c.everyFrame = false
}

// ---- Команды ----

func (c *checker) block(body []Stmt) {
	c.push()
	for _, s := range body {
		c.stmt(s)
	}
	c.pop()
}

func (c *checker) cond(e Expr) {
	if e == nil {
		return
	}
	t := c.expr(e)
	c.wantCond(e, t)
}

func (c *checker) wantCond(e Expr, t vtype) {
	if t == tyUnknown || t == tyBool {
		return
	}
	hint := ""
	if id, ok := e.(*Ident); ok {
		switch t {
		case tyNum:
			hint = "Сравните число с чем-нибудь: например «" + id.Name + " > 0» или «" + id.Name + " is 0»."
		case tyText:
			hint = "Сравните текст с чем-нибудь: например «" + id.Name + " is \"...\"»."
		case tyList:
			hint = "Чтобы проверить, что список не пуст, напишите «" + id.Name + ".length > 0»."
		}
	}
	c.addExpr(CodeCondition, e, "Условие должно быть «да» или «нет», а тут "+typeNom[t]+".", hint)
}

func (c *checker) stmt(s Stmt) {
	switch n := s.(type) {
	case *VarDecl:
		c.expr(n.Value)
		c.declareLocal(n.Name, n.NamePos)
	case *Assign:
		c.assign(n)
	case *If:
		for _, b := range n.Branches {
			c.cond(b.Cond)
			c.block(b.Body)
		}
		c.block(n.Else)
	case *Repeat:
		if n.Count != nil {
			c.want(n.Count, c.expr(n.Count), tyNum, "Число повторений в «repeat … times»")
		}
		c.block(n.Body)
	case *While:
		c.cond(n.Cond)
		c.block(n.Body)
	case *For:
		if n.List != nil {
			c.want(n.List, c.expr(n.List), tyList, "После «in»")
		} else {
			if n.From != nil {
				c.want(n.From, c.expr(n.From), tyNum, "Начало диапазона")
			}
			if n.To != nil {
				c.want(n.To, c.expr(n.To), tyNum, "Конец диапазона")
			}
		}
		c.push()
		c.declareLocal(n.Var, n.VarPos)
		c.block(n.Body)
		c.pop()
	case *Return:
		c.expr(n.Value)
	case *ExprStmt:
		c.expr(n.X)
	case *Move:
		if c.dirTypo(n.Dir) {
			break
		}
		t := c.expr(n.Dir)
		c.want(n.Dir, t, tyDir, "Направление в «move»")
		c.want(n.By, c.expr(n.By), tyNum, "Шаг в «move … by»")
		if n.Avoiding != nil {
			c.want(n.Avoiding, c.expr(n.Avoiding), tyText, "Метка в «avoiding»")
		}
	case *MoveTo:
		c.want(n.X, c.expr(n.X), tyNum, "Координата x в «move to»")
		c.want(n.Y, c.expr(n.Y), tyNum, "Координата y в «move to»")
	case *SlideTo:
		c.want(n.X, c.expr(n.X), tyNum, "Координата x в «slide to»")
		c.want(n.Y, c.expr(n.Y), tyNum, "Координата y в «slide to»")
		c.want(n.Duration, c.expr(n.Duration), tyNum, "Время в «slide to … in»")
		if c.everyFrame {
			c.addAt(CodeWaitEveryFrame, n.Line, n.Col, n.Col+5, "«slide» нельзя в обработчике, который срабатывает каждый кадр.",
				"Уберите «slide» или переместите объект командой «move to» либо «move … per second».")
		}
	case *Turn:
		c.want(n.By, c.expr(n.By), tyNum, "Угол в «turn by»")
	case *Destroy:
		if n.Target != nil {
			c.want(n.Target, c.expr(n.Target), tyObj, "После «destroy»")
		}
	case *Play:
		t := c.expr(n.What)
		c.want(n.What, t, tyText, "Имя файла в «play»")
		if txt, ok := n.What.(*Text); ok && c.env.Assets != nil {
			if lit, ok := txt.Literal(); ok && !matchAsset(lit, c.env.Assets) {
				c.addExpr(CodeNoFile, txt, "Не нашёл файл «"+lit+"» в папке assets.", hintFrom(lit, c.env.Assets))
			}
		}
	case *Print:
		c.expr(n.Value)
	case *Send:
		c.want(n.Message, c.expr(n.Message), tyText, "Сообщение в «send»")
		if n.With != nil {
			c.expr(n.With)
		}
		if n.To != nil {
			c.want(n.To, c.expr(n.To), tyObj, "После «send … to»")
		}
	case *Wait:
		c.want(n.Duration, c.expr(n.Duration), tyNum, "Время в «wait»")
		if c.everyFrame {
			c.addAt(CodeWaitEveryFrame, n.Line, n.Col, n.Col+4, "«wait» нельзя в обработчике, который срабатывает каждый кадр.",
				"Используйте «when every N seconds» или «when key … pressed», либо счётчик времени в переменной.")
		}
	case *GoToScene:
		t := c.expr(n.Scene)
		c.want(n.Scene, t, tyText, "Имя сцены в «go to scene»")
		if txt, ok := n.Scene.(*Text); ok && c.env.Scenes != nil {
			if lit, ok := txt.Literal(); ok && !contains(c.env.Scenes, lit) {
				c.addExpr(CodeNoScene, txt, "Нет сцены «"+lit+"».", hintFrom(lit, c.env.Scenes))
			}
		}
	case *Remember:
		c.want(n.Key, c.expr(n.Key), tyText, "Ключ в «remember»")
		c.expr(n.Value)
	case *ListCmd:
		if n.Item != nil {
			c.expr(n.Item)
		}
		c.want(n.List, c.expr(n.List), tyList, "В этой команде")
	}
}

// dirTypo сообщает TD004, если в позиции направления стоит имя, похожее на left/right/up/down,
// которое нигде не объявлено (ни переменная, ни параметр, ни цикл, ни объект, ни свойство).
// Грамматика разрешает здесь любое имя, поэтому это только подсказка об опечатке.
// В библиотеке команд (нет обработчиков) переменные приходят от объекта-хозяина — молчим.
func (c *checker) dirTypo(e Expr) bool {
	id, ok := e.(*Ident)
	if !ok || c.libMode || rl(id.Name) < 3 {
		return false
	}
	name := id.Name
	if c.declared(name) || c.allVars[name] || contains(c.env.Objects, name) || objectProps[name] != tyUnknown || name == "mouse" {
		return false
	}
	s := suggest(name, directions, 1)
	if s == "" {
		return false
	}
	c.addName(CodeUnknownWord, id.Pos, name, "Я не знаю направления «"+name+"».", didYouMean(s))
	return true
}

func (c *checker) assign(n *Assign) {
	tt := c.expr(n.Target)
	vt := c.expr(n.Value)
	switch tg := n.Target.(type) {
	case *Ident:
		if tg.Name == "name" {
			c.readOnly(tg, "name")
		}
	case *Member:
		if tg.Name == "name" {
			c.readOnly(tg, "name")
		} else if id, ok := tg.X.(*Ident); ok && id.Name == "mouse" {
			c.addExpr(CodeReadOnly, tg, "«mouse."+tg.Name+"» только для чтения: положение и кнопки мыши менять нельзя.", "")
		} else if (tg.Name == "length") && (c.typeOfQuiet(tg.X) == tyList || c.typeOfQuiet(tg.X) == tyText) {
			c.readOnly(tg, "length")
		}
	case *Self, *Other, *ValueRef:
		c.addExpr(CodeReadOnly, n.Target, "Слову «"+n.Target.String()+"» нельзя ничего присвоить.", "")
		return
	}
	switch n.Op {
	case "=":
		if tt != tyUnknown && vt != tyUnknown && vt != tyNothing && tt != vt {
			c.addExpr(CodeWrongType, n.Value, "Здесь хранится "+typeNom[tt]+", а записывается "+typeNom[vt]+".", "")
		}
	case "+=":
		c.arith("+", tt, vt, n.Target, n.Value, n.Value)
	default:
		c.want(n.Target, tt, tyNum, "Слева от «"+n.Op+"»")
		c.want(n.Value, vt, tyNum, "Справа от «"+n.Op+"»")
	}
}

func (c *checker) readOnly(e Expr, what string) {
	hint := ""
	if what == "name" {
		hint = "Имя объекта задаётся в панели Scene."
	}
	c.addExpr(CodeReadOnly, e, "«"+what+"» только для чтения: его нельзя изменить.", hint)
}

func (c *checker) typeOfQuiet(e Expr) vtype {
	old := c.quiet
	c.quiet = true
	t := c.expr(e)
	c.quiet = old
	return t
}

// want сообщает TD205, если тип got точно известен и не равен want.
func (c *checker) want(e Expr, got, want vtype, ctx string) {
	if e == nil || got == tyUnknown || got == want {
		return
	}
	if want == tyObj && got == tyNothing {
		return
	}
	c.addExpr(CodeWrongType, e, ctx+": ожидается "+typeAcc[want]+", а тут "+typeNom[got]+".", wrongTypeHint(want, got))
}

func wrongTypeHint(want, got vtype) string {
	switch {
	case want == tyNum && got == tyText:
		return "Числа пишут без кавычек. Текст в число превращает «number(...)»."
	case want == tyText && got == tyNum:
		return "Чтобы получить текст из числа, вставьте его в кавычки: «\"{...}\"»."
	}
	return ""
}

// ---- Вспомогательное ----

func rl(s string) int { return len([]rune(s)) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hintFrom(word string, list []string) string {
	base := word
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	var bases []string
	byBase := map[string]string{}
	for _, a := range list {
		b := a
		if i := strings.LastIndexByte(b, '/'); i >= 0 {
			b = b[i+1:]
		}
		bases = append(bases, b)
		byBase[b] = a
	}
	if s := suggest(base, bases, 2); s != "" {
		return didYouMean(byBase[s])
	}
	return ""
}

func trimExt(s string) string {
	if i := strings.LastIndexByte(s, '.'); i > strings.LastIndexByte(s, '/') && i > 0 {
		return s[:i]
	}
	return s
}

func cleanPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

// matchPath: путь совпадает с одним из списка (регистр важен).
func matchPath(p string, list []string) bool {
	p = cleanPath(p)
	for _, a := range list {
		if cleanPath(a) == p {
			return true
		}
	}
	return false
}

// matchAsset: имя файла может быть без расширения («theme»), как в «play music "theme"».
func matchAsset(p string, list []string) bool {
	p = cleanPath(p)
	for _, a := range list {
		a = cleanPath(a)
		if a == p || trimExt(a) == p || strings.HasSuffix(a, "/"+p) || strings.HasSuffix(trimExt(a), "/"+p) {
			return true
		}
	}
	return false
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
