package tdsl

// Таблицы, которые нужны семантической проверке (check*.go).

// vtype — статический тип значения; tyUnknown означает «не знаю», и тогда ничего не сообщается.
type vtype int

const (
	tyUnknown vtype = iota
	tyNum
	tyText
	tyBool
	tyList
	tyDir
	tyObj
	tyNothing
)

var typeNom = map[vtype]string{
	tyNum: "число", tyText: "текст", tyBool: "«да» или «нет»", tyList: "список",
	tyDir: "направление", tyObj: "объект", tyNothing: "«ничего»",
}

var typeAcc = map[vtype]string{
	tyNum: "число", tyText: "текст", tyBool: "значение «да» или «нет»", tyList: "список",
	tyDir: "направление", tyObj: "объект", tyNothing: "«ничего»",
}

var typeIns = map[vtype]string{
	tyNum: "числом", tyText: "текстом", tyBool: "значением «да» или «нет»", tyList: "списком",
	tyDir: "направлением", tyObj: "объектом", tyNothing: "«ничем»",
}

// objectProps — встроенные свойства объекта и их типы.
var objectProps = map[string]vtype{
	"x": tyNum, "y": tyNum, "width": tyNum, "height": tyNum, "angle": tyNum,
	"visible": tyBool, "image": tyText, "tint": tyText, "flip": tyBool,
	"layer": tyNum, "tag": tyText, "name": tyText, "text": tyText, "font_size": tyNum,
}

// propNames — порядок для подсказок.
var propNames = []string{"x", "y", "width", "height", "angle", "visible", "image", "tint", "flip", "layer", "tag", "name", "text", "font_size"}

// keywordSet — зарезервированные слова (из справочника, раздел 11).
var keywordSet = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range []string{
		"add", "and", "as", "at", "avoiding", "by", "clear", "destroy", "down", "each", "else",
		"every", "for", "forever", "from", "go", "held", "hide", "if", "in", "is", "key", "left",
		"message", "move", "next", "no", "not", "nothing", "of", "or", "other", "per", "play",
		"print", "released", "remember", "remove", "repeat", "repeating", "restart", "return",
		"right", "round", "scene", "second", "seconds", "self", "send", "shared", "show",
		"shuffle", "slide", "sort", "spawn", "start", "stop", "sound", "music", "times", "to",
		"touch", "turn", "up", "update", "use", "value", "var", "wait", "when", "while", "with",
		"yes", "click", "pressed",
	} {
		m[w] = true
	}
	return m
}()

// builtinSig — сигнатура встроенной функции: допустимое число аргументов, типы и результат.
// tyUnknown в args означает «любой тип».
type builtinSig struct {
	min, max int
	args     []vtype
	ret      vtype
}

var builtins = map[string]builtinSig{
	"abs":      {1, 1, []vtype{tyNum}, tyNum},
	"min":      {2, 2, []vtype{tyNum, tyNum}, tyNum},
	"max":      {2, 2, []vtype{tyNum, tyNum}, tyNum},
	"round":    {1, 1, []vtype{tyNum}, tyNum},
	"floor":    {1, 1, []vtype{tyNum}, tyNum},
	"ceil":     {1, 1, []vtype{tyNum}, tyNum},
	"sqrt":     {1, 1, []vtype{tyNum}, tyNum},
	"sin":      {1, 1, []vtype{tyNum}, tyNum},
	"cos":      {1, 1, []vtype{tyNum}, tyNum},
	"random":   {2, 2, []vtype{tyNum, tyNum}, tyNum},
	"pick":     {1, 1, []vtype{tyList}, tyUnknown},
	"upper":    {1, 1, []vtype{tyText}, tyText},
	"lower":    {1, 1, []vtype{tyText}, tyText},
	"number":   {1, 1, []vtype{tyText}, tyNum},
	"find":     {1, 1, []vtype{tyText}, tyObj},
	"all":      {1, 1, []vtype{tyText}, tyList},
	"count":    {1, 1, []vtype{tyText}, tyNum},
	"exists":   {1, 1, []vtype{tyObj}, tyBool},
	"distance": {2, 2, []vtype{tyObj, tyObj}, tyNum},
	"touching": {1, 2, []vtype{tyUnknown, tyDir}, tyBool},
	"opposite": {1, 1, []vtype{tyDir}, tyDir},
	"recall":   {2, 2, []vtype{tyText, tyUnknown}, tyUnknown},
}

var builtinNames = []string{
	"abs", "min", "max", "round", "floor", "ceil", "sqrt", "sin", "cos", "random", "pick",
	"upper", "lower", "number", "find", "all", "count", "exists", "distance", "touching",
	"opposite", "recall",
}
