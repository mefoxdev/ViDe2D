package tdsl

import (
	"fmt"
	"strconv"
)

// expr проверяет выражение и возвращает его статический тип (tyUnknown, если не известен).
func (c *checker) expr(e Expr) vtype {
	if e == nil {
		return tyUnknown
	}
	switch n := e.(type) {
	case *Number:
		return tyNum
	case *Text:
		for _, p := range n.Parts {
			if p.Expr != nil {
				c.expr(p.Expr)
			}
		}
		return tyText
	case *Bool:
		return tyBool
	case *Nothing:
		return tyNothing
	case *Dir:
		return tyDir
	case *Self:
		return tyObj
	case *Other:
		if !c.inAction && (c.h == nil || (c.h.Event != EventTouch && c.h.Event != EventStopTouching)) {
			c.addExpr(CodeUnknownName, n, "Слово «other» есть только в обработчиках «when touch» и «when stop touching».",
				"В других местах используйте «self» или имя объекта.")
		}
		return tyObj
	case *ValueRef:
		if !c.inAction && (c.h == nil || c.h.Event != EventMessage) {
			c.addExpr(CodeUnknownName, n, "Слово «value» есть только в обработчике «when message».",
				"Значение приходит с сообщением: «send \"hit\" with 5».")
		}
		return tyUnknown
	case *Ident:
		return c.ident(n)
	case *Unary:
		t := c.expr(n.X)
		if n.Op == "not" {
			c.wantCond(n.X, t)
			return tyBool
		}
		c.want(n.X, t, tyNum, "После минуса")
		return tyNum
	case *Binary:
		return c.binary(n)
	case *PerSecond:
		c.want(n.X, c.expr(n.X), tyNum, "Перед «per second»")
		return tyNum
	case *Member:
		return c.member(n)
	case *Index:
		c.expr(n.X)
		c.want(n.I, c.expr(n.I), tyNum, "Номер элемента")
		return tyUnknown
	case *List:
		for _, x := range n.Elems {
			c.expr(x)
		}
		return tyList
	case *Call:
		return c.call(n)
	case *KeyDown:
		return tyBool
	case *Spawn:
		t := c.expr(n.Template)
		if t != tyUnknown && t != tyText && t != tyObj {
			c.want(n.Template, t, tyText, "Шаблон в «spawn»")
		}
		c.want(n.X, c.expr(n.X), tyNum, "Координата x в «spawn»")
		c.want(n.Y, c.expr(n.Y), tyNum, "Координата y в «spawn»")
		if txt, ok := n.Template.(*Text); ok && c.env.Templates != nil {
			if lit, ok := txt.Literal(); ok && !matchAsset(lit, c.env.Templates) {
				c.addExpr(CodeNoFile, txt, "Не нашёл шаблон «"+lit+"» в проекте.", hintFrom(lit, c.env.Templates))
			}
		}
		return tyObj
	}
	return tyUnknown
}

func (c *checker) ident(n *Ident) vtype {
	name := n.Name
	if c.declared(name) || c.allVars[name] && c.libMode {
		return c.vt[name]
	}
	if t, ok := objectProps[name]; ok {
		return t
	}
	if name == "mouse" {
		return tyUnknown
	}
	if contains(c.env.Objects, name) {
		if c.allVars[name] {
			return c.vt[name]
		}
		return tyObj
	}
	if c.allVars[name] {
		// переменная объявлена где-то, но здесь не видна (например, позже в блоке)
		if c.canReportUnknown() {
			c.addName(CodeUnknownName, n.Pos, name, "Переменная «"+name+"» здесь ещё не объявлена.",
				"Объявите её выше через «var "+name+" = ...», до первого использования.")
		}
		return tyUnknown
	}
	if c.canReportUnknown() {
		hint := didYouMean(suggest(name, c.candidates(true), 2))
		msg := "Я не знаю переменной «" + name + "»."
		if hint != "" {
			msg += " Имена различают большие и маленькие буквы."
		} else {
			hint = "Объявите её: «var " + name + " = ...»."
		}
		c.addName(CodeUnknownName, n.Pos, name, msg, hint)
	}
	return tyUnknown
}

