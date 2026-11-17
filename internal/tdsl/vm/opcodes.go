package vm

import "fmt"

// Op is a bytecode instruction code.
type Op uint8

// Instructions. The operands of Instr are A and B; "pop" and "push" refer to
// the value stack of the running Task. Locals live in the stack at the base
// of the current frame, temporaries above them.
const (
	OpNothing     Op = iota // push nothing
	OpTrue                  // push yes
	OpFalse                 // push no
	OpConst                 // push Consts[A]
	OpDir                   // push the direction A
	OpSelf                  // push the object that runs the script
	OpLoadLocal             // push local A
	OpStoreLocal            // pop into local A
	OpLoadGlobal            // push the object variable A
	OpStoreGlobal           // pop into the object variable A
	OpPop                   // drop the top
	OpDup                   // duplicate the top
	OpDup2                  // duplicate the top two values

	OpAdd // pop b, a; push a + b (numbers or texts)
	OpSub
	OpMul
	OpDiv
	OpMod
	OpNeg
	OpPerSecond // pop n; push n / tickRate
	OpNot
	OpEq // is
	OpNe // is not
	OpLt
	OpGt
	OpLe
	OpGe
	OpIn
	OpNotIn

	OpJump        // pc = A
	OpJumpIfFalse // pop a condition; if no, pc = A
	OpAndJump     // top must be yes/no; if no keep it and pc = A, else pop
	OpOrJump      // top must be yes/no; if yes keep it and pc = A, else pop
	OpCheckBool   // top must be yes/no
	OpCheckNum    // top must be a number; Consts[A] says where
	OpCheckList   // top must be a list; Consts[A] says where

	OpList      // pop A values into a new list
	OpText      // pop A values, join their text forms
	OpIndex     // pop i, list; push list[i]; Consts[A] is the list's name or A < 0
	OpSetIndex  // pop v, i, list; list[i] = v
	OpListLen   // pop list; push its length
	OpMember    // pop value; push value.Consts[A] (B is the Prop or 0)
	OpSetMember // pop v, obj; obj.Consts[A] = v (B is the Prop or 0)
	OpGetProp   // pop obj; push its property A
	OpSetProp   // pop v, obj; set property A
	OpKey       // push whether key Consts[A] is held

	OpCall    // call function A with B arguments
	OpReturn  // pop the result and return from the function
	OpBuiltin // call built-in function A with B arguments

	OpMove    // pop by, dir; move self
	OpMoveTo  // pop y, x; put self there
	OpDestroy // pop obj; destroy it
	OpPrint   // pop a value; log it

	OpListAppend  // pop list, item; add item
	OpListRemove  // pop list, item; remove the first equal item
	OpListClear   // pop list; clear it
	OpListSort    // pop list; sort it
	OpListShuffle // pop list; shuffle it

	OpFail // stop with the failure Fails[A] (unsupported feature or a mistake found at compile time)
)

var opNames = [...]string{
	"NOTHING", "TRUE", "FALSE", "CONST", "DIR", "SELF", "LOADLOCAL", "STORELOCAL", "LOADGLOBAL", "STOREGLOBAL",
	"POP", "DUP", "DUP2", "ADD", "SUB", "MUL", "DIV", "MOD", "NEG", "PERSECOND", "NOT", "EQ", "NE", "LT", "GT",
	"LE", "GE", "IN", "NOTIN", "JUMP", "JUMPIFFALSE", "ANDJUMP", "ORJUMP", "CHECKBOOL", "CHECKNUM", "CHECKLIST",
	"LIST", "TEXT", "INDEX", "SETINDEX", "LISTLEN", "MEMBER", "SETMEMBER", "GETPROP", "SETPROP", "KEY", "CALL",
	"RETURN", "BUILTIN", "MOVE", "MOVETO", "DESTROY", "PRINT", "LISTAPPEND", "LISTREMOVE", "LISTCLEAR",
	"LISTSORT", "LISTSHUFFLE", "FAIL",
}

func (o Op) String() string {
	if int(o) < len(opNames) {
		return opNames[o]
	}
	return fmt.Sprintf("OP%d", o)
}

// Instr is one instruction.
type Instr struct {
	Op   Op
	A, B int32
}

// Span is a place in the source: the line and the columns [Col, EndCol).
type Span struct {
	Line, Col, EndCol int32
}

// Func is a compiled function: a "to" command, a handler body or an
// initializer.
type Func struct {
	Name    string
	NParams int
	NLocals int // parameters, variables and hidden temporaries
	Code    []Instr
	Spans   []Span // Spans[i] is the source place of Code[i]
}

// failure is the payload of OpFail.
type failure struct {
	code, msg, hint string
}

// builtin function ids.
const (
	bAbs = iota
	bMin
	bMax
	bRound
	bFloor
	bCeil
	bSqrt
	bRandom
	bPick
	bUpper
	bLower
	bNumber
	bOpposite
)

var builtinIDs = map[string]int{
	"abs": bAbs, "min": bMin, "max": bMax, "round": bRound, "floor": bFloor, "ceil": bCeil,
	"sqrt": bSqrt, "random": bRandom, "pick": bPick, "upper": bUpper, "lower": bLower,
	"number": bNumber, "opposite": bOpposite,
}

var builtinArgs = [...]int{bAbs: 1, bMin: 2, bMax: 2, bRound: 1, bFloor: 1, bCeil: 1, bSqrt: 1, bRandom: 2,
	bPick: 1, bUpper: 1, bLower: 1, bNumber: 1, bOpposite: 1}

// unsupportedBuiltins are legal TrueDSL functions this version does not run.
var unsupportedBuiltins = map[string]string{
	"find": "поиск объектов", "all": "поиск объектов", "count": "поиск объектов", "exists": "поиск объектов",
	"distance": "расстояние между объектами", "touching": "касания", "recall": "recall (память игры)",
	"sin": "sin и cos (их результат отличается на разных процессорах)", "cos": "sin и cos (их результат отличается на разных процессорах)",
}
