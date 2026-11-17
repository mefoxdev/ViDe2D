package tdsl

// compareOp распознаёт оператор сравнения в текущей позиции и возвращает его запись
// и число токенов; если оператора нет — пустую строку.
func (p *parser) compareOp() (string, int) {
	t := p.tok()
	switch {
	case t.kind == tOp:
		switch t.text {
		case "<", ">", "<=", ">=":
			return t.text, 1
		}
	case t.is(tKeyword, "is"):
		if p.peek(1).is(tKeyword, "not") {
			return "is not", 2
		}
		return "is", 1
	case t.is(tKeyword, "in") && !p.noIn:
		return "in", 1
	case t.is(tKeyword, "not") && p.peek(1).is(tKeyword, "in") && !p.noIn:
		return "not in", 2
	}
	return "", 0
}

func (p *parser) parseExpr() Expr {
	p.enter()
	x := p.parseOr()
	p.leave()
	return x
}

// maxChain — предел длины цепочки одинаковых по приоритету операций («1+1+1+...»);
// maxOps — предел числа операций в одной команде. Дерево таких цепочек — левая
// «лесенка», а обходчики дерева рекурсивны: без предела переполнился бы стек.
const (
	maxChain = 10000
	maxOps   = 50000
)

// chainStep учитывает ещё одну операцию в цепочке; n — счётчик цепочки.
func (p *parser) chainStep(n *int) {
	*n++
	p.ops++
	if *n > maxChain || p.ops > maxOps {
		p.fail(p.tok(), CodeExpected, "Выражение слишком длинное: слишком много операций подряд.",
			"Разбейте его на несколько команд через промежуточные переменные.")
	}
}

func (p *parser) parseOr() Expr {
	x := p.parseAnd()
	n := 0
	for p.isKw("or") {
		p.chainStep(&n)
		p.next()
		x = &Binary{Pos: x.Position(), Op: "or", L: x, R: p.parseAnd()}
	}
	return x
}

func (p *parser) parseAnd() Expr {
	x := p.parseNot()
	n := 0
	for p.isKw("and") {
		p.chainStep(&n)
		p.next()
		x = &Binary{Pos: x.Position(), Op: "and", L: x, R: p.parseNot()}
	}
	return x
}

func (p *parser) parseNot() Expr {
	if p.isKw("not") {
		t := p.next()
		p.enter()
		x := p.parseNot()
		p.leave()
		return &Unary{Pos: t.pos(), Op: "not", X: x}
	}
	return p.parseCompare()
}

func (p *parser) parseCompare() Expr {
	x := p.parseAdditive()
	op, n := p.compareOp()
	if op == "" {
		return x
	}
	for i := 0; i < n; i++ {
		p.next()
	}
	x = &Binary{Pos: x.Position(), Op: op, L: x, R: p.parseAdditive()}
	if next, _ := p.compareOp(); next != "" {
		p.fail(p.tok(), CodeExpected, "Цепочки сравнений запрещены.",
			"Напишите два сравнения через «and»: x > 1 and x < 5")
	}
	return x
}

func (p *parser) parseAdditive() Expr {
	x := p.parseTerm()
	n := 0
	for p.isOp("+") || p.isOp("-") {
		p.chainStep(&n)
		op := p.next().text
		x = &Binary{Pos: x.Position(), Op: op, L: x, R: p.parseTerm()}
	}
	if p.isKw("per") {
		p.next()
		p.expectWord("move right by 60 per second", "second")
		x = &PerSecond{Pos: x.Position(), X: x}
	}
	return x
}

func (p *parser) parseTerm() Expr {
	x := p.parseUnary()
	n := 0
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		p.chainStep(&n)
		op := p.next().text
		x = &Binary{Pos: x.Position(), Op: op, L: x, R: p.parseUnary()}
	}
	return x
}

