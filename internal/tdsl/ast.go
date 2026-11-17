package tdsl

import (
	"strconv"
	"strings"
)

// Pos — позиция в исходном тексте: строка и столбец, оба считаются с единицы.
// Столбцы считаются в символах (рунах), а не в байтах.
type Pos struct {
	Line, Col int
}

// Position возвращает позицию; встраивание Pos делает этот метод общим для всех узлов.
func (p Pos) Position() Pos { return p }

// Node — любой узел дерева.
type Node interface {
	Position() Pos
}

// Item — элемент верхнего уровня: var, shared, use, to, when.
type Item interface {
	Node
	itemNode()
}

// Stmt — команда или управляющая конструкция внутри блока.
type Stmt interface {
	Node
	stmtNode()
}

// Expr — выражение. String печатает его в виде S-выражения (см. тесты приоритетов).
type Expr interface {
	Node
	String() string
	exprNode()
}

// Script — разобранный файл .tdsl.
type Script struct {
	Items []Item
}

// ---- Элементы верхнего уровня ----

// VarDecl — «var имя = значение». Встречается и на верхнем уровне (память объекта),
// и внутри блока (локальная переменная).
type VarDecl struct {
	Pos
	Name    string
	NamePos Pos
	Value   Expr
}

// SharedDecl — «shared имя = значение» (общая память игры).
type SharedDecl struct {
	Pos
	Name    string
	NamePos Pos
	Value   Expr
}

// UseDecl — «use "файл.tdsl"».
type UseDecl struct {
	Pos
	Path string
}

// Param — параметр своей команды.
type Param struct {
	Pos
	Name string
}

// ActionDef — своя команда: «to имя(параметры)» и тело.
type ActionDef struct {
	Pos
	Name    string
	NamePos Pos
	Params  []Param
	Body    []Stmt
}

// EventKind — вид события в «when».
type EventKind int

// Виды событий.
const (
	EventStart EventKind = iota
	EventUpdate
	EventKey
	EventClick
	EventTouch
	EventStopTouching
	EventMessage
	EventEvery
)

// KeyState — состояние клавиши в событии «when key ...».
type KeyState string

// Состояния клавиши.
const (
	KeyPressed  KeyState = "pressed"
	KeyReleased KeyState = "released"
	KeyHeld     KeyState = "held"
)

// Handler — обработчик события «when ...». Arg — выражение события
// (метка для touch, текст для message, период для every); для остальных nil.
// Для EventKey заполнены Key и State. Единицы «second»/«seconds» равнозначны и не хранятся.
type Handler struct {
	Pos
	Event EventKind
	Key   string
	State KeyState
	Arg   Expr
	Body  []Stmt
}

func (*VarDecl) itemNode()    {}
func (*SharedDecl) itemNode() {}
func (*UseDecl) itemNode()    {}
func (*ActionDef) itemNode()  {}
func (*Handler) itemNode()    {}

// ---- Команды и управление ----

// Assign — присваивание «цель op значение». Target — *Ident, *Member или *Index;
// Op — один из «=», «+=», «-=», «*=», «/=».
type Assign struct {
	Pos
	Target Expr
	Op     string
	Value  Expr
}

// Branch — ветка «if» или «else if».
type Branch struct {
	Pos
	Cond Expr
	Body []Stmt
}

// If — условие. Branches[0] — «if», остальные — «else if»; Else — тело «else» (nil, если его нет).
type If struct {
	Pos
	Branches []Branch
	Else     []Stmt
}

// Repeat — «repeat N times»; при Count == nil это «repeat forever».
type Repeat struct {
	Pos
	Count Expr
	Body  []Stmt
}

// While — «while условие».
type While struct {
	Pos
	Cond Expr
	Body []Stmt
}

// For — «for each имя in ...». Если List != nil, это обход списка,
// иначе обход чисел от From до To.
type For struct {
	Pos
	Var      string
	VarPos   Pos
	List     Expr
	From, To Expr
	Body     []Stmt
}

// StopRepeating — «stop repeating».
type StopRepeating struct{ Pos }