func (c *checker) member(n *Member) vtype {
	if id, ok := n.X.(*Ident); ok && id.Name == "mouse" && !c.declared("mouse") {
		switch n.Name {
		case "x", "y":
			return tyNum
		case "held":
			return tyBool
		}
		return tyUnknown
	}
	xt := c.expr(n.X)
	switch {
	case xt == tyObj:
		if t, ok := objectProps[n.Name]; ok {
			return t
		}
	case (xt == tyText || xt == tyList) && n.Name == "length":
		return tyNum
	}
	return tyUnknown
}

func (c *checker) binary(n *Binary) vtype {
	l := c.expr(n.L)
	r := c.expr(n.R)
	switch n.Op {
	case "and", "or":
		c.wantCond(n.L, l)
		c.wantCond(n.R, r)
		return tyBool
	case "+":
		return c.arith("+", l, r, n.L, n.R, n)
	case "-", "*", "/", "%":
		c.want(n.L, l, tyNum, "Для «"+n.Op+"»")
		c.want(n.R, r, tyNum, "Для «"+n.Op+"»")
		return tyNum
	case "is", "is not":
		c.sameTypes(n, l, r)
		return tyBool
	case "<", ">", "<=", ">=":
		if !c.sameTypes(n, l, r) {
			return tyBool
		}
		if l != tyUnknown && l != tyNum && l != tyText {
			c.addExpr(CodeWrongType, n, "Сравнивать по величине («"+n.Op+"») можно числа и текст, а тут "+typeNom[l]+".", "")
		}
		return tyBool
	case "in", "not in":
		if r != tyUnknown && r != tyList && r != tyText {
			c.addExpr(CodeWrongType, n.R, "После «in» нужен список или текст, а тут "+typeNom[r]+".", "")
		} else if r == tyText && l != tyUnknown && l != tyText {
			c.addExpr(CodeWrongType, n.L, "В тексте можно искать только текст, а тут "+typeNom[l]+".",
				"Например: \"ab\" in word.")
		}
		return tyBool
	}
	return tyUnknown
}

// sameTypes сообщает TD201 для сравнения разных типов; возвращает true, если типы одинаковы или неизвестны.
func (c *checker) sameTypes(n *Binary, l, r vtype) bool {
	if l == tyUnknown || r == tyUnknown {
		return false
	}
	if l == r {
		return true
	}
	if l == tyNothing || r == tyNothing {
		return false
	}
	hint := ""
	if (l == tyNum && r == tyText) || (l == tyText && r == tyNum) {
		hint = "Число пишут без кавычек: 10, текст — в кавычках: \"10\". Текст в число превращает «number(...)»."
	}
	c.addExpr(CodeCompare, n, "Нельзя сравнить "+typeAcc[l]+" с "+typeIns[r]+".", hint)
	return false
}

// arith проверяет «+» и возвращает тип результата. at — выражение, под которым подчёркивается ошибка.
func (c *checker) arith(op string, l, r vtype, le, re, at Expr) vtype {
	known := func(t vtype) bool { return t != tyUnknown && t != tyNothing && t != tyList }
	switch {
	case l == tyNum && r == tyText, l == tyText && r == tyNum:
		c.addExpr(CodeAddTypes, at, "Нельзя сложить "+typeAcc[l]+" с "+typeIns[r]+".",
			"Чтобы вставить число в текст, используйте { }: \"Счёт: {score}\".")
		return tyUnknown
	case l == tyNum && r == tyNum:
		return tyNum
	case l == tyText && r == tyText:
		return tyText
	}
	for _, p := range []struct {
		t vtype
		e Expr
	}{{l, le}, {r, re}} {
		if known(p.t) && p.t != tyNum && p.t != tyText {
			c.addExpr(CodeWrongType, p.e, "Складывать можно только числа или текст, а тут "+typeNom[p.t]+".", "")
			return tyUnknown
		}
	}
	if l == tyNum || r == tyNum {
		return tyNum
	}
	if l == tyText || r == tyText {
		return tyText
	}
	return tyUnknown
}