func (p *parser) parseUnary() Expr {
	if p.isOp("-") {
		t := p.next()
		p.enter()
		x := p.parseUnary()
		p.leave()
		return &Unary{Pos: t.pos(), Op: "-", X: x}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() Expr {
	x := p.parsePrimary()
	n := 0
	for {
		switch {
		case p.isOp("."):
			p.chainStep(&n)
			p.next()
			t := p.tok()
			if !t.isWord() {
				p.fail(t, CodeExpected, "После точки нужно имя, а найдено "+t.describe()+".", "")
			}
			p.next()
			x = &Member{Pos: x.Position(), X: x, Name: t.text}
		case p.isOp("["):
			p.chainStep(&n)
			open := p.next()
			saved := p.noIn
			p.noIn = false
			idx := p.parseExpr()
			p.noIn = saved
			p.closeBracket(open, "]", false)
			x = &Index{Pos: x.Position(), X: x, I: idx}
		default:
			return x
		}
	}
}

func (p *parser) parsePrimary() Expr {
	t := p.tok()
	pos := t.pos()
	switch t.kind {
	case tNumber:
		p.next()
		return &Number{Pos: pos, Value: t.num, Len: t.endCol - t.col}
	case tText:
		p.next()
		return p.textExpr(t)
	case tName:
		p.next()
		if p.isOp("(") {
			return p.parseCall(t)
		}
		return &Ident{Pos: pos, Name: t.text}
	case tOp:
		switch t.text {
		case "(":
			p.next()
			saved := p.noIn
			p.noIn = false
			x := p.parseExpr()
			p.noIn = saved
			p.closeBracket(t, ")", false)
			return x
		case "[":
			return p.parseList()
		case ")", "]":
			p.fail(t, CodeBracket, "Лишняя закрывающая скобка «"+t.text+"».", "Уберите её или добавьте открывающую.")
		}
	case tKeyword:
		switch t.text {
		case "yes", "no":
			p.next()
			return &Bool{Pos: pos, Value: t.text == "yes"}
		case "nothing":
			p.next()
			return &Nothing{Pos: pos}
		case "left", "right", "up", "down":
			p.next()
			return &Dir{Pos: pos, Name: t.text}
		case "self":
			p.next()
			return &Self{Pos: pos}
		case "other":
			p.next()
			return &Other{Pos: pos}
		case "value":
			p.next()
			return &ValueRef{Pos: pos}
		case "key":
			p.next()
			key := p.parseKeyName()
			p.expectWord("key space is held", "is")
			p.expectWord("key space is held", "held")
			return &KeyDown{Pos: pos, Key: key}
		case "spawn":
			p.next()
			return p.parseSpawn(t)
		case "round":
			// «round(x)» — встроенная функция; «next round» разбирает parseCommand
			if p.peek(1).is(tOp, "(") {
				p.next()
				return p.parseCall(t)
			}
		}
	}
	if t.kind == tNL || t.kind == tEOF {
		p.fail(t, CodeExpected, "Здесь ожидалось значение, но строка закончилась.", "")
	}
	p.fail(t, CodeExpected, "Здесь ожидалось значение (число, текст, имя, список), а найдено "+t.describe()+".", "")
	return nil
}

// parseCall разбирает аргументы вызова; имя (name) и «(» ещё не прочитаны/прочитано имя.
func (p *parser) parseCall(name token) Expr {
	open := p.next()
	call := &Call{Pos: name.pos(), Name: name.text}
	call.Args = p.parseExprList(open, ")")
	return call
}

func (p *parser) parseList() Expr {
	open := p.next()
	return &List{Pos: open.pos(), Elems: p.parseExprList(open, "]")}
}

// parseExprList разбирает «[выражение {, выражение}] закрывающая_скобка».
func (p *parser) parseExprList(open token, closer string) []Expr {
	saved := p.noIn
	p.noIn = false
	var out []Expr
	if !p.isOp(closer) {
		for {
			out = append(out, p.parseExpr())
			if !p.isOp(",") {
				break
			}
			p.noTrailingComma(p.next(), closer)
		}
	}
	p.noIn = saved
	p.closeBracket(open, closer, true)
	return out
}

// noTrailingComma: после запятой comma сразу стоит закрывающая скобка — лишняя запятая.
func (p *parser) noTrailingComma(comma token, closer string) {
	if p.isOp(closer) {
		p.fail(comma, CodeExpected, "После запятой нужно значение — уберите запятую.", "")
	}
}

// textExpr превращает токен текста в узел; вставки «{...}» разбираются в выражения.
func (p *parser) textExpr(t token) *Text {
	out := &Text{Pos: t.pos(), Len: t.endCol - t.col}
	for _, part := range t.parts {
		if !part.isExpr {
			out.Parts = append(out.Parts, TextPart{Lit: part.lit})
			continue
		}
		toks := lexInline(p.s, part.src, part.line, part.col-1, part.depth)
		sub := &parser{s: p.s, toks: toks, depth: p.depth, ops: p.ops}
		var e Expr
		if sub.try(func() {
			e = sub.parseExpr()
			if !sub.at(tEOF) {
				sub.fail(sub.tok(), CodeExpected, "В фигурных скобках должно быть одно выражение, а найдено "+sub.tok().describe()+".", "")
			}
		}) {
			out.Parts = append(out.Parts, TextPart{Expr: e})
		}
	}
	return out
}
