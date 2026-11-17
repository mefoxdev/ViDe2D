package tdsl

import (
	"sort"
	"strings"
)

// syntaxPanic — внутренний сигнал «ошибка записана, синхронизируемся на границе строки».
type syntaxPanic struct{}

type parser struct {
	s     *sink
	toks  []token
	pos   int
	depth int
	noIn  bool // «in» не оператор (нужно в «slide to x, y in 2 seconds»)
	ops   int  // число операций в текущей команде (см. maxOps)
}

// Parse разбирает исходный текст .tdsl. Возвращает дерево (возможно, неполное при ошибках)
// и список ошибок, отсортированный по позиции: не больше 20, по одной на строку.
func Parse(src string) (*Script, []*Error) {
	s := &sink{}
	script := &Script{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(haltPanic); !ok {
					panic(r)
				}
			}
		}()
		if len(src) > maxSourceSize {
			s.add(&Error{Code: CodeExpected, Line: 1, Col: 1, EndCol: 2,
				Message: "Файл слишком большой: больше 2 МБ.",
				Hint:    "Разделите скрипт на несколько файлов и подключите их через «use»."})
			return
		}
		p := &parser{s: s, toks: lexSource(s, src)}
		p.parseScript(script)
	}()
	return script, finishErrors(s)
}

// finishErrors сортирует ошибки и оставляет по одной на строку: лексическую
// (неожиданный символ, незакрытая кавычка, отступ), если она есть, иначе самую левую.
// Лексическая первичнее: после неё лексер пропускает символ, и парсер на той же строке
// сообщает уже вторичную ошибку («ожидалось значение»).
func finishErrors(s *sink) []*Error {
	errs := s.errs
	if len(errs) == 0 {
		return nil
	}
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Line != errs[j].Line {
			return errs[i].Line < errs[j].Line
		}
		return errs[i].Col < errs[j].Col
	})
	out := errs[:0]
	for _, e := range errs {
		if n := len(out); n > 0 && out[n-1].Line == e.Line {
			if s.lex[e] && !s.lex[out[n-1]] {
				out[n-1] = e
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

// ---- Работа с токенами ----

func (p *parser) tok() token { return p.toks[p.pos] }

func (p *parser) peek(n int) token {
	if p.pos+n >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos+n]
}

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tEOF {
		p.pos++
	}
	return t
}

func (t token) is(kind tokKind, text string) bool { return t.kind == kind && t.text == text }

func (p *parser) isKw(w string) bool { return p.tok().is(tKeyword, w) }
func (p *parser) isOp(o string) bool { return p.tok().is(tOp, o) }
func (p *parser) at(k tokKind) bool  { return p.tok().kind == k }

func (t token) pos() Pos { return Pos{t.line, t.col} }

// isWord — имя или ключевое слово (годится как название клавиши или поля после точки).
func (t token) isWord() bool { return t.kind == tName || t.kind == tKeyword }

// describe описывает токен для сообщения об ошибке.
func (t token) describe() string {
	switch t.kind {
	case tEOF:
		return "конец файла"
	case tNL:
		return "конец строки"
	case tIndent, tDedent:
		return "отступ"
	}
	return "«" + t.text + "»"
}

// ---- Ошибки ----

func (p *parser) report(t token, code, msg, hint string) {
	end := t.endCol
	if end <= t.col {
		end = t.col + 1
	}
	p.s.add(&Error{Code: code, Line: t.line, Col: t.col, EndCol: end, Message: msg, Hint: hint})
}

func (p *parser) fail(t token, code, msg, hint string) {
	p.report(t, code, msg, hint)
	panic(syntaxPanic{})
}

// try выполняет f и возвращает false, если f прервалась из-за синтаксической ошибки.
func (p *parser) try(f func()) (ok bool) {
	saved, savedIn := p.depth, p.noIn
	defer func() {
		if r := recover(); r != nil {
			if _, is := r.(syntaxPanic); !is {
				panic(r)
			}
			p.depth, p.noIn = saved, savedIn
			ok = false
		}
	}()
	f()
	return true
}

func (p *parser) enter() {
	p.depth++
	if p.depth > maxDepth {
		p.report(p.tok(), CodeExpected, "Слишком глубокая вложенность: больше 64 уровней.",
			"Вынесите часть кода в свою команду («to»).")
		panic(haltPanic{})
	}
}

func (p *parser) leave() { p.depth-- }

// skipLine пропускает остаток строки вместе с её NL.
func (p *parser) skipLine() {
	for !p.at(tNL) && !p.at(tEOF) {
		p.next()
	}
	if p.at(tNL) {
		p.next()
	}
}

// recoverLine синхронизируется после ошибки: пропускает строку и, если за ней
// следует блок, разбирает его (чтобы найти ошибки и в нём), выбрасывая результат.
func (p *parser) recoverLine() {
	p.skipLine()
	if p.at(tIndent) {
		p.next()
		p.blockBody()
	}
}

// ---- Ожидаемые лексемы ----

// expectWord требует одно из ключевых слов. Если вместо него опечатанное имя,
// это TD004 с подсказкой, иначе TD005. example — необязательный пример правильной записи.
func (p *parser) expectWord(example string, words ...string) string {
	t := p.tok()
	if t.kind == tKeyword {
		for _, w := range words {
			if t.text == w {
				p.next()
				return w
			}
		}
	}
	if t.kind == tName {
		p.fail(t, CodeUnknownWord, "Не знаю слова «"+t.text+"».", p.wordHint(t.text, words))
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "«" + w + "»"
	}
	msg := "Ожидалось слово " + quoted[0]
	if len(words) > 1 {
		msg = "Ожидалось одно из слов: " + strings.Join(quoted, ", ")
	}
	if example != "" {
		msg += ": например `" + example + "`"
	}
	p.fail(t, CodeExpected, msg+".", "")
	return ""
}

func (p *parser) wordHint(word string, candidates []string) string {
	if s := suggest(word, candidates, 2); s != "" {
		return didYouMean(s)
	}
	return "Здесь ожидалось: " + strings.Join(quoteAll(candidates), ", ") + "."
}

func quoteAll(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = "«" + w + "»"
	}
	return out
}