// NextRound — «next round».
type NextRound struct{ Pos }

// Return — «return [значение]».
type Return struct {
	Pos
	Value Expr
}

// ExprStmt — выражение как команда: вызов своей команды или «spawn».
type ExprStmt struct {
	Pos
	X Expr
}

// Move — «move направление by шаг [avoiding метка]».
type Move struct {
	Pos
	Dir, By, Avoiding Expr
}

// MoveTo — «move to x, y».
type MoveTo struct {
	Pos
	X, Y Expr
}

// SlideTo — «slide to x, y in время seconds».
type SlideTo struct {
	Pos
	X, Y, Duration Expr
}

// Turn — «turn by угол».
type Turn struct {
	Pos
	By Expr
}

// Show — «show».
type Show struct{ Pos }

// Hide — «hide».
type Hide struct{ Pos }

// Destroy — «destroy [объект]»; Target == nil значит «сам объект».
type Destroy struct {
	Pos
	Target Expr
}

// Play — «play sound ...» (Kind "sound") или «play music ...» (Kind "music").
type Play struct {
	Pos
	Kind string
	What Expr
}

// StopMusic — «stop music».
type StopMusic struct{ Pos }

// Print — «print значение».
type Print struct {
	Pos
	Value Expr
}

// Send — «send сообщение [with значение] [to объект]».
type Send struct {
	Pos
	Message, With, To Expr
}

// Wait — «wait время seconds».
type Wait struct {
	Pos
	Duration Expr
}

// GoToScene — «go to scene "имя"».
type GoToScene struct {
	Pos
	Scene Expr
}

// RestartScene — «restart scene».
type RestartScene struct{ Pos }

// Remember — «remember ключ as значение».
type Remember struct {
	Pos
	Key, Value Expr
}

// ListCmd — команды над списком: Op "add" / "remove" (заполнены Item и List)
// и "clear" / "sort" / "shuffle" (только List).
type ListCmd struct {
	Pos
	Op   string
	Item Expr
	List Expr
}

func (*VarDecl) stmtNode()       {}
func (*Assign) stmtNode()        {}
func (*If) stmtNode()            {}
func (*Repeat) stmtNode()        {}
func (*While) stmtNode()         {}
func (*For) stmtNode()           {}
func (*StopRepeating) stmtNode() {}
func (*NextRound) stmtNode()     {}
func (*Return) stmtNode()        {}
func (*ExprStmt) stmtNode()      {}
func (*Move) stmtNode()          {}
func (*MoveTo) stmtNode()        {}
func (*SlideTo) stmtNode()       {}
func (*Turn) stmtNode()          {}
func (*Show) stmtNode()          {}
func (*Hide) stmtNode()          {}
func (*Destroy) stmtNode()       {}
func (*Play) stmtNode()          {}
func (*StopMusic) stmtNode()     {}
func (*Print) stmtNode()         {}
func (*Send) stmtNode()          {}
func (*Wait) stmtNode()          {}
func (*GoToScene) stmtNode()     {}
func (*RestartScene) stmtNode()  {}
func (*Remember) stmtNode()      {}
func (*ListCmd) stmtNode()       {}

// ---- Выражения ----

// Number — числовой литерал.
type Number struct {
	Pos
	Value float64
}

// TextPart — кусок текста: либо литерал Lit, либо вставка Expr (тогда Expr != nil).
type TextPart struct {
	Lit  string
	Expr Expr
}

// Text — текстовый литерал со вставками «{выражение}».
type Text struct {
	Pos
	Parts []TextPart
}

// Literal возвращает содержимое текста, если в нём нет вставок.
func (t *Text) Literal() (string, bool) {
	var b strings.Builder
	for _, p := range t.Parts {
		if p.Expr != nil {
			return "", false
		}
		b.WriteString(p.Lit)
	}
	return b.String(), true
}

// Bool — «yes» или «no».
type Bool struct {
	Pos
	Value bool
}

// Nothing — «nothing».
type Nothing struct{ Pos }

