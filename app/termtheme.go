package app

import (
	"math"

	"github.com/gdamore/tcell/v2"

	"bt/terminal"
)

// UseTerminalColors rebuilds the "terminal" theme out of what the terminal
// reported. The sixteen are its own; the grounds are mixed from them, so the
// status line, menu and selection sit a shade off the background in a light
// terminal as well as a dark one.
func UseTerminalColors(got terminal.Colours) {
	bg, ok := got[terminal.Background]
	if !ok {
		return
	}

	at := func(n int, fallback tcell.Color) tcell.Color {
		if c, ok := got[n]; ok {
			return c
		}

		return fallback
	}

	fg := at(terminal.Foreground, away(bg))
	sel := ground(mix(bg, at(4, fg), 0.30), bg, 1.6)
	bar := ground(mix(bg, fg, 0.12), bg, 1.35)

	for i := range themes {
		if themes[i].name != DefaultTheme {
			continue
		}

		themes[i] = theme{
			name:   DefaultTheme,
			fg:     fg,
			bg:     bg,
			muted:  readable(at(8, mix(bg, fg, 0.55)), bg, 3),
			selFg:  readable(fg, sel, 4.5),
			selBg:  sel,
			barFg:  readable(fg, bar, 4.5),
			barBg:  bar,
			menuBg: ground(mix(bg, fg, 0.10), bg, 1.35),
			red:    at(1, fg),
			green:  at(2, fg),
			yellow: at(3, fg),
			blue:   at(4, fg),
			cyan:   at(6, fg),
		}
	}
}

// WCAG 2.1 relative luminance.
func luminance(c tcell.Color) float64 {
	r, g, b := c.RGB()

	ch := func(v int32) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}

		return math.Pow((s+0.055)/1.055, 2.4)
	}

	return 0.2126*ch(r) + 0.7152*ch(g) + 0.0722*ch(b)
}

func contrast(a, b tcell.Color) float64 {
	la := luminance(a) + 0.05
	lb := luminance(b) + 0.05

	return max(la, lb) / min(la, lb)
}

func mix(a, b tcell.Color, t float64) tcell.Color {
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	f := func(x, y int32) int32 {
		return int32(float64(x) + (float64(y)-float64(x))*t)
	}

	return tcell.NewRGBColor(f(ar, br), f(ag, bg), f(ab, bb))
}

// away is the end a colour moves towards to stand off bg: white on a dark
// scheme, black on a light one.
func away(bg tcell.Color) tcell.Color {
	if luminance(bg) > 0.4 {
		return tcell.NewRGBColor(0, 0, 0)
	}

	return tcell.NewRGBColor(255, 255, 255)
}

// walk moves c towards the far end from bg until the two clear the ratio.
func walk(c, bg tcell.Color, want float64) (tcell.Color, bool) {
	target := away(bg)

	for i := 0; i <= 50; i++ {
		m := mix(c, target, float64(i)/50)
		if contrast(m, bg) >= want {
			return m, true
		}
	}

	return target, false
}

// readable is a text colour moved off its ground until it can be read.
func readable(c, bg tcell.Color, want float64) tcell.Color {
	m, _ := walk(c, bg, want)

	return m
}

// ground is a background moved off the page's until the two can be told
// apart.
func ground(c, bg tcell.Color, want float64) tcell.Color {
	m, ok := walk(c, bg, want)
	if !ok {
		return c
	}

	return m
}
