package studio

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/vide2d/vide-studio/internal/clip"
	"github.com/vide2d/vide-studio/internal/codeedit"
	"github.com/vide2d/vide-studio/internal/project"
	"github.com/vide2d/vide-studio/internal/scene"
	"github.com/vide2d/vide-studio/internal/tdsl"
	"github.com/vide2d/vide-studio/internal/ui"
)

const (
	codeLineH    = 18
	codeGutterW  = 46
	codeBarH     = 30
	codeRowH     = 20
	codeMaxRows  = 5
	codeMaxPopup = 8
	diagDelay    = 6 // frames after the last edit before re-checking
)

// code is the state of the VCode panel: one open script at a time.
type code struct {
	rel      string // open script, relative to the scripts folder; "" if none
	buf      *codeedit.Buffer
	saved    int  // buffer version at the last save/load
	readonly bool // the file did not fit the editor limits; saving would lose data

	focus            bool
	scrollX, scrollY int
	follow           bool // scroll to keep the cursor visible this frame
	drag             bool
	frame            int
	lastClickFrame   int
	lastClickPos     image.Point

	diag    []*tdsl.Error
	diagVer int
	env     *tdsl.Env
	cenv    *tdsl.CompleteEnv

	popup struct {
		items []tdsl.Completion
		sel   int
	}
}

func (c *code) dirty() bool { return c.buf != nil && c.buf.Version() != c.saved }

// openInCode opens a script in the VCode panel and brings the panel forward.
// A modified script that is open at the time is saved first.
func (s *Studio) openInCode(rel string) {
	c := &s.code
	if c.rel == rel && c.buf != nil {
		s.dock.Root.Activate(panelCode)
		c.focus = true
		return
	}
	file := s.scriptFile(rel)
	if file == "" {
		s.Logf("Cannot open %q: not a valid script path.", rel)
		return
	}
	st, err := os.Lstat(file)
	if err != nil || !st.Mode().IsRegular() {
		s.Logf("Cannot open %s: the file does not exist (use New in the Inspector).", rel)
		return
	}
	if st.Size() > codeedit.MaxBytes {
		s.Logf("Cannot open %s: the file is larger than %d KB.", rel, codeedit.MaxBytes/1024)
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		s.Logf("Cannot open %s: %v", rel, err)
		return
	}
	if c.dirty() {
		s.saveCode()
	}
	buf := codeedit.New(string(data))
	*c = code{rel: rel, buf: buf, saved: buf.Version(), focus: true, readonly: buf.Truncated(), follow: true}
	if c.readonly {
		s.Logf("%s is too large for the editor and was cut; saving is disabled so nothing is lost.", rel)
	}
	s.refreshCodeEnv()
	s.runDiag()
	s.dock.Root.Activate(panelCode)
}

func (s *Studio) saveCode() {
	c := &s.code
	if c.buf == nil {
		return
	}
	if c.readonly {
		s.Logf("Saving %s is disabled (the file was cut to fit the editor).", c.rel)
		return
	}
	file := s.scriptFile(c.rel)
	if file == "" {
		s.Logf("Cannot save: bad script path.")
		return
	}
	if err := project.WriteAtomic(file, []byte(c.buf.Text())); err != nil {
		s.Logf("Save failed: %v", err)
		return
	}
	c.saved = c.buf.Version()
	s.Logf("Saved scripts/%s", c.rel)
	s.refreshCodeEnv()
}

// refreshCodeEnv rebuilds what the checker and the completer know about the project.
func (s *Studio) refreshCodeEnv() {
	c := &s.code
	env := &tdsl.Env{Objects: []string{}, Templates: []string{}, SharedDeclared: map[string]string{}}
	tags := map[string]bool{}
	s.scene.Root.Walk(func(n *scene.Node, _ int) {
		env.Objects = append(env.Objects, n.Name)
		if n.Tag != "" {
			tags[n.Tag] = true
		}
		if n.Template {
			env.Templates = append(env.Templates, n.Name)
		}
	})
	for t := range tags {
		env.Tags = append(env.Tags, t)
	}
	sort.Strings(env.Tags)
	scripts := s.listScripts()
	env.Scripts = scripts
	for _, name := range scripts {
		if name == c.rel {
			continue
		}
		data, err := os.ReadFile(s.scriptFile(name))
		if err != nil || len(data) > codeedit.MaxBytes {
			continue
		}
		sc, _ := tdsl.Parse(string(data))
		if sc == nil {
			continue
		}
		for _, it := range sc.Items {
			if sh, ok := it.(*tdsl.SharedDecl); ok {
				env.SharedDeclared[sh.Name] = name
			}
		}
	}
	var images []string
	for _, a := range s.assets.list {
		images = append(images, a.Path)
	}
	c.env = env
	c.cenv = &tdsl.CompleteEnv{Objects: env.Objects, Tags: env.Tags, Assets: images, Scripts: scripts}
}

