package app

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v2"
)

func (a *App) key(ev *tcell.EventKey) {
	if a.cmd.key(ev) {
		return
	}

	if a.help != nil {
		if a.help.key(ev) {
			a.help = nil
		}

		return
	}

	switch ev.Key() {
	case tcell.KeyCtrlC, tcell.KeyCtrlQ:
		a.quit = true
	case tcell.KeyDown:
		a.move(1)
	case tcell.KeyUp:
		a.move(-1)
	case tcell.KeyPgDn:
		a.move(a.pageRows())
	case tcell.KeyPgUp:
		a.move(-a.pageRows())
	case tcell.KeyHome:
		a.moveTo(0)
	case tcell.KeyEnd:
		a.moveTo(1 << 30)
	case tcell.KeyRight:
		a.tab = (a.tab + 1) % numTabs
	case tcell.KeyLeft:
		a.tab = (a.tab + numTabs - 1) % numTabs
	case tcell.KeyTab, tcell.KeyBacktab:
		a.focus = 1 - a.focus
	case tcell.KeyEnter:
		a.focus = focusDetail
	case tcell.KeyEscape:
		switch {
		case a.focus == focusDetail:
			a.focus = focusList
		case a.filter != "":
			a.filter = ""
		}
	case tcell.KeyDelete, tcell.KeyBackspace, tcell.KeyBackspace2:
		a.askRemove(ev.Modifiers()&tcell.ModShift != 0)
	case tcell.KeyCtrlF:
		a.askFilter()
	case tcell.KeyCtrlP:
		a.askCommand()
	case tcell.KeyF1:
		a.help = newHelp()
	case tcell.KeyRune:
		a.runeKey(ev.Rune())
	}
}

func (a *App) runeKey(r rune) {
	switch r {
	case '1', '2', '3', '4':
		a.tab = int(r - '1')
	case 'a':
		a.askAdd("")
	case 'A':
		a.askAddTo()
	case 'n':
		a.askMake()
	case 'p':
		a.togglePause()
	case ' ':
		if a.focus == focusDetail && a.tab == tabFiles {
			a.toggleFile()
		} else {
			a.togglePause()
		}
	case '+', '-':
		a.setAllFiles(r == '+')
	case 'D':
		a.askRemove(true)
	case 'r':
		a.recheck()
	case 'c':
		a.copyMagnet()
	case '/':
		a.askFilter()
	case '?':
		a.help = newHelp()
	case 't':
		a.nextTheme()
	case 'q':
		a.quit = true
	}
}

func (a *App) copyMagnet() {
	t, ok := a.selected()
	if !ok {
		return
	}

	link, err := a.sess.Magnet(t.Hash)
	if err != nil {
		a.cmd.error("%v", err)
		return
	}

	a.copyText(link)
	a.cmd.info(msgCopied)
}

func (a *App) copyHash() {
	t, ok := a.selected()
	if ok {
		a.copyText(t.Hash)
		a.cmd.info(msgCopiedHash)
	}
}

func (a *App) copyPath() {
	t, ok := a.selected()
	if ok && a.detail != nil {
		a.copyText(filepath.Join(a.detail.Dir, t.Name))
		a.cmd.info(msgCopiedPath)
	}
}

func (a *App) askAdd(initial string) {
	p := &prompt{
		label:    promptAdd,
		complete: completePath,
		onSubmit: func(text string) {
			if text != "" {
				a.Add(text)
			}
		},
	}
	p.edit.set(initial)
	a.cmd.ask(p)
}

// askAddTo asks for the torrent, then for the folder to save it in.
func (a *App) askAddTo() {
	a.cmd.ask(&prompt{
		label:    promptAdd,
		complete: completePath,
		onSubmit: func(src string) {
			if src == "" {
				return
			}

			p := &prompt{
				label:    promptSaveIn,
				complete: completeDir,
				onSubmit: func(dir string) {
					name, err := a.sess.Add(src, dir)
					if err != nil {
						a.cmd.error("%v", err)
						return
					}

					a.cmd.info(msgAdded, name)
				},
			}
			p.edit.set(a.cfg.DownloadDir + "/")
			a.cmd.ask(p)
		},
	})
}

func (a *App) askFilter() {
	old := a.filter
	p := &prompt{
		label: promptSearch,
		onChange: func(text string) {
			a.filter = text
			a.refresh()
		},
		onSubmit: func(text string) {
			a.filter = text
		},
		onCancel: func() {
			a.filter = old
		},
	}
	p.edit.set(old)
	a.cmd.ask(p)
}

func (a *App) askRemove(deleteFiles bool) {
	t, ok := a.selected()
	if !ok {
		return
	}

	q := promptRemove

	if deleteFiles {
		q = promptDelete
	}

	a.cmd.confirm(sprintf(q, t.Name), func() {
		err := a.sess.Remove(t.Hash, deleteFiles)
		if err != nil {
			a.cmd.error("%v", err)
			return
		}

		if deleteFiles {
			a.cmd.info(msgDeleted, t.Name)
		} else {
			a.cmd.info(msgRemoved, t.Name)
		}
	})
}

