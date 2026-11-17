package vm

import (
	"math"
	"strconv"
	"strings"
)

// Kind is the type of a Value.
type Kind uint8

// The seven kinds of values (reference.md, section 3).
const (
	KNothing Kind = iota
	KNum
	KText
	KBool
	KList
	KDir
	KObj
)

// Dir is a direction value.
type Dir uint8

// Directions.
const (
	Left Dir = iota
	Right
	Up
	Down
)

var dirNames = [...]string{"left", "right", "up", "down"}

func (d Dir) String() string { return dirNames[d&3] }

// Opposite returns left for right, up for down and vice versa.
func (d Dir) Opposite() Dir {
	switch d {
	case Left:
		return Right
	case Right:
		return Left
	case Up:
		return Down
	}
	return Up
}

// Delta returns the shift of a move by n pixels. The y axis grows downward.
func (d Dir) Delta(n float64) (dx, dy float64) {
	switch d {
	case Left:
		return float64(-n), 0
	case Right:
		return n, 0
	case Up:
		return 0, float64(-n)
	}
	return 0, n
}

// List is the payload of a list value. Lists are shared by reference.
type List struct {
	Items []Value
}

// Value is one TrueDSL value. Which field is meaningful depends on K: N holds
// numbers, booleans (0 or 1), directions and object ids; S holds text; L
// holds lists.
type Value struct {
	K Kind
	N float64
	S string
	L *List
}

// Constructors.
func Nothing() Value               { return Value{} }
func Num(f float64) Value          { return Value{K: KNum, N: f} }
func Text(s string) Value          { return Value{K: KText, S: s} }
func DirValue(d Dir) Value         { return Value{K: KDir, N: float64(d)} }
func Obj(id ObjID) Value           { return Value{K: KObj, N: float64(id)} }
func ListValue(l *List) Value      { return Value{K: KList, L: l} }
func NewList(items ...Value) Value { return Value{K: KList, L: &List{Items: items}} }

// Bool makes a yes/no value.
func Bool(b bool) Value {
	if b {
		return Value{K: KBool, N: 1}
	}
	return Value{K: KBool}
}

// IsTrue reports whether a KBool value is yes.
func (v Value) IsTrue() bool { return v.N != 0 }

// Dir returns the direction of a KDir value.
func (v Value) Dir() Dir { return Dir(v.N) }

// Obj returns the object id of a KObj value.
func (v Value) Obj() ObjID { return ObjID(v.N) }

// Names of the types in the messages, as in the static checker.
var (
	kindNom = [...]string{KNothing: "«ничего»", KNum: "число", KText: "текст", KBool: "«да» или «нет»", KList: "список", KDir: "направление", KObj: "объект"}
	kindAcc = [...]string{KNothing: "«ничего»", KNum: "число", KText: "текст", KBool: "значение «да» или «нет»", KList: "список", KDir: "направление", KObj: "объект"}
	kindIns = [...]string{KNothing: "«ничем»", KNum: "числом", KText: "текстом", KBool: "значением «да» или «нет»", KList: "списком", KDir: "направлением", KObj: "объектом"}
)

// FormatNum shows a number: whole numbers without a fraction, others with 12
// significant digits. The result is the same on every platform.
func FormatNum(f float64) string {
	switch {
	case f == 0:
		return "0"
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case f == math.Floor(f) && math.Abs(f) < 1e15:
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	return strconv.FormatFloat(f, 'g', 12, 64)
}

// String is how a value looks in "print" and in {insertions}.
func (v Value) String() string {
	s, _ := v.show(false, 0, math.MaxInt)
	return s
}

// show renders v. Texts inside lists are quoted. It gives up (ok false) when
// the result would exceed limit bytes or lists are nested too deeply.
func (v Value) show(quote bool, depth, limit int) (string, bool) {
	switch v.K {
	case KNum:
		return FormatNum(v.N), true
	case KText:
		if quote {
			return strconv.Quote(v.S), true
		}
		return v.S, true
	case KBool:
		if v.IsTrue() {
			return "yes", true
		}
		return "no", true
	case KDir:
		return v.Dir().String(), true
	case KObj:
		return "object", true
	case KList:
		if depth > 32 {
			return "[...]", true
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, it := range v.L.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			s, ok := it.show(true, depth+1, limit-b.Len())
			if !ok {
				return "", false
			}
			b.WriteString(s)
			if b.Len() > limit {
				return "", false
			}
		}
		b.WriteByte(']')
		return b.String(), true
	}
	return "nothing", true
}

// work bounds the effort of comparing nested lists.
type work struct {
	n    int
	over bool
}

const maxCompareWork = 200_000

// equal is the "is" relation for two values of the same kind (or nothing).
func equal(a, b Value, w *work) bool {
	if a.K != b.K {
		return false
	}
	switch a.K {
	case KNothing:
		return true
	case KText:
		return a.S == b.S
	case KList:
		if a.L == b.L {
			return true
		}
		if len(a.L.Items) != len(b.L.Items) {
			return false
		}
		for i := range a.L.Items {
			if w.n++; w.n > maxCompareWork {
				w.over = true
				return false
			}
			if !equal(a.L.Items[i], b.L.Items[i], w) {
				return false
			}
		}
		return true
	}
	return a.N == b.N
}

// textTooLong reports whether s has more than limit characters.
func textTooLong(s string, limit int) bool {
	if len(s) <= limit {
		return false
	}
	n := 0
	for range s {
		n++
	}
	return n > limit
}
