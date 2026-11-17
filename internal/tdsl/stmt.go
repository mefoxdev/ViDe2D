package tdsl

var (
	directions   = []string{"left", "right", "up", "down"}
	commandWords = []string{
		"var", "if", "repeat", "while", "for", "stop", "next", "return", "move", "slide", "turn",
		"show", "hide", "destroy", "spawn", "play", "print", "send", "wait", "go", "restart",
		"remember", "add", "remove", "clear", "sort", "shuffle",
	}
)

func isAssignOp(t token) bool {
	if t.kind != tOp {
		return false
	}
	switch t.text {
	case "=", "+=", "-=", "*=", "/=":
		return true
	}
	return false
}

// parseStatement разбирает одну команду или конструкцию вместе с концом строки / блоком.
func (p *parser) parseStatement() Stmt {
	p.ops = 0
	t := p.tok()
	switch t.kind {
	case tKeyword:
		switch t.text {
		case "if":
			return p.parseIf()
		case "repeat":
			return p.parseRepeat()
		case "while":
			p.next()
			cond := p.parseExpr()
			p.endStmt()
			return &While{Pos: t.pos(), Cond: cond, Body: p.parseBlock()}
		case "for":
			return p.parseFor()
		case "var":
			return p.parseVarDecl()
		case "self", "other", "value":
			return p.parseAssignOrCall()
		case "else":
			p.fail(t, CodeExpected, "«else» без «if»: оно должно стоять на одном уровне с «if» сразу после его блока.", "")
		}
		st := p.parseCommand()
		p.endStmt()
		return st
	case tName:
		n := p.peek(1)
		if n.is(tOp, "(") || n.is(tOp, ".") || n.is(tOp, "[") || isAssignOp(n) {
			return p.parseAssignOrCall()
		}
		if s := suggest(t.text, commandWords, 2); s != "" {
			p.fail(t, CodeUnknownWord, "Не знаю слова «"+t.text+"».", didYouMean(s))
		}
		if n.kind == tNL || n.kind == tEOF {
			p.fail(t, CodeExpected, "После имени «"+t.text+"» ожидалось «(...)» (вызов команды) или «=» (присваивание).", "")
		}
		p.fail(t, CodeUnknownWord, "Не знаю слова «"+t.text+"».", "Здесь ожидалась команда: move, print, play, send и т. д.")
	}
	p.fail(t, CodeExpected, "Ожидалась команда, а найдено "+t.describe()+".", "")
	return nil
}

// parseAssignOrCall разбирает присваивание в имя / свойство / элемент списка либо вызов команды.
func (p *parser) parseAssignOrCall() Stmt {
	first := p.tok()
	x := p.parsePostfix()
	if isAssignOp(p.tok()) {
		switch x.(type) {
		case *Ident, *Member, *Index:
		default:
			p.fail(first, CodeExpected, "Присвоить можно только переменной, свойству или элементу списка.", "")
		}
		op := p.next().text
		value := p.parseExpr()
		p.endStmt()
		return &Assign{Pos: first.pos(), Target: x, Op: op, Value: value}
	}
	if _, ok := x.(*Call); !ok {
		p.fail(p.tok(), CodeExpected, "Ожидалось присваивание («=», «+=» ...) или вызов команды со скобками.", "")
	}
	p.endStmt()
	return &ExprStmt{Pos: first.pos(), X: x}
}

func (p *parser) parseIf() Stmt {
	t := p.next()
	st := &If{Pos: t.pos()}
	cond := p.parseExpr()
	p.endStmt()
	st.Branches = append(st.Branches, Branch{Pos: t.pos(), Cond: cond, Body: p.parseBlock()})
	for p.isKw("else") {
		e := p.next()
		if p.isKw("if") {
			p.next()
			cond := p.parseExpr()
			p.endStmt()
			st.Branches = append(st.Branches, Branch{Pos: e.pos(), Cond: cond, Body: p.parseBlock()})
			continue
		}
		p.endStmt()
		st.Else = p.parseBlock()
		break
	}
	return st
}

func (p *parser) parseRepeat() Stmt {
	t := p.next()
	st := &Repeat{Pos: t.pos()}
	if p.isKw("forever") {
		p.next()
	} else {
		st.Count = p.parseExpr()
		p.expectWord("repeat 3 times", "times")
	}
	p.endStmt()
	st.Body = p.parseBlock()
	return st
}

func (p *parser) parseFor() Stmt {
	t := p.next()
	p.expectWord("for each item in items", "each")
	v := p.expectName("имя переменной цикла")
	p.expectWord("for each item in items", "in")
	st := &For{Pos: t.pos(), Var: v.text, VarPos: v.pos()}
	first := p.parseExpr()
	if p.isKw("to") {
		p.next()
		st.From, st.To = first, p.parseAdditive()
	} else {
		st.List = first
	}
	p.endStmt()
	st.Body = p.parseBlock()
	return st
}

