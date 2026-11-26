package app

import "github.com/gdamore/tcell/v2"

// helpView lists every key, a section to a heading, over the whole screen.
type helpView struct {
	top  int
	rows int
}

func newHelp() *helpView {
	return &helpView{
		rows: 1,
	}
}

type helpLine struct {
	heading string
	key     helpKey
}

func helpLines() []helpLine {
	var out []helpLine

	for i, sec := range helpSections {
		if i > 0 {
			out = append(out, helpLine{})
		}

		out = append(out, helpLine{
			heading: sec.title,
		})

		for _, k := range sec.keys {
			out = append(out, helpLine{
				key: k,
			})
		}
	}

	return out
}

func (h *helpView) scroll(n int) {
	h.top = max(0, min(h.top+n, len(helpLines())-h.rows))
}

// key handles a key and reports whether the help should close.
func (h *helpView) key(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC, tcell.KeyF1:
		return true
	case tcell.KeyDown:
		h.scroll(1)
	case tcell.KeyUp:
		h.scroll(-1)
	case tcell.KeyCtrlD:
		h.scroll(h.rows / 2)
	case tcell.KeyCtrlU:
		h.scroll(-h.rows / 2)
	case tcell.KeyPgDn:
		h.scroll(h.rows)
	case tcell.KeyPgUp:
		h.scroll(-h.rows)
	case tcell.KeyHome:
		h.top = 0
	case tcell.KeyEnd:
		h.scroll(1 << 20)
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', '?':
			return true
		case 'j':
			h.scroll(1)
		case 'k':
			h.scroll(-1)
		case 'G':
			h.scroll(1 << 20)
		}
	}

	return false
}

func (h *helpView) draw(cv canvas) {
	in := cv.box(strHelp, "", true)
	in = in.sub(2, 0, in.w-4, in.h)
	h.rows = in.h
	lines := helpLines()
	h.top = max(0, min(h.top, len(lines)-h.rows))

	keyW := 0

	for _, l := range lines {
		keyW = max(keyW, width(l.key.keys))
	}

	for y := range in.h {
		i := h.top + y
		if i >= len(lines) {
			break
		}

		l := lines[i]
		if l.heading != "" {
			in.text(0, y, l.heading, stAccent)
			continue
		}

		in.textMax(2, y, keyW, l.key.keys, stLabel)
		in.text(2+keyW+3, y, l.key.what, stNormal)
	}
}
