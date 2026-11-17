package tdsl

import (
	"sort"
	"strings"
)

// CompletionKind — вид подсказки автодополнения.
type CompletionKind int

// Виды подсказок.
const (
	CompKeyword  CompletionKind = iota // управление: if, repeat, ...
	CompCommand                        // команда: move, show, ...
	CompEvent                          // событие после when
	CompBuiltin                        // встроенная функция
	CompProperty                       // свойство объекта
	CompVariable                       // переменная, параметр, переменная цикла
	CompAction                         // своя команда «to» из файла
	CompValue                          // константа, направление, self/other/value, слово-продолжение
	CompObject                         // объект сцены
	CompTag                            // метка
	CompAsset                          // файл из assets
	CompScript                         // другой скрипт
	CompScene                          // сцена
	CompMessage                        // сообщение, известное в файле
)

// Completion — одна подсказка. Insert заменяет «слово под курсором» (буквы, цифры и «_»
// непосредственно перед курсором); Label — то, что показывается в списке и по чему
// идёт фильтрация.
type Completion struct {
	Label  string
	Insert string
	Kind   CompletionKind
	Detail string
}

// CompleteEnv — сведения о проекте для подсказок. Допустим nil.
type CompleteEnv struct {
	Objects, Tags, Assets, Scripts, Scenes []string
}

const completeLimit = 50

type cmItem struct {
	label, insert, detail string
	kind                  CompletionKind
}

var cmTopLevel = []cmItem{
	{"when", "when ", "обработчик события", CompKeyword},
	{"var", "var ", "переменная объекта", CompKeyword},
	{"shared", "shared ", "общая переменная игры", CompKeyword},
	{"to", "to ", "своя команда", CompKeyword},
	{"use", "use ", "подключить файл с действиями", CompKeyword},
}

var cmControl = []cmItem{
	{"if", "if ", "условие", CompKeyword},
	{"else", "else", "иначе", CompKeyword},
	{"repeat", "repeat ", "повторить несколько раз", CompKeyword},
	{"while", "while ", "цикл «пока»", CompKeyword},
	{"for each", "for each ", "цикл по списку или числам", CompKeyword},
	{"return", "return ", "вернуть результат / выйти", CompKeyword},
	{"stop repeating", "stop repeating", "выйти из цикла", CompKeyword},
	{"next round", "next round", "следующий шаг цикла", CompKeyword},
	{"var", "var ", "объявить переменную", CompKeyword},
}

var cmCommands = []cmItem{
	{"move", "move ", "сдвинуть объект", CompCommand},
	{"slide", "slide to ", "плавно переехать в точку", CompCommand},
	{"turn", "turn by ", "повернуть", CompCommand},
	{"show", "show", "показать объект", CompCommand},
	{"hide", "hide", "спрятать объект", CompCommand},
	{"destroy", "destroy", "удалить объект", CompCommand},
	{"spawn", "spawn ", "создать копию шаблона", CompCommand},
	{"play sound", "play sound ", "проиграть звук", CompCommand},
	{"play music", "play music ", "включить музыку", CompCommand},
	{"stop music", "stop music", "выключить музыку", CompCommand},
	{"print", "print ", "вывести в консоль", CompCommand},
	{"send", "send ", "отправить сообщение", CompCommand},
	{"wait", "wait ", "подождать", CompCommand},
	{"go to scene", "go to scene ", "перейти на другую сцену", CompCommand},
	{"restart scene", "restart scene", "начать игру заново", CompCommand},
	{"remember", "remember ", "запомнить значение", CompCommand},
	{"add", "add ", "добавить в список", CompCommand},
	{"remove", "remove ", "убрать из списка", CompCommand},
	{"clear", "clear ", "очистить список", CompCommand},
	{"sort", "sort ", "отсортировать список", CompCommand},
	{"shuffle", "shuffle ", "перемешать список", CompCommand},
}

var cmEvents = []cmItem{
	{"start", "start", "один раз при появлении объекта", CompEvent},
	{"update", "update", "каждый кадр", CompEvent},
	{"key", "key ", "клавиша", CompEvent},
	{"click", "click", "клик по объекту", CompEvent},
	{"touch", "touch ", "начал касаться", CompEvent},
	{"stop touching", "stop touching ", "перестал касаться", CompEvent},
	{"message", "message ", "пришло сообщение", CompEvent},
	{"every", "every ", "повторять по таймеру", CompEvent},
}

