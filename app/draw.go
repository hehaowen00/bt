package app

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// A canvas is a rectangle of the screen, so a pane draws in its own
// coordinates and cannot spill into the next.
type canvas struct {
	s          tcell.Screen
	x, y, w, h int
}

func (c canvas) sub(x, y, w, h int) canvas {
	w = max(0, min(w, c.w-x))
	h = max(0, min(h, c.h-y))

	return canvas{
		s: c.s,
		x: c.x + x,
		y: c.y + y,
		w: w,
		h: h,
	}
}

func (c canvas) fill(st tcell.Style) {
	for y := range c.h {
		c.fillRow(0, y, st)
	}
}

// fillRow paints one row from x to the right edge.
func (c canvas) fillRow(x, y int, st tcell.Style) {
	if y < 0 || y >= c.h {
		return
	}

	for ; x < c.w; x++ {
		c.s.SetContent(c.x+x, c.y+y, ' ', nil, st)
	}
}

// text writes s at x, y, cut to the canvas with an ellipsis where it does not
// fit, and answers with the cells used.
func (c canvas) text(x, y int, s string, st tcell.Style) int {
	return c.textMax(x, y, c.w-x, s, st)
}

// textMax is text cut to at most w cells.
func (c canvas) textMax(x, y, w int, s string, st tcell.Style) int {
	if y < 0 || y >= c.h || x >= c.w || w <= 0 {
		return 0
	}

	w = min(w, c.w-x)
	s = truncate(s, w)

	used := 0
	state := -1
	rest := s

	for rest != "" {
		var (
			cluster string
			cw      int
		)

		cluster, rest, cw, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if cluster == "\t" {
			cluster = " "
			cw = 1
		}

		runes := []rune(cluster)
		c.s.SetContent(c.x+x+used, c.y+y, runes[0], runes[1:], st)
		used += cw
	}

	return used
}

// right writes s ending at the right edge of w cells starting at x.
func (c canvas) right(x, y, w int, s string, st tcell.Style) {
	s = truncate(s, w)
	c.textMax(x+w-width(s), y, w, s, st)
}

func width(s string) int { return uniseg.StringWidth(s) }

// truncate cuts s to w cells, ending in an ellipsis if anything was cut.
func truncate(s string, w int) string {
	if width(s) <= w {
		return s
	}

	if w <= 0 {
		return ""
	}

	var b strings.Builder

	used := 0
	state := -1
	rest := s

	for rest != "" {
		var (
			cluster string
			cw      int
		)

		cluster, rest, cw, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if used+cw > w-1 {
			break
		}

		b.WriteString(cluster)
		used += cw
	}

	return b.String() + "…"
}

// box draws a rounded border with a title in the top edge, and answers with
// the canvas inside it.
func (c canvas) box(title, right string, focused bool) canvas {
	if c.w < 2 || c.h < 2 {
		return c.sub(0, 0, 0, 0)
	}

	st := stBorder
	titleSt := stBold

	if focused {
		st = stInfo
		titleSt = stAccent
	}

	for x := 1; x < c.w-1; x++ {
		c.s.SetContent(c.x+x, c.y, '─', nil, st)
		c.s.SetContent(c.x+x, c.y+c.h-1, '─', nil, st)
	}

	for y := 1; y < c.h-1; y++ {
		c.s.SetContent(c.x, c.y+y, '│', nil, st)
		c.s.SetContent(c.x+c.w-1, c.y+y, '│', nil, st)
	}

	c.s.SetContent(c.x, c.y, '╭', nil, st)
	c.s.SetContent(c.x+c.w-1, c.y, '╮', nil, st)
	c.s.SetContent(c.x, c.y+c.h-1, '╰', nil, st)
	c.s.SetContent(c.x+c.w-1, c.y+c.h-1, '╯', nil, st)

	used := 0

	if title != "" {
		used = c.textMax(2, 0, c.w-4, " "+title+" ", titleSt)
	}

	if right != "" {
		rw := width(right) + 2
		if 2+used+1+rw < c.w-2 {
			c.text(c.w-2-rw, 0, " "+right+" ", stDim)
		}
	}

	return c.sub(1, 1, c.w-2, c.h-2)
}

// bar draws a progress bar of w cells.
func (c canvas) bar(x, y, w int, frac float64, st tcell.Style) {
	frac = max(0, min(1, frac))
	full := int(frac * float64(w) * 8)

	for i := range w {
		n := min(8, max(0, full-i*8))

		switch n {
		case 8:
			c.s.SetContent(c.x+x+i, c.y+y, '█', nil, st)
		case 0:
			c.s.SetContent(c.x+x+i, c.y+y, '░', nil, stBarLeft)
		default:
			c.s.SetContent(c.x+x+i, c.y+y, []rune("▏▎▍▌▋▊▉")[n-1], nil, st)
		}
	}
}

// pieceMap draws which pieces are done, each cell standing for a run of
// pieces: full when all are done, shaded by how many otherwise.
func (c canvas) pieceMap(x, y, w int, have []bool) {
	if len(have) == 0 || w <= 0 {
		return
	}

	shades := []rune(" ░▒▓█")

	for i := range w {
		lo := i * len(have) / w
		hi := max(lo+1, (i+1)*len(have)/w)
		n := 0

		for _, h := range have[lo:hi] {
			if h {
				n++
			}
		}

		r := shades[(n*4+hi-lo-1)/(hi-lo)]
		st := stBarDone

		if n == 0 {
			r = '░'
			st = stBarLeft
		}

		c.s.SetContent(c.x+x+i, c.y+y, r, nil, st)
	}
}