func (c *checker) call(n *Call) vtype {
	ts := make([]vtype, len(n.Args))
	for i, a := range n.Args {
		ts[i] = c.expr(a)
	}
	end := n.Col + rl(n.Name)
	if act, ok := c.actions[n.Name]; ok {
		if len(n.Args) != len(act.Params) {
			c.addAt(CodeArgCount, n.Line, n.Col, end, fmt.Sprintf("Команда «%s» принимает %d %s, а передано %d.",
				n.Name, len(act.Params), plural(len(act.Params), "значение", "значения", "значений"), len(n.Args)), "")
		}
		if c.everyFrame && c.waits[n.Name] {
			c.addAt(CodeWaitEveryFrame, n.Line, n.Col, end, "Команда «"+n.Name+"» содержит «wait» или «slide», а этот обработчик срабатывает каждый кадр.",
				"Вызывайте её из «when key … pressed», «when every …» или «when start».")
		}
		return tyUnknown
	}
	sig, ok := builtins[n.Name]
	if !ok {
		if !c.hasUse && !c.quiet {
			hint := didYouMean(suggest(n.Name, append(actionNames(c.actions), builtinNames...), 2))
			if hint == "" {
				hint = "Свою команду объявляют так: «to " + n.Name + "(...)»."
			}
			c.addAt(CodeUnknownName, n.Line, n.Col, end, "Я не знаю команды или функции «"+n.Name+"».", hint)
		}
		return tyUnknown
	}
	if len(n.Args) < sig.min || len(n.Args) > sig.max {
		want := strconv.Itoa(sig.min)
		cnt := sig.min
		if sig.max != sig.min {
			want = fmt.Sprintf("от %d до %d", sig.min, sig.max)
			cnt = sig.max
		}
		c.addAt(CodeArgCount, n.Line, n.Col, end, fmt.Sprintf("Функция «%s» принимает %s %s, а передано %d.",
			n.Name, want, plural(cnt, "значение", "значения", "значений"), len(n.Args)), "")
		return sig.ret
	}
	for i, a := range n.Args {
		if i < len(sig.args) && sig.args[i] != tyUnknown {
			c.want(a, ts[i], sig.args[i], "Для «"+n.Name+"»")
		}
	}
	return sig.ret
}

