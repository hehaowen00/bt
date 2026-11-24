package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// The command line is the bottom row, as in Neovim: it reports what happened,
// errors included, and every question the program asks is typed there.

// lineEdit is text being typed.
type lineEdit struct {
	r   []rune
	cur int
}

func (l *lineEdit) String() string { return string(l.r) }

func (l *lineEdit) set(s string) {
	l.r = []rune(s)
	l.cur = len(l.r)
}

func (l *lineEdit) insert(s string) {
	ins := []rune(s)
	l.r = slices.Insert(l.r, l.cur, ins...)
	l.cur += len(ins)
}

// key applies a keystroke and reports whether it changed the text or cursor.
func (l *lineEdit) key(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyRune:
		if ev.Modifiers()&tcell.ModAlt != 0 {
			return false
		}

		l.insert(string(ev.Rune()))
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if l.cur > 0 {
			l.r = slices.Delete(l.r, l.cur-1, l.cur)
			l.cur--
		}
	case tcell.KeyDelete, tcell.KeyCtrlD:
		if l.cur < len(l.r) {
			l.r = slices.Delete(l.r, l.cur, l.cur+1)
		}
	case tcell.KeyLeft, tcell.KeyCtrlB:
		l.cur = max(0, l.cur-1)
	case tcell.KeyRight, tcell.KeyCtrlF:
		l.cur = min(len(l.r), l.cur+1)
	case tcell.KeyHome, tcell.KeyCtrlA:
		l.cur = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		l.cur = len(l.r)
	case tcell.KeyCtrlU:
		l.r = slices.Delete(l.r, 0, l.cur)
		l.cur = 0
	case tcell.KeyCtrlK:
		l.r = l.r[:l.cur]
	case tcell.KeyCtrlW:
		i := l.cur
		for i > 0 && unicode.IsSpace(l.r[i-1]) {
			i--
		}

		for i > 0 && !unicode.IsSpace(l.r[i-1]) && l.r[i-1] != '/' {
			i--
		}

		if i == l.cur && i > 0 {
			i--
		}

		l.r = slices.Delete(l.r, i, l.cur)
		l.cur = i
	default:
		return false
	}

	return true
}

type msgKind int

const (
	msgInfo msgKind = iota
	msgError
)

// A prompt is a question on the command line.
type prompt struct {
	label string
	edit  lineEdit

	// complete lists what may replace the text, for Tab.
	complete func(text string) []string
	matches  []string
	sel      int

	// pick, when set, makes the prompt a palette: the list it returns for
	// the text is always shown, and Enter submits the highlighted item.
	pick  func(text string) []string
	aside func(item string) string

	onChange func(text string)
	onSubmit func(text string)
	onCancel func()

	// confirm is a y/n question: one key answers it.
	confirm bool
}

type cmdline struct {
	msg    string
	kind   msgKind
	prompt *prompt
}

func (c *cmdline) info(format string, args ...any) {
	c.msg = fmt.Sprintf(format, args...)
	c.kind = msgInfo
}

func (c *cmdline) error(format string, args ...any) {
	c.msg = fmt.Sprintf(format, args...)
	c.kind = msgError
}

func (c *cmdline) ask(p *prompt) {
	p.sel = -1
	c.prompt = p
	c.msg = ""

	if p.pick != nil {
		c.repick()
	}
}

// confirm asks a y/n question and calls yes only on a y.
func (c *cmdline) confirm(question string, yes func()) {
	c.ask(&prompt{
		label:   question + " ",
		confirm: true,
		onSubmit: func(string) {
			yes()
		},
	})
}

// key gives a keystroke to the prompt, and reports whether it took it.
func (c *cmdline) key(ev *tcell.EventKey) bool {
	p := c.prompt
	if p == nil {
		return false
	}

	if p.confirm {
		c.prompt = nil

		if ev.Key() == tcell.KeyRune && (ev.Rune() == 'y' || ev.Rune() == 'Y') {
			p.onSubmit("y")
		} else if p.onCancel != nil {
			p.onCancel()
		}

		return true
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		c.cancel()
		return true
	case tcell.KeyEnter:
		text := strings.TrimSpace(p.edit.String())
		c.prompt = nil

		if p.pick == nil {
			p.onSubmit(text)
		} else if p.sel >= 0 {
			p.onSubmit(p.matches[p.sel])
		}

		return true
	case tcell.KeyTab, tcell.KeyDown:
		c.cycle(1)
		return true
	case tcell.KeyBacktab, tcell.KeyUp:
		c.cycle(-1)
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(p.edit.r) == 0 {
			c.cancel()
			return true
		}
	}

	if p.edit.key(ev) {
		c.edited()
	}

	return true
}

// paste puts pasted text into the prompt, as one line.
func (c *cmdline) paste(s string) {
	if c.prompt == nil || c.prompt.confirm {
		return
	}

	c.prompt.edit.insert(strings.Join(strings.Fields(s), ""))
	c.edited()
}