// listScripts lists the .tdsl files in the scripts folder (recursively), as
// slash-separated relative paths.
func (s *Studio) listScripts() []string {
	var out []string
	var walk func(dir, prefix string, depth int)
	walk = func(dir, prefix string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil || depth > 4 || len(out) > 2000 {
			return
		}
		for _, e := range entries {
			switch {
			case e.IsDir():
				walk(dir+string(os.PathSeparator)+e.Name(), prefix+e.Name()+"/", depth+1)
			case e.Type().IsRegular() && strings.EqualFold(path.Ext(e.Name()), scriptExt):
				out = append(out, prefix+e.Name())
			}
		}
	}
	if s.scriptsDir != "" {
		walk(s.scriptsDir, "", 0)
	}
	sort.Strings(out)
	return out
}

func (s *Studio) runDiag() {
	c := &s.code
	sc, errs := tdsl.Parse(c.buf.Text())
	if len(errs) == 0 {
		errs = tdsl.Check(sc, c.env)
	}
	c.diag, c.diagVer = errs, c.buf.Version()
}

// ---- layout ----

type codeLayout struct {
	bar, gutter, text, problems image.Rectangle
	rows                        int // visible problem rows
}

func (s *Studio) codeLayout(r image.Rectangle) codeLayout {
	var l codeLayout
	rows := min(len(s.code.diag), codeMaxRows)
	ph := 0
	if rows > 0 {
		ph = rows*codeRowH + 6
	}
	l.rows = rows
	l.bar = image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+codeBarH)
	l.problems = image.Rect(r.Min.X, r.Max.Y-ph, r.Max.X, r.Max.Y)
	body := image.Rect(r.Min.X, l.bar.Max.Y, r.Max.X, l.problems.Min.Y)
	l.gutter = image.Rect(body.Min.X, body.Min.Y, body.Min.X+codeGutterW, body.Max.Y)
	l.text = image.Rect(l.gutter.Max.X, body.Min.Y, body.Max.X, body.Max.Y)
	return l
}

func (s *Studio) codeTextX(l codeLayout) int { return l.text.Min.X + 8 - s.code.scrollX }

// posAt maps a screen point to a buffer position.
func (s *Studio) codePosAt(l codeLayout, x, y int) (line, col int) {
	c := &s.code
	line = (y - l.text.Min.Y + c.scrollY) / codeLineH
	if y < l.text.Min.Y {
		line = -1
	}
	line = min(max(line, 0), c.buf.Lines()-1)
	col = int(math.Round(float64(x-s.codeTextX(l)) / s.ui.MonoAdv))
	col = min(max(col, 0), c.buf.LineLen(line))
	return
}

// ---- panel ----