func (p *parser) expectOp(op, msg string) token {
	t := p.tok()
	if !t.is(tOp, op) {
		p.fail(t, CodeExpected, msg, "")
	}
	return p.next()
}

// expectName требует имя (не зарезервированное слово).
func (p *parser) expectName(what string) token {
	t := p.tok()
	switch t.kind {
	case tName:
		return p.next()
	case tKeyword:
		p.fail(t, CodeExpected, "«"+t.text+"» — зарезервированное слово, его нельзя использовать как "+what+".",
			"Придумайте другое имя.")
	}
	p.fail(t, CodeExpected, "Ожидалось "+what+", а найдено "+t.describe()+".", "")
	return t
}

// expectUnit требует «second» или «seconds».
func (p *parser) expectUnit() {
	p.expectWord("", "second", "seconds")
}

// endStmt требует конец строки.
func (p *parser) endStmt() {
	t := p.tok()
	switch {
	case t.kind == tNL:
		p.next()
	case t.kind == tEOF:
	case t.is(tOp, ")") || t.is(tOp, "]"):
		p.fail(t, CodeBracket, "Лишняя закрывающая скобка «"+t.text+"».", "Уберите её или добавьте открывающую.")
	case t.is(tOp, "="):
		p.fail(t, CodeExpected, "Знак «=» бывает только в присваивании.",
			"Чтобы сравнить значения, используйте слово «is»: if lives is 0")
	default:
		p.fail(t, CodeExpected, "Здесь должна закончиться строка, а найдено "+t.describe()+".", "")
	}
}