func (a *App) togglePause() {
	t, ok := a.selected()
	if !ok {
		return
	}

	if t.Paused {
		a.do(a.sess.Resume(t.Hash), msgResumed, t.Name)
	} else {
		a.do(a.sess.Pause(t.Hash), msgPaused, t.Name)
	}
}

func (a *App) recheck() {
	t, ok := a.selected()
	if ok {
		a.do(a.sess.Recheck(t.Hash), msgRechecking, t.Name)
	}
}

// do reports err, or the message when there is none.
func (a *App) do(err error, msg string, args ...any) {
	if err != nil {
		a.cmd.error("%v", err)
		return
	}

	a.cmd.info(msg, args...)
}

func (a *App) toggleFile() {
	d := a.detail
	if d == nil || len(d.Files) == 0 {
		return
	}

	row := a.dsel[tabFiles]
	i := a.sess.FileIndex(d.Hash, row)
	want := !d.Files[row].Wanted

	err := a.sess.SetWanted(d.Hash, []int{i}, want)
	if err != nil {
		a.cmd.error("%v", err)
		return
	}

	a.move(1)
}

func (a *App) setAllFiles(want bool) {
	d := a.detail
	if d == nil || a.focus != focusDetail || a.tab != tabFiles {
		a.cmd.error(errNotFilesTab)
		return
	}

	var idx []int

	for row := range d.Files {
		idx = append(idx, a.sess.FileIndex(d.Hash, row))
	}

	if want {
		a.do(a.sess.SetWanted(d.Hash, idx, true), msgWanted, len(idx))
	} else {
		a.do(a.sess.SetWanted(d.Hash, idx, false), msgSkipped, len(idx))
	}
}

func (a *App) nextTheme() {
	i := slices.IndexFunc(themes, func(t theme) bool {
		return t.name == a.cfg.Theme
	})
	a.setTheme(themes[(i+1)%len(themes)].name)
}

func (a *App) setTheme(name string) bool {
	t, ok := themeByName(name)
	if !ok {
		return false
	}

	t.apply()
	a.cfg.Theme = name
	a.s.SetStyle(stNormal)
	a.s.Sync()
	a.cmd.info(msgTheme, name)

	return true
}

// A paletteCmd is one entry in the command palette.
type paletteCmd struct {
	name, keys string
	run        func(a *App)
}

var paletteCmds = []paletteCmd{
	{
		cmdAdd,
		"a",
		func(a *App) {
			a.askAdd("")
		},
	},
	{
		cmdAddTo,
		"A",
		func(a *App) {
			a.askAddTo()
		},
	},
	{
		cmdMake,
		"n",
		func(a *App) {
			a.askMake()
		},
	},
	{
		cmdPause,
		"Space",
		func(a *App) {
			a.togglePause()
		},
	},
	{
		cmdRecheck,
		"r",
		func(a *App) {
			a.recheck()
		},
	},
	{
		cmdRemove,
		"Delete",
		func(a *App) {
			a.askRemove(false)
		},
	},
	{
		cmdDelete,
		"D",
		func(a *App) {
			a.askRemove(true)
		},
	},
	{
		cmdCopyMagnet,
		"c",
		func(a *App) {
			a.copyMagnet()
		},
	},
	{
		cmdCopyHash,
		"",
		func(a *App) {
			a.copyHash()
		},
	},
	{
		cmdCopyPath,
		"",
		func(a *App) {
			a.copyPath()
		},
	},
	{
		cmdFilter,
		"/",
		func(a *App) {
			a.askFilter()
		},
	},
	{
		cmdNextTheme,
		"t",
		func(a *App) {
			a.nextTheme()
		},
	},
	{
		cmdHelp,
		"?",
		func(a *App) {
			a.help = newHelp()
		},
	},
	{
		cmdQuit,
		"q",
		func(a *App) {
			a.quit = true
		},
	},
}

// palette is every command, then a theme command for each theme.
func palette() []paletteCmd {
	out := slices.Clone(paletteCmds)

	for _, name := range ThemeNames() {
		out = append(out, paletteCmd{
			name: sprintf(cmdTheme, name),
			run: func(a *App) {
				a.setTheme(name)
			},
		})
	}

	return out
}

func (a *App) askCommand() {
	cmds := palette()
	find := func(name string) (paletteCmd, bool) {
		i := slices.IndexFunc(cmds, func(c paletteCmd) bool {
			return c.name == name
		})
		if i < 0 {
			return paletteCmd{}, false
		}

		return cmds[i], true
	}

	a.cmd.ask(&prompt{
		label: promptCommand,
		pick: func(text string) []string {
			var out []string

			for _, c := range cmds {
				if matchWords(c.name, text) {
					out = append(out, c.name)
				}
			}

			return out
		},
		aside: func(name string) string {
			c, _ := find(name)
			return c.keys
		},
		onSubmit: func(name string) {
			c, ok := find(name)
			if ok {
				c.run(a)
			}
		},
	})
}

// matchWords reports whether every word of query starts a word of name,
// ignoring case, so "rem del" finds "Remove Torrent and Delete Files".
func matchWords(name, query string) bool {
	words := strings.Fields(strings.ToLower(name))

	for _, q := range strings.Fields(strings.ToLower(query)) {
		if !slices.ContainsFunc(words, func(w string) bool {
			return strings.HasPrefix(w, q)
		}) {
			return false
		}
	}

	return true
}
