package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// The file browser, ported from ix's file explorer: a folder listed over the
// whole screen, folders first, Enter going into what is on the line and -
// going up. It lists folders and .torrent files only, since a torrent is all
// it is for, and picking one hands its path back.

type browseEntry struct {
	name     string
	isDir    bool
	size     int64
	mode     fs.FileMode
	modified time.Time
	created  time.Time // zero where the system does not keep it
}

type browser struct {
	dir     string
	entries []browseEntry
	sel     int
	top     int
	rows    int
	listY   int
	pending rune
	pick    func(path string)
}

// openBrowser shows the browser at the folder from names, or where browsing
// last was, and calls pick with the .torrent file chosen.
func (a *App) openBrowser(from string, pick func(path string)) {
	b := &browser{
		pick: pick,
		rows: 1,
	}

	err := a.browseTo(b, a.browseStart(from), "")
	if err != nil {
		a.cmd.error("%v", err)
		return
	}

	a.browse = b
}

// browseStart is the folder a browse begins in: the one typed at the
// prompt, else the last one browsed, else the download folder.
func (a *App) browseStart(from string) string {
	var tries []string

	from = expandHome(strings.TrimSpace(from))
	if from != "" && !strings.HasPrefix(from, "magnet:") {
		tries = append(tries, from, filepath.Dir(from))
	}

	tries = append(tries, a.browseDir, a.cfg.DownloadDir)

	home, err := os.UserHomeDir()
	if err == nil {
		tries = append(tries, home)
	}

	for _, d := range tries {
		if d == "" {
			continue
		}

		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}

		st, err := os.Stat(abs)
		if err == nil && st.IsDir() {
			return abs
		}
	}

	return "."
}

// browseTo lists dir, with the cursor on the entry named at, or else the
// first entry below the .. line.
func (a *App) browseTo(b *browser, dir, at string) error {
	entries, err := readBrowseDir(dir, a.browseHidden)
	if err != nil {
		return err
	}

	b.dir = dir
	b.entries = entries
	b.top = 0
	b.sel = 0

	if len(entries) > 1 && entries[0].name == ".." {
		b.sel = 1
	}

	for i, en := range entries {
		if en.name == at {
			b.sel = i
		}
	}

	a.browseDir = dir

	return nil
}

// readBrowseDir is the folders and .torrent files in dir, folders first, each
// in name order, under a .. line when dir has a parent.
func readBrowseDir(dir string, hidden bool) ([]browseEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var out []browseEntry

	if filepath.Dir(dir) != dir {
		out = append(out, browseEntry{
			name:  "..",
			isDir: true,
		})
	}

	for _, d := range des {
		name := d.Name()
		if !hidden && strings.HasPrefix(name, ".") {
			continue
		}

		st, err := d.Info()
		if err != nil {
			continue
		}

		isDir := d.IsDir()

		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Stat(filepath.Join(dir, name))
			isDir = err == nil && target.IsDir()
		}

		if !isDir && !strings.HasSuffix(strings.ToLower(name), ".torrent") {
			continue
		}

		out = append(out, browseEntry{
			name:     name,
			isDir:    isDir,
			size:     st.Size(),
			mode:     st.Mode(),
			modified: st.ModTime(),
			created:  createdAt(st),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].name == ".." || out[j].name == ".." {
			return out[i].name == ".."
		}

		if out[i].isDir != out[j].isDir {
			return out[i].isDir
		}

		return out[i].name < out[j].name
	})

	return out, nil
}

func (b *browser) move(n int) {
	b.sel = max(0, min(b.sel+n, len(b.entries)-1))
}

// enter goes into the folder on the line, or picks the file.
func (a *App) browseEnter() {
	b := a.browse
	if b.sel >= len(b.entries) {
		return
	}

	en := b.entries[b.sel]
	if en.name == ".." {
		a.browseUp()
		return
	}

	path := filepath.Join(b.dir, en.name)

	if !en.isDir {
		a.browse = nil
		b.pick(path)

		return
	}

	err := a.browseTo(b, path, "")
	if err != nil {
		a.cmd.error("%v", err)
	}
}

// browseUp lists the folder above, with the cursor on the one just left.
func (a *App) browseUp() {
	b := a.browse

	parent := filepath.Dir(b.dir)
	if parent == b.dir {
		return
	}

	err := a.browseTo(b, parent, filepath.Base(b.dir))
	if err != nil {
		a.cmd.error("%v", err)
	}
}