// closeBracket закрывает скобку open. Если commaOK, перед закрытием допустима запятая.
func (p *parser) closeBracket(open token, closer string, commaOK bool) {
	if p.isOp(closer) {
		p.next()
		return
	}
	t := p.tok()
	switch {
	case t.kind == tNL || t.kind == tEOF:
		p.fail(open, CodeBracket, "Не закрыта скобка «"+open.text+"».", "Добавьте «"+closer+"».")
	case t.is(tOp, ")") || t.is(tOp, "]"):
		p.fail(t, CodeBracket, "Скобка «"+open.text+"» закрыта не той скобкой: нужна «"+closer+"».", "")
	case commaOK:
		p.fail(t, CodeExpected, "Ожидалась запятая или «"+closer+"», а найдено "+t.describe()+".", "")
	}
	p.fail(t, CodeExpected, "Ожидалась «"+closer+"», а найдено "+t.describe()+".", "")
}

// ---- Верхний уровень ----

var topWords = []string{"var", "shared", "use", "to", "when"}

func (p *parser) parseScript(script *Script) {
	for !p.at(tEOF) {
		start := p.pos
		switch {
		case p.at(tNL) || p.at(tDedent):
			p.next()
		case p.at(tIndent):
			p.unexpectedIndent()
		default:
			var item Item
			if !p.try(func() { item = p.parseItem() }) {
				p.recoverLine()
			} else if item != nil {
				script.Items = append(script.Items, item)
			}
		}
		if p.pos == start {
			p.next()
		}
	}
}

func (p *parser) unexpectedIndent() {
	p.report(p.tok(), CodeIndent, "Неверный отступ: эта строка сдвинута лишний раз.",
		"Сдвигать нужно только строки внутри when, to, if, repeat, while и for.")
	p.next()
	p.blockBody()
}

func (p *parser) parseItem() Item {
	p.ops = 0
	t := p.tok()
	if t.kind == tKeyword {
		switch t.text {
		case "var":
			return p.parseVarDecl()
		case "shared":
			p.next()
			name, value := p.parseDeclTail()
			p.endStmt()
			return &SharedDecl{Pos: t.pos(), Name: name.text, NamePos: name.pos(), Value: value}
		case "use":
			p.next()
			path := p.tok()
			if path.kind != tText {
				p.fail(path, CodeExpected, "После «use» нужен путь в кавычках: use \"helpers.tdsl\".", "")
			}
			p.next()
			lit, ok := p.textExpr(path).Literal()
			if !ok {
				p.fail(path, CodeExpected, "Путь в «use» не может содержать вставки «{...}».", "")
			}
			p.endStmt()
			return &UseDecl{Pos: t.pos(), Path: lit}
		case "to":
			return p.parseActionDef()
		case "when":
			return p.parseHandler()
		}
		p.fail(t, CodeExpected, "Команду «"+t.text+"» нельзя писать вне события.",
			"Положите её внутрь обработчика, например «when start».")
	}
	if t.kind == tName {
		if s := suggest(t.text, topWords, 2); s != "" {
			p.fail(t, CodeUnknownWord, "Не знаю слова «"+t.text+"».", didYouMean(s))
		}
		p.fail(t, CodeExpected, "Здесь можно начинать только с var, shared, use, to или when.",
			"Присваивание и вызовы пишутся внутри обработчика («when ...») или команды («to ...»).")
	}
	p.fail(t, CodeExpected, "Здесь можно начинать только с var, shared, use, to или when, а найдено "+t.describe()+".", "")
	return nil
}

// parseDeclTail разбирает «имя = выражение» после var / shared.
func (p *parser) parseDeclTail() (token, Expr) {
	name := p.expectName("имя переменной")
	p.expectOp("=", "После имени переменной нужен знак «=»: var "+name.text+" = 0")
	return name, p.parseExpr()
}

func (p *parser) parseVarDecl() *VarDecl {
	t := p.next()
	name, value := p.parseDeclTail()
	p.endStmt()
	return &VarDecl{Pos: t.pos(), Name: name.text, NamePos: name.pos(), Value: value}
}