var cmBuiltinDetail = map[string]string{
	"abs": "модуль числа", "min": "меньшее из двух", "max": "большее из двух",
	"round": "округлить", "floor": "округлить вниз", "ceil": "округлить вверх",
	"sqrt": "квадратный корень", "sin": "синус (градусы)", "cos": "косинус (градусы)",
	"random": "случайное целое от a до b", "pick": "случайный элемент списка",
	"upper": "ЗАГЛАВНЫЕ буквы", "lower": "строчные буквы", "number": "текст в число",
	"find": "найти объект по имени", "all": "все объекты с меткой", "count": "сколько объектов с меткой",
	"exists": "существует ли объект", "distance": "расстояние между объектами",
	"touching": "касаюсь ли метки", "opposite": "противоположное направление",
	"recall": "достать сохранённое значение",
}

var cmProperties = []cmItem{
	{"x", "x", "положение по горизонтали", CompProperty},
	{"y", "y", "положение по вертикали", CompProperty},
	{"width", "width", "ширина", CompProperty},
	{"height", "height", "высота", CompProperty},
	{"angle", "angle", "поворот в градусах", CompProperty},
	{"visible", "visible", "виден ли объект", CompProperty},
	{"image", "image", "картинка", CompProperty},
	{"tint", "tint", "цвет-оттенок", CompProperty},
	{"flip", "flip", "отзеркалить", CompProperty},
	{"layer", "layer", "порядок рисования", CompProperty},
	{"tag", "tag", "метка", CompProperty},
	{"name", "name", "имя в сцене", CompProperty},
	{"text", "text", "надпись Text", CompProperty},
	{"font_size", "font_size", "размер шрифта", CompProperty},
}

var cmDotExtra = []cmItem{
	{"length", "length", "длина списка или текста", CompProperty},
	{"first", "first", "первый элемент", CompProperty},
	{"last", "last", "последний элемент", CompProperty},
}

var cmKeyNames = []string{"left", "right", "up", "down", "space", "enter", "escape", "shift", "ctrl"}

// ---- разбор префикса строки ----

type cmTok struct {
	kind byte // 'w' слово, 'n' число, 's' закрытая строка, 'p' знак
	text string
}

type cmCtx struct {
	toks     []cmTok // токены текущего контекста кода (до слова под курсором)
	partial  string  // слово под курсором (уже набранная часть)
	afterDot bool
	inString bool
	inBrace  bool   // курсор внутри вставки {…} в тексте
	strText  string // набранное в строке (если inString)
	comment  bool
	indent   int
}