func (s *Studio) codePanel(r image.Rectangle) {
	u, c := s.ui, &s.code
	c.frame++
	if c.buf == nil {
		u.Text(image.Rect(r.Min.X+10, r.Min.Y+8, r.Max.X, r.Min.Y+8+ui.LineHeight),
			"No script open. Select an object, then use Script → New or Open in the Inspector.", u.Theme.TextDim)
		c.focus = false
		return
	}
	l := s.codeLayout(r)

	// top bar
	name := c.rel
	if c.dirty() {
		name += " *"
	}
	u.Fill(l.bar, u.Theme.PanelHead)
	u.Text(image.Rect(l.bar.Min.X+10, l.bar.Min.Y, l.bar.Max.X-200, l.bar.Max.Y), name, u.Theme.Text)
	status, scol := "no problems", color.RGBA{0x5c, 0xc9, 0x6b, 0xff}
	if n := len(c.diag); n > 0 {
		status, scol = fmt.Sprintf("%d problem(s)", n), color.RGBA{0xff, 0x6b, 0x6b, 0xff}
	}
	u.Text(image.Rect(l.bar.Max.X-300, l.bar.Min.Y, l.bar.Max.X-86, l.bar.Max.Y), status, scol)
	if u.Button(image.Rect(l.bar.Max.X-76, l.bar.Min.Y+3, l.bar.Max.X-6, l.bar.Max.Y-3), "Save", c.dirty() && !c.readonly) {
		s.saveCode()
	}

	// focus and mouse
	hoverText := u.Hover(l.text)
	if u.Pressed {
		c.focus = hoverText
	}
	if hoverText && u.Wheel != 0 {
		if u.Shift {
			c.scrollX -= int(u.Wheel * 40)
		} else {
			c.scrollY -= int(u.Wheel * codeLineH * 3)
		}
	}
	s.codeMouse(l, hoverText)
	if c.focus {
		s.codeKeys(l)
	}
	u.External = c.focus

	// diagnostics (debounced)
	if c.buf.Version() != c.diagVer && c.frame%diagDelay == 0 {
		s.runDiag()
	}
	l = s.codeLayout(r) // the number of problems may have changed above

	// problems list
	for i := 0; i < l.rows && i < len(c.diag); i++ {
		row := image.Rect(l.problems.Min.X, l.problems.Min.Y+3+i*codeRowH, l.problems.Max.X, l.problems.Min.Y+3+(i+1)*codeRowH)
		e := c.diag[i]
		if u.Hover(row) {
			u.Fill(row, u.Theme.PanelHead)
			if u.Pressed {
				c.buf.SetCursor(e.Line-1, e.Col-1, false)
				c.focus, c.follow = true, true
			}
		}
		msg := fmt.Sprintf("%d:%d  %s  %s", e.Line, e.Col, e.Code, e.Message)
		if e.Hint != "" {
			msg += "  — " + e.Hint
		}
		u.Text(image.Rect(row.Min.X+10, row.Min.Y, row.Max.X, row.Max.Y), u.FitText(msg, float64(row.Dx()-20)), color.RGBA{0xff, 0x9a, 0x9a, 0xff})
	}
	if l.rows > 0 {
		u.Fill(image.Rect(l.problems.Min.X, l.problems.Min.Y, l.problems.Max.X, l.problems.Min.Y+1), u.Theme.Border)
	}

	// scroll limits and follow-cursor
	viewH, viewW := l.text.Dy(), l.text.Dx()-16
	cur := c.buf.Cursor()
	if c.follow {
		top, bottom := cur.Line*codeLineH, (cur.Line+1)*codeLineH
		if top < c.scrollY {
			c.scrollY = top
		}
		if bottom > c.scrollY+viewH {
			c.scrollY = bottom - viewH
		}
		cx := int(float64(cur.Col) * u.MonoAdv)
		if cx < c.scrollX {
			c.scrollX = max(cx-20, 0)
		}
		if cx > c.scrollX+viewW-20 {
			c.scrollX = cx - viewW + 40
		}
		c.follow = false
	}
	c.scrollY = min(max(c.scrollY, 0), max(0, c.buf.Lines()*codeLineH-viewH+codeLineH*2))
	c.scrollX = max(c.scrollX, 0)

	s.drawCode(l)
}

// ---- mouse ----

func (s *Studio) codeMouse(l codeLayout, hoverText bool) {
	u, c := s.ui, &s.code
	if u.Pressed && hoverText {
		line, col := s.codePosAt(l, u.MX, u.MY)
		pt := image.Pt(line, col)
		if c.frame-c.lastClickFrame < 20 && c.lastClickPos == pt {
			c.buf.SelectWord(line, col)
		} else {
			c.buf.SetCursor(line, col, u.Shift)
			c.drag = true
		}
		c.lastClickFrame, c.lastClickPos = c.frame, pt
		c.popup.items = nil
		c.follow = true
	}
	if c.drag {
		if u.Down {
			line, col := s.codePosAt(l, u.MX, u.MY)
			c.buf.SetCursor(line, col, true)
			c.follow = true
		} else {
			c.drag = false
		}
	}
}

// ---- keyboard ----