func actionNames(m map[string]*ActionDef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---- Обходы дерева ----

// eachStmt вызывает f для каждой команды в body, включая вложенные.
func eachStmt(body []Stmt, f func(Stmt)) {
	for _, s := range body {
		if s == nil {
			continue
		}
		f(s)
		switch n := s.(type) {
		case *If:
			for _, b := range n.Branches {
				eachStmt(b.Body, f)
			}
			eachStmt(n.Else, f)
		case *Repeat:
			eachStmt(n.Body, f)
		case *While:
			eachStmt(n.Body, f)
		case *For:
			eachStmt(n.Body, f)
		}
	}
}

// stmtExprs возвращает выражения самой команды (без вложенных блоков).
func stmtExprs(s Stmt) []Expr {
	var out []Expr
	add := func(es ...Expr) {
		for _, e := range es {
			if e != nil {
				out = append(out, e)
			}
		}
	}
	switch n := s.(type) {
	case *VarDecl:
		add(n.Value)
	case *Assign:
		add(n.Target, n.Value)
	case *If:
		for _, b := range n.Branches {
			add(b.Cond)
		}
	case *Repeat:
		add(n.Count)
	case *While:
		add(n.Cond)
	case *For:
		add(n.List, n.From, n.To)
	case *Return:
		add(n.Value)
	case *ExprStmt:
		add(n.X)
	case *Move:
		add(n.Dir, n.By, n.Avoiding)
	case *MoveTo:
		add(n.X, n.Y)
	case *SlideTo:
		add(n.X, n.Y, n.Duration)
	case *Turn:
		add(n.By)
	case *Destroy:
		add(n.Target)
	case *Play:
		add(n.What)
	case *Print:
		add(n.Value)
	case *Send:
		add(n.Message, n.With, n.To)
	case *Wait:
		add(n.Duration)
	case *GoToScene:
		add(n.Scene)
	case *Remember:
		add(n.Key, n.Value)
	case *ListCmd:
		add(n.Item, n.List)
	}
	return out
}

// eachExpr вызывает f для e и всех его подвыражений.
func eachExpr(e Expr, f func(Expr)) {
	if e == nil {
		return
	}
	f(e)
	switch n := e.(type) {
	case *Text:
		for _, p := range n.Parts {
			eachExpr(p.Expr, f)
		}
	case *Unary:
		eachExpr(n.X, f)
	case *Binary:
		eachExpr(n.L, f)
		eachExpr(n.R, f)
	case *PerSecond:
		eachExpr(n.X, f)
	case *Member:
		eachExpr(n.X, f)
	case *Index:
		eachExpr(n.X, f)
		eachExpr(n.I, f)
	case *List:
		for _, x := range n.Elems {
			eachExpr(x, f)
		}
	case *Call:
		for _, x := range n.Args {
			eachExpr(x, f)
		}
	case *Spawn:
		eachExpr(n.Template, f)
		eachExpr(n.X, f)
		eachExpr(n.Y, f)
	}
}

// endOf оценивает позицию сразу за последним символом выражения (оценка по AST;
// настоящих позиций концов в дереве нет).
func endOf(e Expr) (line, col int) {
	if e == nil {
		return 0, 0
	}
	p := e.Position()
	switch n := e.(type) {
	case *Number:
		if n.Len > 0 {
			return p.Line, p.Col + n.Len
		}
		return p.Line, p.Col + rl(strconv.FormatFloat(n.Value, 'g', -1, 64))
	case *Text:
		if n.Len > 0 {
			return p.Line, p.Col + n.Len
		}
		w := 2
		for _, part := range n.Parts {
			if part.Expr != nil {
				w += rl(part.Expr.String()) + 2
			} else {
				w += rl(part.Lit)
			}
		}
		return p.Line, p.Col + w
	case *Bool:
		if n.Value {
			return p.Line, p.Col + 3
		}
		return p.Line, p.Col + 2
	case *Nothing:
		return p.Line, p.Col + 7
	case *Dir:
		return p.Line, p.Col + rl(n.Name)
	case *Self:
		return p.Line, p.Col + 4
	case *Other, *ValueRef:
		return p.Line, p.Col + 5
	case *Ident:
		return p.Line, p.Col + rl(n.Name)
	case *Unary:
		return endOf(n.X)
	case *Binary:
		return endOf(n.R)
	case *PerSecond:
		l, c := endOf(n.X)
		return l, c + 11
	case *Member:
		l, c := endOf(n.X)
		return l, c + 1 + rl(n.Name)
	case *Index:
		l, c := endOf(n.I)
		return l, c + 1
	case *List:
		if len(n.Elems) == 0 {
			return p.Line, p.Col + 2
		}
		l, c := endOf(n.Elems[len(n.Elems)-1])
		return l, c + 1
	case *Call:
		if len(n.Args) == 0 {
			return p.Line, p.Col + rl(n.Name) + 2
		}
		l, c := endOf(n.Args[len(n.Args)-1])
		return l, c + 1
	case *KeyDown:
		return p.Line, p.Col + 12 + rl(n.Key)
	case *Spawn:
		return endOf(n.Y)
	}
	return p.Line, p.Col + 1
}