// parseDirExpr разбирает направление: слово left/right/up/down или имя переменной.
func (p *parser) parseDirExpr() Expr {
	t := p.tok()
	switch t.kind {
	case tKeyword:
		for _, d := range directions {
			if t.text == d {
				p.next()
				return &Dir{Pos: t.pos(), Name: d}
			}
		}
	case tName:
		// Опечатку в направлении («rigth») определяет Check: имя может быть переменной.
		p.next()
		return &Ident{Pos: t.pos(), Name: t.text}
	}
	p.fail(t, CodeExpected, "Ожидалось направление: left, right, up, down или имя переменной, а найдено "+t.describe()+".", "")
	return nil
}

// parseCommand разбирает команду-фразу (без конца строки).
func (p *parser) parseCommand() Stmt {
	t := p.next()
	pos := t.pos()
	switch t.text {
	case "move":
		if p.isKw("to") {
			p.next()
			x, y := p.parseXY(false)
			return &MoveTo{Pos: pos, X: x, Y: y}
		}
		st := &Move{Pos: pos, Dir: p.parseDirExpr()}
		p.expectWord("move right by 5", "by")
		st.By = p.parseExpr()
		if p.isKw("avoiding") {
			p.next()
			st.Avoiding = p.parseExpr()
		}
		return st
	case "slide":
		p.expectWord("slide to 100, 50 in 2 seconds", "to")
		x, y := p.parseXY(true) // после y слово «in» — часть команды, а не оператор
		p.expectWord("slide to 100, 50 in 2 seconds", "in")
		d := p.parseExpr()
		p.expectUnit()
		return &SlideTo{Pos: pos, X: x, Y: y, Duration: d}
	case "turn":
		p.expectWord("turn by 90", "by")
		return &Turn{Pos: pos, By: p.parseExpr()}
	case "show":
		return &Show{Pos: pos}
	case "hide":
		return &Hide{Pos: pos}
	case "destroy":
		st := &Destroy{Pos: pos}
		if !p.at(tNL) && !p.at(tEOF) {
			st.Target = p.parseExpr()
		}
		return st
	case "spawn":
		return &ExprStmt{Pos: pos, X: p.parseSpawn(t)}
	case "play":
		kind := p.expectWord("play sound \"jump.wav\"", "sound", "music")
		return &Play{Pos: pos, Kind: kind, What: p.parseExpr()}
	case "stop":
		if p.expectWord("", "repeating", "music") == "repeating" {
			return &StopRepeating{Pos: pos}
		}
		return &StopMusic{Pos: pos}
	case "next":
		p.expectWord("next round", "round")
		return &NextRound{Pos: pos}
	case "return":
		st := &Return{Pos: pos}
		if !p.at(tNL) && !p.at(tEOF) {
			st.Value = p.parseExpr()
		}
		return st
	case "print":
		return &Print{Pos: pos, Value: p.parseExpr()}
	case "send":
		st := &Send{Pos: pos, Message: p.parseExpr()}
		if p.isKw("with") {
			p.next()
			st.With = p.parseExpr()
		}
		if p.isKw("to") {
			p.next()
			st.To = p.parseExpr()
		}
		return st
	case "wait":
		d := p.parseExpr()
		p.expectUnit()
		return &Wait{Pos: pos, Duration: d}
	case "go":
		p.expectWord("go to scene \"level2\"", "to")
		p.expectWord("go to scene \"level2\"", "scene")
		return &GoToScene{Pos: pos, Scene: p.parseExpr()}
	case "restart":
		p.expectWord("restart scene", "scene")
		return &RestartScene{Pos: pos}
	case "remember":
		key := p.parseExpr()
		p.expectWord("remember \"best\" as score", "as")
		return &Remember{Pos: pos, Key: key, Value: p.parseExpr()}
	case "add":
		item := p.parseExpr()
		p.expectWord("add \"меч\" to items", "to")
		return &ListCmd{Pos: pos, Op: "add", Item: item, List: p.parseExpr()}
	case "remove":
		item := p.parseExpr()
		p.expectWord("remove \"меч\" from items", "from")
		return &ListCmd{Pos: pos, Op: "remove", Item: item, List: p.parseExpr()}
	case "clear", "sort", "shuffle":
		return &ListCmd{Pos: pos, Op: t.text, List: p.parseExpr()}
	}
	p.fail(t, CodeExpected, "Ожидалась команда, а найдено «"+t.text+"».", "")
	return nil
}

// parseXY разбирает «x, y». При noInY в выражении y слово «in» не считается оператором.
func (p *parser) parseXY(noInY bool) (Expr, Expr) {
	x := p.parseExpr()
	p.expectOp(",", "Между координатами нужна запятая: move to 100, 50")
	saved := p.noIn
	p.noIn = noInY
	y := p.parseExpr()
	p.noIn = saved
	return x, y
}

// parseSpawn разбирает «spawn шаблон at x, y»; слово spawn (t) уже прочитано.
func (p *parser) parseSpawn(t token) *Spawn {
	tmpl := p.parseExpr()
	p.expectWord("spawn \"Star\" at 10, 20", "at")
	x, y := p.parseXY(false)
	return &Spawn{Pos: t.pos(), Template: tmpl, X: x, Y: y}
}