func (s *Studio) codeKeys(l codeLayout) {
	c := &s.code
	b := c.buf
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl) || ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)
	just := inpututil.IsKeyJustPressed
	prevLine := b.Cursor().Line
	typed := false
	acted := false
	act := func(f func()) { f(); acted = true }

	// completion popup keys come first
	if len(c.popup.items) > 0 {
		switch {
		case ui.KeyRepeat(ebiten.KeyArrowDown):
			c.popup.sel = (c.popup.sel + 1) % len(c.popup.items)
			return
		case ui.KeyRepeat(ebiten.KeyArrowUp):
			c.popup.sel = (c.popup.sel + len(c.popup.items) - 1) % len(c.popup.items)
			return
		case just(ebiten.KeyEnter), just(ebiten.KeyNumpadEnter), just(ebiten.KeyTab):
			s.acceptCompletion()
			c.follow = true
			return
		case just(ebiten.KeyEscape):
			c.popup.items = nil
			return
		}
	}

	if !ctrl && !alt {
		for _, r := range ebiten.AppendInputChars(nil) {
			if unicode.IsControl(r) {
				continue
			}
			b.Insert(string(r))
			typed, acted = true, true
		}
	}

	switch {
	case just(ebiten.KeyEnter), just(ebiten.KeyNumpadEnter):
		act(b.Enter)
	case ui.KeyRepeat(ebiten.KeyBackspace):
		if ctrl {
			act(b.DeleteWordBack)
		} else {
			act(b.Backspace)
		}
	case ui.KeyRepeat(ebiten.KeyDelete):
		if ctrl {
			act(b.DeleteWordForward)
		} else {
			act(b.Delete)
		}
	case ui.KeyRepeat(ebiten.KeyArrowLeft):
		if ctrl {
			act(func() { b.MoveWordLeft(shift) })
		} else {
			act(func() { b.MoveLeft(shift) })
		}
	case ui.KeyRepeat(ebiten.KeyArrowRight):
		if ctrl {
			act(func() { b.MoveWordRight(shift) })
		} else {
			act(func() { b.MoveRight(shift) })
		}
	case ui.KeyRepeat(ebiten.KeyArrowUp):
		if alt {
			act(b.MoveLinesUp)
		} else {
			act(func() { b.MoveUp(shift) })
		}
	case ui.KeyRepeat(ebiten.KeyArrowDown):
		if alt {
			act(b.MoveLinesDown)
		} else {
			act(func() { b.MoveDown(shift) })
		}
	case just(ebiten.KeyHome):
		if ctrl {
			act(func() { b.MoveTextStart(shift) })
		} else {
			act(func() { b.MoveHome(shift) })
		}
	case just(ebiten.KeyEnd):
		if ctrl {
			act(func() { b.MoveTextEnd(shift) })
		} else {
			act(func() { b.MoveEnd(shift) })
		}
	case ui.KeyRepeat(ebiten.KeyPageUp):
		act(func() { b.MovePageUp(l.text.Dy()/codeLineH-1, shift) })
	case ui.KeyRepeat(ebiten.KeyPageDown):
		act(func() { b.MovePageDown(l.text.Dy()/codeLineH-1, shift) })
	case just(ebiten.KeyTab):
		if shift {
			act(b.ShiftTab)
		} else {
			act(b.Tab)
		}
	case ctrl && just(ebiten.KeyA):
		act(b.SelectAll)
	case ctrl && just(ebiten.KeyC):
		if t := b.Copy(); t != "" {
			clip.Set(t)
		}
	case ctrl && just(ebiten.KeyX):
		if t := b.Cut(); t != "" {
			clip.Set(t)
			acted = true
		}
	case ctrl && just(ebiten.KeyV):
		act(func() { b.Paste(clip.Get()) })
	case ctrl && !shift && just(ebiten.KeyZ):
		act(func() { b.Undo() })
	case ctrl && (just(ebiten.KeyY) || (shift && just(ebiten.KeyZ))):
		act(func() { b.Redo() })
	case ctrl && shift && just(ebiten.KeyK):
		act(b.DeleteLines)
	case ctrl && just(ebiten.KeyD):
		act(b.DuplicateLines)
	case ctrl && just(ebiten.KeySlash):
		act(b.ToggleComment)
	case ctrl && just(ebiten.KeyS):
		s.saveCode()
	case ctrl && just(ebiten.KeySpace):
		s.openCompletion()
	case just(ebiten.KeyEscape):
		c.focus = false
	}
	if acted {
		c.follow = true
	}
	if b.Cursor().Line != prevLine && !typed {
		c.popup.items = nil
	}
	if typed {
		cur := b.Cursor()
		line := []rune(b.Line(cur.Line))
		if cur.Col > 0 && cur.Col <= len(line) {
			if ch := line[cur.Col-1]; unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' || ch == '.' || ch == '"' {
				s.openCompletion()
				return
			}
		}
		c.popup.items = nil
	}
}

