package app

import "github.com/gdamore/tcell/v2"

// A theme is the colours the styles are built from. An unset colour is the
// terminal's own: the terminal theme sets none of the ground colours, so the
// program looks like the terminal it runs in.
type theme struct {
	name string

	fg, bg       tcell.Color
	muted        tcell.Color
	selFg, selBg tcell.Color
	barFg, barBg tcell.Color
	menuBg       tcell.Color

	red, green, yellow, blue, cyan tcell.Color
}

const DefaultTheme = "terminal"

func hex(v int32) tcell.Color { return tcell.NewHexColor(v) }

var themes = []theme{
	{
		name:   "terminal",
		menuBg: tcell.PaletteColor(8),
		red:    tcell.PaletteColor(1),
		green:  tcell.PaletteColor(2),
		yellow: tcell.PaletteColor(3),
		blue:   tcell.PaletteColor(4),
		cyan:   tcell.PaletteColor(6),
	},
	{
		name:   "dark",
		fg:     hex(0xd4d4d4),
		bg:     hex(0x1e1e1e),
		muted:  hex(0x808080),
		selFg:  hex(0xffffff),
		selBg:  hex(0x264f78),
		barFg:  hex(0xcccccc),
		barBg:  hex(0x333333),
		menuBg: hex(0x2d2d2d),
		red:    hex(0xf14c4c),
		green:  hex(0x89d185),
		yellow: hex(0xe5c07b),
		blue:   hex(0x61afef),
		cyan:   hex(0x56b6c2),
	},
	{
		name:   "light",
		fg:     hex(0x1f2328),
		bg:     hex(0xffffff),
		muted:  hex(0x8c959f),
		selFg:  hex(0xffffff),
		selBg:  hex(0x0969da),
		barFg:  hex(0x1f2328),
		barBg:  hex(0xeaeef2),
		menuBg: hex(0xf6f8fa),
		red:    hex(0xcf222e),
		green:  hex(0x1a7f37),
		yellow: hex(0x9a6700),
		blue:   hex(0x0969da),
		cyan:   hex(0x1b7c83),
	},
	{
		name:   "solarized-dark",
		fg:     hex(0x839496),
		bg:     hex(0x002b36),
		muted:  hex(0x586e75),
		selFg:  hex(0xfdf6e3),
		selBg:  hex(0x586e75),
		barFg:  hex(0x93a1a1),
		barBg:  hex(0x073642),
		menuBg: hex(0x073642),
		red:    hex(0xdc322f),
		green:  hex(0x859900),
		yellow: hex(0xb58900),
		blue:   hex(0x268bd2),
		cyan:   hex(0x2aa198),
	},
	{
		name:   "solarized-light",
		fg:     hex(0x657b83),
		bg:     hex(0xfdf6e3),
		muted:  hex(0x93a1a1),
		selFg:  hex(0xfdf6e3),
		selBg:  hex(0x657b83),
		barFg:  hex(0x586e75),
		barBg:  hex(0xeee8d5),
		menuBg: hex(0xeee8d5),
		red:    hex(0xdc322f),
		green:  hex(0x859900),
		yellow: hex(0xb58900),
		blue:   hex(0x268bd2),
		cyan:   hex(0x2aa198),
	},
	{
		name:   "gruvbox",
		fg:     hex(0xebdbb2),
		bg:     hex(0x282828),
		muted:  hex(0x928374),
		selFg:  hex(0xfbf1c7),
		selBg:  hex(0x504945),
		barFg:  hex(0xebdbb2),
		barBg:  hex(0x3c3836),
		menuBg: hex(0x3c3836),
		red:    hex(0xfb4934),
		green:  hex(0xb8bb26),
		yellow: hex(0xfabd2f),
		blue:   hex(0x83a598),
		cyan:   hex(0x8ec07c),
	},
	{
		name:   "nord",
		fg:     hex(0xd8dee9),
		bg:     hex(0x2e3440),
		muted:  hex(0x616e88),
		selFg:  hex(0xeceff4),
		selBg:  hex(0x434c5e),
		barFg:  hex(0xd8dee9),
		barBg:  hex(0x3b4252),
		menuBg: hex(0x3b4252),
		red:    hex(0xbf616a),
		green:  hex(0xa3be8c),
		yellow: hex(0xebcb8b),
		blue:   hex(0x81a1c1),
		cyan:   hex(0x88c0d0),
	},
}

// ThemeNames is every built-in theme, for the config file to check against.
func ThemeNames() []string {
	names := make([]string, len(themes))

	for i, t := range themes {
		names[i] = t.name
	}

	return names
}

func themeByName(name string) (theme, bool) {
	for _, t := range themes {
		if t.name == name {
			return t, true
		}
	}

	return theme{}, false
}

func set(c tcell.Color) bool { return c != tcell.ColorDefault }

// The styles everything is drawn in, built from the theme by apply.
var (
	stNormal   tcell.Style
	stDim      tcell.Style
	stBold     tcell.Style
	stLabel    tcell.Style
	stAccent   tcell.Style
	stError    tcell.Style
	stOK       tcell.Style
	stWarn     tcell.Style
	stInfo     tcell.Style
	stBorder   tcell.Style
	stSelected tcell.Style
	stStatus   tcell.Style
	stMenu     tcell.Style
	stBarDone  tcell.Style
	stBarLeft  tcell.Style
)

// apply rebuilds every style from the theme's colours.
func (t theme) apply() {
	base := tcell.StyleDefault.Foreground(t.fg).Background(t.bg)

	stNormal = base
	stBold = base.Bold(true)
	stLabel = base.Foreground(t.cyan)
	stAccent = base.Foreground(t.blue).Bold(true)
	stError = base.Foreground(t.red)
	stOK = base.Foreground(t.green)
	stWarn = base.Foreground(t.yellow)
	stInfo = base.Foreground(t.blue)
	stMenu = base.Background(t.menuBg)
	stBarDone = base.Foreground(t.green)

	stDim = base.Dim(true)

	if set(t.muted) {
		stDim = base.Foreground(t.muted)
	}

	stBorder = stDim
	stBarLeft = stDim

	stSelected = base.Reverse(true)

	if set(t.selBg) {
		stSelected = tcell.StyleDefault.Foreground(t.selFg).Background(t.selBg)
	}

	stStatus = base.Reverse(true)

	if set(t.barBg) {
		stStatus = tcell.StyleDefault.Foreground(t.barFg).Background(t.barBg)
	}
}