func (c *cmdline) edited() {
	p := c.prompt
	p.matches = nil
	p.sel = -1

	if p.pick != nil {
		c.repick()
	}

	if p.onChange != nil {
		p.onChange(p.edit.String())
	}
}

func (c *cmdline) cancel() {
	p := c.prompt
	c.prompt = nil

	if p.onCancel != nil {
		p.onCancel()
	}
}

// repick lists the palette items that match the text, the first one
// highlighted.
func (c *cmdline) repick() {
	p := c.prompt
	p.matches = p.pick(p.edit.String())
	p.sel = min(0, len(p.matches)-1)
}

// cycle steps through the completions, listing them on the first Tab, or
// moves the highlight in a palette.
func (c *cmdline) cycle(dir int) {
	p := c.prompt

	if p.pick != nil {
		if len(p.matches) > 0 {
			p.sel = (p.sel + dir + len(p.matches)) % len(p.matches)
		}

		return
	}

	if p.complete == nil {
		return
	}

	if p.matches == nil {
		p.matches = p.complete(p.edit.String())
		p.sel = -1

		if len(p.matches) == 1 {
			p.edit.set(p.matches[0])
			p.matches = nil

			return
		}

		prefix := commonPrefix(p.matches)
		if len(prefix) > len(p.edit.String()) {
			p.edit.set(prefix)
			return
		}
	}

	if len(p.matches) == 0 {
		return
	}

	p.sel = (p.sel + dir + len(p.matches)) % len(p.matches)
	p.edit.set(p.matches[p.sel])
}

func commonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}

	p := ss[0]

	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
		}
	}

	return p
}

// completePath lists folders and .torrent files that start with text.
func completePath(text string) []string {
	if strings.HasPrefix(text, "magnet:") {
		return nil
	}

	return completeEntries(text, func(name string) bool {
		return strings.HasSuffix(strings.ToLower(name), ".torrent")
	})
}

// completeAny lists folders and files that start with text.
func completeAny(text string) []string {
	return completeEntries(text, func(string) bool {
		return true
	})
}

// completeEntries lists folders, and the files keep accepts, that start
// with text.
func completeEntries(text string, keep func(name string) bool) []string {
	expanded := text

	if rest, ok := strings.CutPrefix(text, "~"); ok {
		home, _ := os.UserHomeDir()
		expanded = home + rest
	}

	dir, base := filepath.Split(expanded)
	shownDir := text[:len(text)-len(base)]

	readDir := dir
	if readDir == "" {
		readDir = "."
	}

	entries, err := os.ReadDir(readDir)
	if err != nil {
		return nil
	}

	var out []string

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, base) || strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}

		isDir := e.IsDir()

		if e.Type()&os.ModeSymlink != 0 {
			st, err := os.Stat(filepath.Join(readDir, name))
			isDir = err == nil && st.IsDir()
		}

		switch {
		case isDir:
			out = append(out, shownDir+name+"/")
		case keep(name):
			out = append(out, shownDir+name)
		}
	}

	return out
}

// completeDir lists folders that start with text.
func completeDir(text string) []string {
	return slices.DeleteFunc(completePath(text), func(s string) bool {
		return !strings.HasSuffix(s, "/")
	})
}

// draw shows the message or the prompt on the bottom row, and the
// completions above it.
func (c *cmdline) draw(cv canvas, y int) {
	cv.fillRow(0, y, stNormal)

	p := c.prompt
	if p == nil {
		st := stNormal

		if c.kind == msgError {
			st = stError
		}

		cv.text(0, y, c.msg, st)

		return
	}

	used := cv.text(0, y, p.label, stBold)
	text := p.edit.String()
	avail := cv.w - used - 1

	// keep the cursor in view: show the tail when the text is too long
	before := string(p.edit.r[:p.edit.cur])
	shift := max(0, width(before)-avail)
	shown := []rune(text)

	for shift > 0 && len(shown) > 0 {
		shift -= width(string(shown[0]))
		shown = shown[1:]
	}

	cv.textMax(used, y, avail, string(shown), stNormal)
	cv.s.ShowCursor(cv.x+used+min(width(before), avail), cv.y+y)

	if len(p.matches) > 1 || p.pick != nil && len(p.matches) > 0 {
		c.drawMenu(cv, y)
	}
}

func (c *cmdline) drawMenu(cv canvas, y int) {
	p := c.prompt
	rows := min(len(p.matches), 10, y)
	top := 0

	if p.sel >= rows {
		top = p.sel - rows + 1
	}

	for i := range rows {
		m := p.matches[top+i]
		st := stMenu

		if top+i == p.sel {
			st = stSelected
		}

		row := y - rows + i
		cv.fillRow(0, row, st)
		cv.text(1, row, m, st)

		if p.aside != nil {
			cv.right(0, row, cv.w-1, p.aside(m), st)
		}
	}
}