func (s *Studio) openCompletion() {
	c := &s.code
	cur := c.buf.Cursor()
	items := tdsl.Complete(c.buf.Text(), cur.Line, cur.Col, c.cenv)
	if len(items) > codeMaxPopup*3 {
		items = items[:codeMaxPopup*3]
	}
	c.popup.items, c.popup.sel = items, 0
}

func (s *Studio) acceptCompletion() {
	c := &s.code
	if c.popup.sel >= len(c.popup.items) {
		c.popup.items = nil
		return
	}
	item := c.popup.items[c.popup.sel]
	c.popup.items = nil
	cur := c.buf.Cursor()
	line := []rune(c.buf.Line(cur.Line))
	start := cur.Col
	for start > 0 && start <= len(line) {
		ch := line[start-1]
		if !(unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_') {
			break
		}
		start--
	}
	c.buf.SetSelection(codeedit.Pos{Line: cur.Line, Col: start}, cur)
	c.buf.Insert(item.Insert)
}

// ---- drawing ----

var kindColors = map[tdsl.HighlightKind]color.RGBA{
	tdsl.KindKeyword:   {0xc5, 0x86, 0xc0, 0xff},
	tdsl.KindCommand:   {0x4f, 0x9d, 0xff, 0xff},
	tdsl.KindEvent:     {0xe5, 0xa2, 0x4b, 0xff},
	tdsl.KindBuiltin:   {0xdc, 0xdc, 0x8b, 0xff},
	tdsl.KindProperty:  {0x9c, 0xdc, 0xfe, 0xff},
	tdsl.KindDirection: {0x4e, 0xc9, 0xb0, 0xff},
	tdsl.KindLiteral:   {0x56, 0x9c, 0xd6, 0xff},
	tdsl.KindSelfWord:  {0xff, 0x8c, 0x69, 0xff},
	tdsl.KindNumber:    {0xb5, 0xce, 0xa8, 0xff},
	tdsl.KindText:      {0xce, 0x91, 0x78, 0xff},
	tdsl.KindComment:   {0x6a, 0x99, 0x55, 0xff},
	tdsl.KindOperator:  {0xd4, 0xd4, 0xd4, 0xff},
	tdsl.KindPunct:     {0x8b, 0x92, 0xa3, 0xff},
	tdsl.KindName:      {0xe2, 0xe5, 0xec, 0xff},
	tdsl.KindError:     {0xff, 0x6b, 0x6b, 0xff},
}

type codeLine struct {
	n     int
	text  []rune
	spans []tdsl.Span
}