func (a *App) browseToggleHidden() {
	b := a.browse
	a.browseHidden = !a.browseHidden

	at := ""

	if b.sel < len(b.entries) {
		at = b.entries[b.sel].name
	}

	err := a.browseTo(b, b.dir, at)
	if err != nil {
		a.cmd.error("%v", err)
		return
	}

	if a.browseHidden {
		a.cmd.info(msgHiddenShown)
	} else {
		a.cmd.info(msgHiddenHidden)
	}
}

func (a *App) browseKey(ev *tcell.EventKey) {
	b := a.browse

	if b.pending != 0 {
		p := b.pending
		b.pending = 0

		if p == 'g' && ev.Key() == tcell.KeyRune && ev.Rune() == 'g' {
			b.sel = 0
		}

		return
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		a.browse = nil
	case tcell.KeyDown:
		b.move(1)
	case tcell.KeyUp:
		b.move(-1)
	case tcell.KeyPgDn:
		b.move(b.rows)
	case tcell.KeyPgUp:
		b.move(-b.rows)
	case tcell.KeyCtrlD:
		b.move(b.rows / 2)
	case tcell.KeyCtrlU:
		b.move(-b.rows / 2)
	case tcell.KeyHome:
		b.sel = 0
	case tcell.KeyEnd:
		b.move(1 << 30)
	case tcell.KeyEnter, tcell.KeyRight:
		a.browseEnter()
	case tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		a.browseUp()
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'j':
			b.move(1)
		case 'k':
			b.move(-1)
		case 'G':
			b.move(1 << 30)
		case 'g':
			b.pending = 'g'
		case 'l':
			a.browseEnter()
		case 'h', '-':
			a.browseUp()
		case 'I':
			a.browseToggleHidden()
		case 'q':
			a.browse = nil
		}
	}
}

func (a *App) browseMouse(btn tcell.ButtonMask, pressed bool, y int) {
	b := a.browse

	switch {
	case btn&tcell.WheelUp != 0:
		b.move(-3)
	case btn&tcell.WheelDown != 0:
		b.move(3)
	case pressed && y >= b.listY && y < b.listY+b.rows:
		r := b.top + y - b.listY
		if r >= len(b.entries) {
			return
		}

		if r == b.sel {
			a.browseEnter()
		} else {
			b.sel = r
		}
	}
}

var browseCols = []column{
	{
		title: colName,
	},
	{
		title: colSize,
		w:     9,
		right: true,
		drop:  1,
	},
	{
		title: colCreated,
		w:     16,
		drop:  3,
	},
	{
		title: colModified,
		w:     16,
		drop:  2,
	},
	{
		title: colMode,
		w:     10,
		drop:  4,
	},
}

func (b *browser) draw(cv canvas) {
	in := cv.box(strBrowse, displayPath(b.dir), true)
	if in.h < 2 {
		b.rows = 1
		return
	}

	in = in.sub(1, 0, in.w-2, in.h)
	cols := fit(browseCols, in.w, 20)
	drawHeader(in, cols)

	rows := in.sub(0, 1, in.w, in.h-1)
	b.listY = rows.y
	b.rows = max(1, rows.h)
	b.sel = max(0, min(b.sel, len(b.entries)-1))
	b.top = scroll(b.top, b.sel, rows.h, len(b.entries))

	for y := range rows.h {
		r := b.top + y
		if r >= len(b.entries) {
			break
		}

		en := b.entries[r]
		sel := r == b.sel

		if sel {
			rows.fillRow(0, y, stSelected)
		}

		x := 0

		for _, c := range cols {
			s, st := browseCell(en, c)

			if sel {
				st = stSelected
			}

			if c.right {
				rows.right(x, y, c.w, s, st)
			} else {
				rows.textMax(x, y, c.w, s, st)
			}

			x += c.w + 1
		}
	}
}

func browseCell(en browseEntry, c column) (string, tcell.Style) {
	switch c.title {
	case colName:
		if en.isDir && en.name != ".." {
			return en.name + "/", stAccent
		}

		return en.name, stNormal
	case colSize:
		if en.isDir {
			return "-", stDim
		}

		return size(en.size), stNormal
	case colCreated:
		return browseTime(en.created), stDim
	case colModified:
		return browseTime(en.modified), stDim
	case colMode:
		if en.name == ".." {
			return "-", stDim
		}

		return en.mode.String(), stDim
	}

	return "", stNormal
}

func browseTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	return t.Format("2006-01-02 15:04")
}

// displayPath writes a path under the home folder with ~.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}

	if p == home {
		return "~"
	}

	rest, ok := strings.CutPrefix(p, home+string(filepath.Separator))
	if ok {
		return filepath.Join("~", rest)
	}

	return p
}