func cmScan(p []rune) (c cmCtx) {
	var stackLen int
	defer func() { c.inBrace = stackLen > 1 }()
	for c.indent < len(p) && p[c.indent] == ' ' {
		c.indent++
	}
	type frame struct{ toks []cmTok }
	stack := []frame{{}}
	top := func() *frame { stackLen = len(stack); return &stack[len(stack)-1] }
	i := 0
	n := len(p)
	for i < n {
		ch := p[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r':
			i++
		case ch == '#':
			c.comment = true
			return c
		case ch == '"':
			// строка: ищем конец или вставку
			j := i + 1
			closed := false
			opened := false
			for j < n {
				if p[j] == '\\' {
					j += 2
					continue
				}
				if p[j] == '"' {
					closed = true
					break
				}
				if p[j] == '{' {
					opened = true
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			switch {
			case closed:
				top().toks = append(top().toks, cmTok{'s', string(p[i+1 : j])})
				i = j + 1
			case opened:
				// вставка: открываем новый кадр кода; закрытие «}» вернёт в строку
				stack = append(stack, frame{})
				i = j + 1
				// помечаем, что родитель — строка, фиктивным токеном
				stack[len(stack)-2].toks = append(stack[len(stack)-2].toks, cmTok{'S', ""})
			default:
				c.inString = true
				c.strText = string(p[i+1 : n])
				c.toks = top().toks
				return c
			}
		case ch == '}' && len(stack) > 1:
			stack = stack[:len(stack)-1]
			// убираем фиктивный токен и продолжаем строку после «}»
			pt := top().toks
			pt = pt[:len(pt)-1]
			top().toks = pt
			j := i + 1
			closed := false
			opened := false
			for j < n {
				if p[j] == '\\' {
					j += 2
					continue
				}
				if p[j] == '"' {
					closed = true
					break
				}
				if p[j] == '{' {
					opened = true
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			switch {
			case closed:
				// строка закончилась: считаем её значением
				top().toks = append(top().toks, cmTok{'s', ""})
				i = j + 1
			case opened:
				stack = append(stack, frame{})
				top2 := &stack[len(stack)-2]
				top2.toks = append(top2.toks, cmTok{'S', ""})
				i = j + 1
			default:
				c.inString = true
				c.strText = string(p[i+1 : n])
				c.toks = top().toks
				return c
			}
		case hlIsStart(ch):
			j := i
			for j < n && hlIsPart(p[j]) {
				j++
			}
			if j == n {
				c.partial = string(p[i:j])
				c.afterDot = len(top().toks) > 0 && top().toks[len(top().toks)-1].text == "." && top().toks[len(top().toks)-1].kind == 'p'
				if c.afterDot {
					top().toks = top().toks[:len(top().toks)-1]
				}
				c.toks = top().toks
				return c
			}
			top().toks = append(top().toks, cmTok{'w', string(p[i:j])})
			i = j
		case ch >= '0' && ch <= '9':
			j := i
			for j < n && (p[j] >= '0' && p[j] <= '9' || p[j] == '.' && j+1 < n && p[j+1] >= '0' && p[j+1] <= '9') {
				j++
			}
			top().toks = append(top().toks, cmTok{'n', string(p[i:j])})
			i = j
		default:
			j := i + 1
			if (ch == '<' || ch == '>' || ch == '=' || ch == '+' || ch == '-' || ch == '*' || ch == '/') && j < n && p[j] == '=' {
				j++
			}
			top().toks = append(top().toks, cmTok{'p', string(p[i:j])})
			i = j
		}
	}
	if len(top().toks) > 0 {
		t := top().toks[len(top().toks)-1]
		if t.kind == 'p' && t.text == "." {
			c.afterDot = true
			top().toks = top().toks[:len(top().toks)-1]
		}
	}
	c.toks = top().toks
	return c
}

// ---- сведения из файла ----

type cmFile struct {
	vars     map[string]CompletionKind // имя -> вид
	actions  []string
	messages []string
	handler  string // «touch», «message» или "" — обработчик, в котором курсор
}

func cmWords(line string) []string {
	var out []string
	r := []rune(line)
	i := 0
	for i < len(r) {
		if hlIsStart(r[i]) {
			j := i
			for j < len(r) && hlIsPart(r[j]) {
				j++
			}
			out = append(out, string(r[i:j]))
			i = j
		} else {
			i++
		}
	}
	return out
}

func cmQuoted(line, after string) string {
	k := strings.Index(line, after)
	if k < 0 {
		return ""
	}
	rest := line[k+len(after):]
	a := strings.IndexByte(rest, '"')
	if a < 0 {
		return ""
	}
	b := strings.IndexByte(rest[a+1:], '"')
	if b < 0 {
		return ""
	}
	return rest[a+1 : a+1+b]
}

func cmScanFile(lines []string, cur int) cmFile {
	f := cmFile{vars: map[string]CompletionKind{}}
	seenMsg := map[string]bool{}
	addMsg := func(m string) {
		if m != "" && !seenMsg[m] {
			seenMsg[m] = true
			f.messages = append(f.messages, m)
		}
	}
	itemStart := 0
	for i := 0; i < cur && i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		trim := strings.TrimLeft(line, " ")
		if trim != "" && !strings.HasPrefix(trim, "#") && len(line) == len(trim) {
			itemStart = i
		}
	}
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trim, "#") {
			continue
		}
		indent0 := len(line) == len(trim)
		w := cmWords(cmStripCode(line))
		if len(w) == 0 {
			continue
		}
		if w[0] == "when" && len(w) > 1 && w[1] == "message" {
			addMsg(cmQuoted(line, "message"))
		}
		if w[0] == "send" {
			addMsg(cmQuoted(line, "send"))
		}
		if i == cur {
			continue
		}
		local := i < cur && i >= itemStart
		switch w[0] {
		case "var", "shared":
			if len(w) > 1 && (indent0 || local) {
				f.vars[w[1]] = CompVariable
			}
		case "to":
			if indent0 && len(w) > 1 {
				f.actions = append(f.actions, w[1])
				if i < cur && i == itemStart {
					for _, p := range w[2:] {
						f.vars[p] = CompVariable
					}
				}
			}
		case "for":
			if local && len(w) > 2 && w[1] == "each" {
				f.vars[w[2]] = CompVariable
			}
		}
	}
	if cur < len(lines) && strings.HasPrefix(lines[cur], " ") && itemStart < len(lines) {
		hw := cmWords(cmStripCode(lines[itemStart]))
		if len(hw) > 1 && hw[0] == "when" {
			switch hw[1] {
			case "touch":
				f.handler = "touch"
			case "stop":
				f.handler = "touch"
			case "message":
				f.handler = "message"
			}
		}
	}
	return f
}

// cmStripCode убирает из строки тексты в кавычках и комментарий, чтобы слова внутри
// них не принимались за код.
func cmStripCode(line string) string {
	var b strings.Builder
	in := false
	for _, ch := range line {
		switch {
		case ch == '"':
			in = !in
			b.WriteByte(' ')
		case in:
		case ch == '#':
			return b.String()
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// ---- основная функция ----

// Complete возвращает подсказки для позиции курсора (line, col — с нуля, col в рунах).
// Контекст определяется по тексту текущей строки перед курсором и простому построчному
// скану файла (без Parse), поэтому работает на недописанном коде. env может быть nil.
// Результат: не более 50 штук; сначала совпадающие по началу (без учёта регистра),
// затем содержащие префикс, внутри групп — по алфавиту. Insert заменяет слово под курсором.
func Complete(src string, line, col int, env *CompleteEnv) []Completion {
	lines := strings.Split(strings.TrimPrefix(src, "\uFEFF"), "\n") // BOM срезается, как в лексере
	if line < 0 || line >= len(lines) {
		return nil
	}
	rl := []rune(strings.TrimSuffix(lines[line], "\r"))
	if col < 0 {
		col = 0
	}
	if col > len(rl) {
		col = len(rl)
	}
	ctx := cmScan(rl[:col])
	if ctx.comment {
		return nil
	}
	if env == nil {
		env = &CompleteEnv{}
	}
	file := cmScanFile(lines, line)
	var items []cmItem
	prefix := ctx.partial
	if ctx.inString {
		items = cmStringItems(&ctx, env, &file)
		prefix = ctx.strText
	} else {
		items = cmCodeItems(&ctx, env, &file)
	}
	return cmFinish(items, prefix)
}

func cmFinish(items []cmItem, prefix string) []Completion {
	lp := strings.ToLower(prefix)
	type scored struct {
		c    Completion
		rank int
	}
	var res []scored
	seen := map[string]bool{}
	for _, it := range items {
		ll := strings.ToLower(it.label)
		rank := 0
		switch {
		case lp == "" || strings.HasPrefix(ll, lp):
		case strings.Contains(ll, lp):
			rank = 1
		default:
			continue
		}
		if seen[it.label+"\x00"+it.insert] {
			continue
		}
		seen[it.label+"\x00"+it.insert] = true
		res = append(res, scored{Completion{it.label, it.insert, it.kind, it.detail}, rank})
	}
	sort.SliceStable(res, func(a, b int) bool {
		if res[a].rank != res[b].rank {
			return res[a].rank < res[b].rank
		}
		// встроенные функции в конце: при пустом префиксе предел в 50 не должен
		// отрезать команды и управление
		if ba, bb := res[a].c.Kind == CompBuiltin, res[b].c.Kind == CompBuiltin; ba != bb {
			return bb
		}
		return res[a].c.Label < res[b].c.Label
	})
	if len(res) > completeLimit {
		res = res[:completeLimit]
	}
	if len(res) == 0 {
		return nil
	}
	out := make([]Completion, len(res))
	for i, s := range res {
		out[i] = s.c
	}
	return out
}

func cmList(names []string, kind CompletionKind, detail string, quote bool) []cmItem {
	out := make([]cmItem, 0, len(names))
	for _, n := range names {
		ins := n
		if quote {
			ins = `"` + n + `"`
		}
		out = append(out, cmItem{n, ins, detail, kind})
	}
	return out
}

// строка в кавычках: подсказки только для известных путей и меток
func cmStringItems(c *cmCtx, env *CompleteEnv, f *cmFile) []cmItem {
	t := c.toks
	n := len(t)
	if n == 0 {
		return nil
	}
	w := func(k int) string { // слово с конца (0 — последнее)
		if n-1-k < 0 || t[n-1-k].kind != 'w' {
			return ""
		}
		return t[n-1-k].text
	}
	p := func(k int) string {
		if n-1-k < 0 || t[n-1-k].kind != 'p' {
			return ""
		}
		return t[n-1-k].text
	}
	first := ""
	if t[0].kind == 'w' {
		first = t[0].text
	}
	tagsAndObjs := func() []cmItem {
		return append(cmList(env.Tags, CompTag, "метка", false), cmList(env.Objects, CompObject, "объект сцены", false)...)
	}
	switch {
	case first == "when" && (w(0) == "touch" || w(0) == "touching"):
		return tagsAndObjs()
	case first == "when" && w(0) == "message":
		return cmList(f.messages, CompMessage, "сообщение", false)
	case w(1) == "play" && (w(0) == "sound" || w(0) == "music"):
		return cmList(env.Assets, CompAsset, "файл из assets", false)
	case p(0) == "=" && w(1) == "image":
		return cmList(env.Assets, CompAsset, "файл из assets", false)
	case p(0) == "=" && w(1) == "tag":
		return cmList(env.Tags, CompTag, "метка", false)
	case w(0) == "use" && n == 1:
		return cmList(env.Scripts, CompScript, "скрипт", false)
	case w(0) == "scene" && w(1) == "to":
		return cmList(env.Scenes, CompScene, "сцена", false)
	case w(0) == "send" && n == 1:
		return cmList(f.messages, CompMessage, "сообщение", false)
	case w(0) == "avoiding":
		return cmList(env.Tags, CompTag, "метка", false)
	case p(0) == "(" && (w(1) == "touching" || w(1) == "all" || w(1) == "count"):
		return cmList(env.Tags, CompTag, "метка", false)
	case p(0) == "(" && w(1) == "find":
		return cmList(env.Objects, CompObject, "объект сцены", false)
	}
	return nil
}

func cmValueEnd(t cmTok) bool {
	switch t.kind {
	case 'n', 's':
		return true
	case 'w':
		return !(hlOperators[t.text] || cmExprStartWords[t.text] || cmFollowWords[t.text])
	case 'p':
		return t.text == ")" || t.text == "]"
	}
	return false
}

// слова, после которых ждём выражение
var cmExprStartWords = hlSet(`if while print return is not and or in by to at with as from
	wait destroy clear sort shuffle add remove remember repeat avoiding times touch message every
	spawn turn send slide`)

// слова-связки, которые не заканчивают значение
var cmFollowWords = hlSet(`seconds second forever sound music scene repeating round`)

func cmCodeItems(c *cmCtx, env *CompleteEnv, f *cmFile) []cmItem {
	t := c.toks
	n := len(t)

	if c.afterDot {
		return append(append([]cmItem{}, cmProperties...), cmDotExtra...)
	}
	if c.inBrace {
		if n == 0 || cmExprStart(t[n-1]) {
			return cmExpr(env, f, c)
		}
		return nil
	}
	if n == 0 {
		if c.indent == 0 {
			return cmTopLevel
		}
		return cmStatementStart(f)
	}
	word := func(k int) string {
		if k < 0 || k >= n || t[k].kind != 'w' {
			return ""
		}
		return t[k].text
	}
	punct := func(k int) string {
		if k < 0 || k >= n || t[k].kind != 'p' {
			return ""
		}
		return t[k].text
	}
	first := word(0)
	last := n - 1
	cont := func(words ...string) []cmItem { // слова-продолжения
		out := make([]cmItem, 0, len(words))
		for _, w := range words {
			out = append(out, cmItem{w, w + " ", "продолжение команды", CompValue})
		}
		return out
	}
	hasP := func(w string) bool {
		for i := range t {
			if punct(i) == w {
				return true
			}
		}
		return false
	}
	quoted := func(names []string, kind CompletionKind, d string) []cmItem { return cmList(names, kind, d, true) }
	has := func(w string) bool {
		for i := range t {
			if word(i) == w {
				return true
			}
		}
		return false
	}

	// строки со своими правилами
	switch first {
	case "when":
		switch {
		case n == 1:
			return cmEvents
		case n == 2 && word(1) == "stop":
			return []cmItem{{"touching", "touching ", "перестал касаться", CompEvent}}
		case word(1) == "key" && n == 2:
			return cmList(cmKeyNames, CompValue, "клавиша", false)
		case word(1) == "key" && n == 3:
			return []cmItem{{"pressed", "pressed", "в кадр нажатия", CompEvent}, {"released", "released", "в кадр отпускания", CompEvent}, {"held", "held", "пока зажата", CompEvent}}
		case word(1) == "touch" && n == 2, word(1) == "stop" && word(2) == "touching" && n == 3:
			return append(quoted(env.Tags, CompTag, "метка"), cmList(env.Objects, CompObject, "объект сцены", false)...)
		case word(1) == "message" && n == 2:
			return quoted(f.messages, CompMessage, "сообщение")
		case word(1) == "every" && n >= 3 && cmValueEnd(t[last]):
			return cont("seconds", "second")
		}
	case "use":
		if n == 1 {
			return quoted(env.Scripts, CompScript, "скрипт")
		}
		return nil
	case "to":
		return nil
	case "var", "shared":
		if n == 1 {
			return nil
		}
		if n == 2 {
			return []cmItem{{"=", "= ", "начальное значение", CompValue}}
		}
	case "else":
		if n == 1 {
			return cont("if")
		}
	case "for":
		switch {
		case n == 1:
			return cont("each")
		case n == 2:
			return nil
		case n == 3 && word(1) == "each":
			return cont("in")
		}
	case "stop":
		if n == 1 {
			return []cmItem{{"music", "music", "выключить музыку", CompValue}, {"repeating", "repeating", "выйти из цикла", CompValue}}
		}
		return nil
	case "next":
		if n == 1 {
			return cont("round")
		}
		return nil
	case "restart":
		if n == 1 {
			return cont("scene")
		}
		return nil
	case "go":
		switch {
		case n == 1:
			return cont("to")
		case n == 2:
			return cont("scene")
		case n == 3:
			return quoted(env.Scenes, CompScene, "сцена")
		}
		return nil
	case "play":
		switch {
		case n == 1:
			return cont("sound", "music")
		case n == 2 && (word(1) == "sound" || word(1) == "music"):
			return quoted(env.Assets, CompAsset, "файл из assets")
		}
		return nil
	case "turn":
		if n == 1 {
			return cont("by")
		}
	case "show", "hide":
		return nil
	case "move":
		switch {
		case n == 1:
			out := []cmItem{{"left", "left ", "влево", CompValue}, {"right", "right ", "вправо", CompValue},
				{"up", "up ", "вверх", CompValue}, {"down", "down ", "вниз", CompValue}, {"to", "to ", "перенести в точку", CompValue}}
			return append(out, cmVars(f)...)
		case n == 2 && word(1) != "to":
			return cont("by")
		case has("by") && !has("avoiding") && (cmValueEnd(t[last]) || word(last) == "second"):
			if word(last) == "second" {
				return cont("avoiding")
			}
			return []cmItem{{"per second", "per second", "в секунду", CompValue}, {"avoiding", "avoiding ", "не заходя в объекты с меткой", CompValue}}
		case word(last) == "per":
			return cont("second")
		case word(last) == "avoiding":
			return quoted(env.Tags, CompTag, "метка")
		}
	case "send":
		switch {
		case n == 1:
			return quoted(f.messages, CompMessage, "сообщение")
		case n >= 2 && cmValueEnd(t[last]) && !has("to"):
			return cont("with", "to")
		}
	case "wait":
		if n >= 2 && cmValueEnd(t[last]) {
			return cont("seconds", "second")
		}
	case "slide":
		switch {
		case n == 1:
			return cont("to")
		case has("in") && cmValueEnd(t[last]):
			return cont("seconds", "second")
		case has("to") && cmValueEnd(t[last]) && hasP(",") && !has("in"):
			return cont("in")
		}
	case "repeat":
		if n == 1 {
			return append(cont("forever"), cmExpr(env, f, c)...)
		}
		if cmValueEnd(t[last]) && !has("times") && word(1) != "forever" {
			return cont("times")
		}
	case "add":
		if n >= 2 && cmValueEnd(t[last]) && !has("to") {
			return cont("to")
		}
	case "remove":
		if n >= 2 && cmValueEnd(t[last]) && !has("from") {
			return cont("from")
		}
	case "remember":
		if n >= 2 && cmValueEnd(t[last]) && !has("as") {
			return cont("as")
		}
	case "spawn":
		if n >= 2 && cmValueEnd(t[last]) && !has("at") {
			return cont("at")
		}
	}

	if word(last) == "per" {
		return cont("second")
	}
	// присваивание картинке и метке
	if punct(last) == "=" && (word(last-1) == "image") {
		return quoted(env.Assets, CompAsset, "файл из assets")
	}
	if punct(last) == "=" && word(last-1) == "tag" {
		return quoted(env.Tags, CompTag, "метка")
	}
	// другие выражения
	if cmExprStart(t[last]) {
		return cmExpr(env, f, c)
	}
	// «touching(» / «all(» / «find(» без кавычки
	return nil
}

func cmExprStart(t cmTok) bool {
	switch t.kind {
	case 'w':
		return cmExprStartWords[t.text]
	case 'p':
		switch t.text {
		case ")", "]", "}":
			return false
		}
		return true
	}
	return false
}

func cmVars(f *cmFile) []cmItem {
	out := make([]cmItem, 0, len(f.vars))
	for n := range f.vars {
		out = append(out, cmItem{n, n, "переменная", CompVariable})
	}
	return out
}

func cmStatementStart(f *cmFile) []cmItem {
	out := append([]cmItem{}, cmControl...)
	out = append(out, cmCommands...)
	for _, v := range cmVars(f) {
		out = append(out, v)
	}
	for _, a := range f.actions {
		out = append(out, cmItem{a, a + "(", "своя команда", CompAction})
	}
	out = append(out, cmBuiltinItems()...)
	return out
}

func cmBuiltinItems() []cmItem {
	var out []cmItem
	for n, d := range cmBuiltinDetail {
		out = append(out, cmItem{n, n + "(", d, CompBuiltin})
	}
	return out
}

func cmExpr(env *CompleteEnv, f *cmFile, c *cmCtx) []cmItem {
	out := cmVars(f)
	for _, a := range f.actions {
		out = append(out, cmItem{a, a + "(", "своя команда", CompAction})
	}
	out = append(out, cmBuiltinItems()...)
	out = append(out, cmProperties...)
	out = append(out,
		cmItem{"yes", "yes", "да", CompValue},
		cmItem{"no", "no", "нет", CompValue},
		cmItem{"nothing", "nothing", "ничего", CompValue},
		cmItem{"left", "left", "влево", CompValue},
		cmItem{"right", "right", "вправо", CompValue},
		cmItem{"up", "up", "вверх", CompValue},
		cmItem{"down", "down", "вниз", CompValue},
		cmItem{"self", "self", "сам объект", CompValue},
		cmItem{"mouse", "mouse", "мышь", CompValue},
		cmItem{"key", "key ", "клавиша (key space is held)", CompValue},
		cmItem{"not", "not ", "отрицание", CompValue},
	)
	if f.handler == "touch" {
		out = append(out, cmItem{"other", "other", "второй объект касания", CompValue})
	}
	if f.handler == "message" {
		out = append(out, cmItem{"value", "value", "то, что отправили через with", CompValue})
	}
	for _, o := range env.Objects {
		out = append(out, cmItem{o, o, "объект сцены", CompObject})
	}
	return out
}