func (s *Studio) drawCode(l codeLayout) {
	u, c := s.ui, &s.code
	b := c.buf
	adv := u.MonoAdv
	first := c.scrollY / codeLineH
	last := min(b.Lines()-1, (c.scrollY+l.text.Dy())/codeLineH)
	var lines []codeLine
	for i := first; i <= last; i++ {
		t := b.Line(i)
		lines = append(lines, codeLine{n: i, text: []rune(t), spans: tdsl.HighlightLine(t)})
	}
	cur := b.Cursor()
	selStart, selEnd, hasSel := b.Selection()
	errLines := map[int]bool{}
	for _, e := range c.diag {
		errLines[e.Line-1] = true
	}
	diag := c.diag
	scrollY, originX := c.scrollY, s.codeTextX(l)
	blink := c.focus && (c.frame/30)%2 == 0
	var pop []tdsl.Completion
	popSel := c.popup.sel
	if len(c.popup.items) > 0 {
		pop = c.popup.items
		if len(pop) > codeMaxPopup {
			start := min(max(popSel-codeMaxPopup+1, 0), len(pop)-codeMaxPopup)
			pop = pop[start : start+codeMaxPopup]
			popSel -= start
		}
	}
	focus := c.focus
	u.Emit(func(d *ebiten.Image) {
		t := u.Theme
		ui.FillRect(d, l.text, color.RGBA{0x1b, 0x1d, 0x23, 0xff})
		ui.FillRect(d, l.gutter, color.RGBA{0x20, 0x22, 0x28, 0xff})
		txt := d.SubImage(l.text).(*ebiten.Image)
		gut := d.SubImage(l.gutter).(*ebiten.Image)
		for _, ln := range lines {
			y := l.text.Min.Y + ln.n*codeLineH - scrollY
			row := image.Rect(l.text.Min.X, y, l.text.Max.X, y+codeLineH)
			if ln.n == cur.Line && focus {
				ui.FillRect(txt, row, color.RGBA{0x22, 0x25, 0x2d, 0xff})
			}
			if hasSel && ln.n >= selStart.Line && ln.n <= selEnd.Line {
				from, to := 0, len(ln.text)+1
				if ln.n == selStart.Line {
					from = selStart.Col
				}
				if ln.n == selEnd.Line {
					to = selEnd.Col
				}
				if to > from {
					ui.FillRect(txt, image.Rect(originX+int(float64(from)*adv), y, originX+int(float64(to)*adv), y+codeLineH), color.RGBA{0x2f, 0x4a, 0x6e, 0xff})
				}
			}
			for _, sp := range ln.spans {
				col := kindColors[sp.Kind]
				u.DrawMono(txt, string(ln.text[sp.Start:sp.End]), float64(originX)+float64(sp.Start)*adv, float64(y+2), col)
			}
			numCol := t.TextDim
			if errLines[ln.n] {
				numCol = color.RGBA{0xff, 0x6b, 0x6b, 0xff}
			}
			num := fmt.Sprint(ln.n + 1)
			u.DrawMono(gut, num, float64(l.gutter.Max.X)-8-float64(len(num))*adv, float64(y+2), numCol)
		}
		for _, e := range diag {
			if e.Line-1 < first || e.Line-1 > last {
				continue
			}
			y := l.text.Min.Y + (e.Line-1)*codeLineH - scrollY + codeLineH - 3
			x1 := originX + int(float64(e.Col-1)*adv)
			x2 := originX + int(float64(max(e.EndCol, e.Col+1)-1)*adv)
			ui.FillRect(txt, image.Rect(x1, y, x2, y+2), color.RGBA{0xff, 0x6b, 0x6b, 0xff})
		}
		if blink && cur.Line >= first && cur.Line <= last {
			y := l.text.Min.Y + cur.Line*codeLineH - scrollY
			x := originX + int(float64(cur.Col)*adv)
			ui.FillRect(txt, image.Rect(x, y+1, x+2, y+codeLineH-1), t.Text)
		}
		if len(pop) > 0 && cur.Line >= first && cur.Line <= last {
			w := 0
			for _, p := range pop {
				w = max(w, len([]rune(p.Label))+len([]rune(p.Detail))+4)
			}
			pw := min(int(float64(w)*adv)+16, l.text.Dx()-8)
			x := min(originX+int(float64(cur.Col)*adv), l.text.Max.X-pw-4)
			y := l.text.Min.Y + (cur.Line+1)*codeLineH - scrollY + 2
			ph := len(pop) * codeLineH
			if y+ph > l.text.Max.Y {
				y = l.text.Min.Y + cur.Line*codeLineH - scrollY - ph - 2
			}
			box := image.Rect(x, y, x+pw, y+ph)
			ui.FillRect(txt, box, color.RGBA{0x2e, 0x32, 0x3c, 0xff})
			ui.StrokeRect(txt, box, t.Accent)
			for i, p := range pop {
				ry := y + i*codeLineH
				if i == popSel {
					ui.FillRect(txt, image.Rect(x+1, ry, x+pw-1, ry+codeLineH), color.RGBA{0x2f, 0x4a, 0x6e, 0xff})
				}
				u.DrawMono(txt, p.Label, float64(x+6), float64(ry+2), t.Text)
				if p.Detail != "" {
					u.DrawMono(txt, p.Detail, float64(x+6)+float64(len([]rune(p.Label))+2)*adv, float64(ry+2), t.TextDim)
				}
			}
		}
	})
}