func (p *parser) parseActionDef() *ActionDef {
	t := p.next()
	name := p.expectName("имя команды")
	open := p.expectOp("(", "После имени команды нужны скобки: to "+name.text+"()")
	def := &ActionDef{Pos: t.pos(), Name: name.text, NamePos: name.pos()}
	if !p.isOp(")") {
		for {
			prm := p.expectName("имя параметра")
			def.Params = append(def.Params, Param{Pos: prm.pos(), Name: prm.text})
			if !p.isOp(",") {
				break
			}
			p.noTrailingComma(p.next(), ")")
		}
	}
	p.closeBracket(open, ")", true)
	p.endStmt()
	def.Body = p.parseBlock()
	return def
}

var eventWords = []string{"start", "update", "key", "click", "touch", "stop", "message", "every"}

func (p *parser) parseHandler() *Handler {
	t := p.next()
	h := &Handler{Pos: t.pos()}
	ev := p.tok()
	if ev.kind != tKeyword {
		if ev.kind == tName {
			p.fail(ev, CodeUnknownWord, "Не знаю такого события «"+ev.text+"».", p.wordHint(ev.text, eventWords))
		}
		p.fail(ev, CodeExpected, "После «when» нужно событие, а найдено "+ev.describe()+".",
			"Например: when start, when update, when key space pressed.")
	}
	switch ev.text {
	case "start":
		p.next()
		h.Event = EventStart
	case "update":
		p.next()
		h.Event = EventUpdate
	case "click":
		p.next()
		h.Event = EventClick
	case "key":
		p.next()
		h.Event = EventKey
		h.Key = p.parseKeyName()
		h.State = KeyState(p.expectWord("when key space pressed", "pressed", "released", "held"))
	case "touch":
		p.next()
		h.Event = EventTouch
		h.Arg = p.parseExpr()
	case "stop":
		p.next()
		if n := p.tok(); !n.is(tName, "touching") {
			p.fail(n, CodeExpected, "После «when stop» ожидалось слово «touching»: when stop touching \"coin\".", "")
		}
		p.next()
		h.Event = EventStopTouching
		h.Arg = p.parseExpr()
	case "message":
		p.next()
		h.Event = EventMessage
		h.Arg = p.parseExpr()
	case "every":
		p.next()
		h.Event = EventEvery
		h.Arg = p.parseExpr()
		p.expectUnit()
	default:
		p.fail(ev, CodeExpected, "Не знаю такого события «"+ev.text+"».", p.wordHint(ev.text, eventWords))
	}
	p.endStmt()
	h.Body = p.parseBlock()
	return h
}

// parseKeyName разбирает название клавиши: слово или текст в кавычках.
func (p *parser) parseKeyName() string {
	t := p.tok()
	switch {
	case t.kind == tText:
		p.next()
		lit, ok := p.textExpr(t).Literal()
		if !ok {
			p.fail(t, CodeExpected, "Название клавиши не может содержать вставки «{...}».", "")
		}
		return lit
	case t.isWord():
		p.next()
		return t.text
	}
	p.fail(t, CodeExpected, "Ожидалось название клавиши (space, left, a, \"1\"), а найдено "+t.describe()+".", "")
	return ""
}

// ---- Блоки ----

// parseBlock разбирает INDENT { statement } DEDENT. Если блока нет, сообщает об
// ошибке (без остановки разбора) и возвращает nil.
func (p *parser) parseBlock() []Stmt {
	if !p.at(tIndent) {
		prev := p.toks[p.pos-1] // NL заголовка
		p.report(prev, CodeExpected, "Ожидался блок: следующие строки нужно сдвинуть на 2 пробела.", "")
		return nil
	}
	p.next()
	return p.blockBody()
}

// blockBody разбирает операторы до DEDENT (INDENT уже съеден).
func (p *parser) blockBody() []Stmt {
	p.enter()
	var stmts []Stmt
	for !p.at(tDedent) && !p.at(tEOF) {
		start := p.pos
		if p.at(tIndent) {
			p.unexpectedIndent()
		} else {
			var st Stmt
			if p.try(func() { st = p.parseStatement() }) {
				stmts = append(stmts, st)
			} else {
				p.recoverLine()
			}
		}
		if p.pos == start {
			p.next()
		}
	}
	if p.at(tDedent) {
		p.next()
	}
	p.leave()
	return stmts
}