// Dir — литерал направления: Name это "left", "right", "up" или "down".
type Dir struct {
	Pos
	Name string
}

// Self — «self».
type Self struct{ Pos }

// Other — «other».
type Other struct{ Pos }

// ValueRef — «value» (значение, пришедшее с сообщением).
type ValueRef struct{ Pos }

// Ident — имя переменной, свойства или объекта сцены.
type Ident struct {
	Pos
	Name string
}

// Unary — «-x» (Op "-") или «not x» (Op "not").
type Unary struct {
	Pos
	Op string
	X  Expr
}

// Binary — двоичная операция. Op: + - * / % and or is "is not" < > <= >= in "not in".
type Binary struct {
	Pos
	Op   string
	L, R Expr
}

// PerSecond — «x per second».
type PerSecond struct {
	Pos
	X Expr
}

// Member — «x.имя».
type Member struct {
	Pos
	X    Expr
	Name string
}

// Index — «x[i]».
type Index struct {
	Pos
	X, I Expr
}

// List — «[a, b, c]».
type List struct {
	Pos
	Elems []Expr
}

// Call — вызов «имя(аргументы)» (встроенная функция или своя команда).
type Call struct {
	Pos
	Name string
	Args []Expr
}

// KeyDown — «key клавиша is held» (клавиша зажата).
type KeyDown struct {
	Pos
	Key string
}

// Spawn — «spawn шаблон at x, y» (и команда, и выражение).
type Spawn struct {
	Pos
	Template, X, Y Expr
}

func (*Number) exprNode()    {}
func (*Text) exprNode()      {}
func (*Bool) exprNode()      {}
func (*Nothing) exprNode()   {}
func (*Dir) exprNode()       {}
func (*Self) exprNode()      {}
func (*Other) exprNode()     {}
func (*ValueRef) exprNode()  {}
func (*Ident) exprNode()     {}
func (*Unary) exprNode()     {}
func (*Binary) exprNode()    {}
func (*PerSecond) exprNode() {}
func (*Member) exprNode()    {}
func (*Index) exprNode()     {}
func (*List) exprNode()      {}
func (*Call) exprNode()      {}
func (*KeyDown) exprNode()   {}
func (*Spawn) exprNode()     {}

func (n *Number) String() string { return strconv.FormatFloat(n.Value, 'g', -1, 64) }
func (n *Bool) String() string {
	if n.Value {
		return "yes"
	}
	return "no"
}
func (n *Nothing) String() string  { return "nothing" }
func (n *Dir) String() string      { return n.Name }
func (n *Self) String() string     { return "self" }
func (n *Other) String() string    { return "other" }
func (n *ValueRef) String() string { return "value" }
func (n *Ident) String() string    { return n.Name }
func (n *Unary) String() string    { return "(" + n.Op + " " + n.X.String() + ")" }
func (n *Binary) String() string {
	return "(" + n.Op + " " + n.L.String() + " " + n.R.String() + ")"
}
func (n *PerSecond) String() string { return "(per-second " + n.X.String() + ")" }
func (n *Member) String() string    { return "(. " + n.X.String() + " " + n.Name + ")" }
func (n *Index) String() string     { return "(index " + n.X.String() + " " + n.I.String() + ")" }
func (n *KeyDown) String() string   { return "(key-held " + n.Key + ")" }
func (n *Spawn) String() string {
	return "(spawn " + n.Template.String() + " " + n.X.String() + " " + n.Y.String() + ")"
}

func (n *Text) String() string {
	var b strings.Builder
	b.WriteByte('"')
	for _, p := range n.Parts {
		if p.Expr != nil {
			b.WriteString("{" + p.Expr.String() + "}")
			continue
		}
		q := strconv.Quote(p.Lit)
		b.WriteString(q[1 : len(q)-1])
	}
	b.WriteByte('"')
	return b.String()
}

func (n *List) String() string {
	parts := make([]string, len(n.Elems))
	for i, e := range n.Elems {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func (n *Call) String() string {
	s := "(call " + n.Name
	for _, a := range n.Args {
		s += " " + a.String()
	}
	return s + ")"
}
