// Package app is the terminal interface: the torrent list, the detail pane,
// the status line and the command line.
package app

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"bt/torrent"
)

type Config struct {
	DownloadDir string
	Theme       string
}

const (
	tabGeneral = iota
	tabFiles
	tabPeers
	tabTrackers
	numTabs
)

var tabNames = [numTabs]string{strGeneral, strFiles, strPeers, strTrackers}

const (
	focusList = iota
	focusDetail
)

type App struct {
	s    tcell.Screen
	sess *torrent.Session
	cfg  Config

	all    []torrent.Summary
	rows   []int // indices into all that pass the filter
	filter string

	selHash string
	sel     int
	top     int

	detail *torrent.Detail
	tab    int
	focus  int
	dsel   [numTabs]int
	dtop   [numTabs]int

	cmd  cmdline
	help *helpView

	// where things were drawn, for the mouse and for page moves
	listY, listRows     int
	detailY, detailRows int
	tabX                [numTabs + 1]int
	tabY                int

	pasting  bool
	pasteBuf strings.Builder
	buttons  tcell.ButtonMask
	quit     bool
	making   bool
}

type eventTick struct{ tcell.EventTime }

type eventNotify struct {
	tcell.EventTime
	name, msg string
	isErr     bool
}

func New(s tcell.Screen, sess *torrent.Session, cfg Config) *App {
	a := &App{
		s:    s,
		sess: sess,
		cfg:  cfg,
	}

	t, ok := themeByName(cfg.Theme)
	if !ok {
		t = themes[0]
	}

	t.apply()
	s.SetStyle(stNormal)

	return a
}

// Notify reports a finished or failed torrent, from any goroutine.
func (a *App) Notify(name, msg string, isErr bool) {
	ev := &eventNotify{
		name:  name,
		msg:   msg,
		isErr: isErr,
	}
	ev.SetEventNow()
	_ = a.s.PostEvent(ev)
}

// Error shows an error on the command line, for one found at startup.
func (a *App) Error(format string, args ...any) { a.cmd.error(format, args...) }

// Add adds a torrent named on the command line.
func (a *App) Add(src string) {
	name, err := a.sess.Add(src, "")
	if err != nil {
		a.cmd.error("%v", err)
		return
	}

	a.cmd.info(msgAdded, name)
}

// Quit ends Run, from any goroutine.
func (a *App) Quit() {
	ev := &eventKeyQuit{}
	ev.SetEventNow()
	_ = a.s.PostEvent(ev)
}

type eventKeyQuit struct{ tcell.EventTime }

func (a *App) Run() {
	a.s.EnableMouse()
	a.s.EnablePaste()

	stop := make(chan struct{})
	defer close(stop)

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()

		for {
			select {
			case <-stop:
				return
			case <-t.C:
				ev := &eventTick{}
				ev.SetEventNow()
				_ = a.s.PostEvent(ev)
			}
		}
	}()

	a.refresh()

	for !a.quit {
		a.draw()

		switch ev := a.s.PollEvent().(type) {
		case nil:
			return
		case *tcell.EventResize:
			a.s.Sync()
		case *eventTick:
			a.refresh()
		case *eventKeyQuit:
			return
		case *eventNotify:
			if ev.isErr {
				a.cmd.error(msgFinished, ev.name, ev.msg)
			} else {
				a.cmd.info(msgFinished, ev.name, ev.msg)
			}

			a.refresh()
		case *eventMakeProgress:
			a.cmd.info(msgHashing, ev.name, ev.percent)
		case *eventMade:
			a.made(ev)
			a.refresh()
		case *tcell.EventPaste:
			a.pasteEvent(ev)
		case *tcell.EventKey:
			if a.pasting {
				a.pasteKey(ev)
				continue
			}

			a.key(ev)
			a.refresh()
		case *tcell.EventMouse:
			a.mouse(ev)
		}
	}
}

// refresh takes a new snapshot of the torrents.
func (a *App) refresh() {
	a.all = a.sess.List()
	a.rows = a.rows[:0]
	f := strings.ToLower(a.filter)

	for i, t := range a.all {
		if f == "" || strings.Contains(strings.ToLower(t.Name), f) {
			a.rows = append(a.rows, i)
		}
	}

	a.sel = -1

	for r, i := range a.rows {
		if a.all[i].Hash == a.selHash {
			a.sel = r
		}
	}

	if a.sel < 0 && len(a.rows) > 0 {
		a.sel = 0
	}

	a.detail = nil
	a.selHash = ""

	if a.sel >= 0 {
		a.selHash = a.all[a.rows[a.sel]].Hash
		a.detail, _ = a.sess.Detail(a.selHash)
	}
}

func (a *App) selected() (torrent.Summary, bool) {
	if a.sel < 0 || a.sel >= len(a.rows) {
		a.cmd.error(errNoTorrent)
		return torrent.Summary{}, false
	}

	return a.all[a.rows[a.sel]], true
}

func (a *App) selectRow(r int) {
	if len(a.rows) == 0 {
		return
	}

	r = max(0, min(r, len(a.rows)-1))

	if r != a.sel {
		a.dsel = [numTabs]int{}
		a.dtop = [numTabs]int{}
	}

	a.sel = r
	a.selHash = a.all[a.rows[r]].Hash
}

func (a *App) detailLen() int {
	d := a.detail
	if d == nil {
		return 0
	}

	switch a.tab {
	case tabFiles:
		return len(d.Files)
	case tabPeers:
		return len(d.PeerList)
	case tabTrackers:
		return len(d.Trackers)
	}

	return 0
}

// move moves the cursor of the focused pane by n rows.
func (a *App) move(n int) {
	if a.focus == focusList {
		a.selectRow(a.sel + n)
		return
	}

	l := a.detailLen()

	if a.tab == tabGeneral {
		a.dtop[a.tab] = max(0, a.dtop[a.tab]+n)
		return
	}

	if l > 0 {
		a.dsel[a.tab] = max(0, min(a.dsel[a.tab]+n, l-1))
	}
}

func (a *App) moveTo(r int) {
	if a.focus == focusList {
		a.selectRow(r)
		return
	}

	a.dsel[a.tab] = max(0, min(r, a.detailLen()-1))
	a.dtop[a.tab] = 0

	if a.tab == tabGeneral && r > 0 {
		a.dtop[a.tab] = r
	}
}

func (a *App) pageRows() int {
	if a.focus == focusList {
		return max(1, a.listRows)
	}

	return max(1, a.detailRows)
}

// pasteEvent opens the add prompt for text pasted at the list, and
// otherwise gives it to the prompt.
func (a *App) pasteEvent(ev *tcell.EventPaste) {
	if ev.Start() {
		a.pasting = true
		a.pasteBuf.Reset()

		return
	}

	a.pasting = false
	text := a.pasteBuf.String()

	if a.cmd.prompt == nil && a.help == nil {
		a.askAdd("")
	}

	a.cmd.paste(text)
}

func (a *App) pasteKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyRune:
		a.pasteBuf.WriteRune(ev.Rune())
	case tcell.KeyEnter, tcell.KeyTab:
		a.pasteBuf.WriteByte(' ')
	}
}

func (a *App) mouse(ev *tcell.EventMouse) {
	btn := ev.Buttons()
	pressed := btn&tcell.Button1 != 0 && a.buttons&tcell.Button1 == 0
	a.buttons = btn
	x, y := ev.Position()

	if a.help != nil {
		switch {
		case btn&tcell.WheelUp != 0:
			a.help.scroll(-3)
		case btn&tcell.WheelDown != 0:
			a.help.scroll(3)
		}

		return
	}

	inList := y >= a.listY && y < a.listY+a.listRows
	inDetail := y >= a.detailY-1 && y < a.detailY+a.detailRows

	switch {
	case btn&(tcell.WheelUp|tcell.WheelDown) != 0:
		d := 3

		if btn&tcell.WheelUp != 0 {
			d = -3
		}

		switch {
		case inList:
			a.focus = focusList
		case inDetail:
			a.focus = focusDetail
		}

		a.move(d)
	case !pressed:
		return
	case y == a.tabY && x >= a.tabX[0] && x < a.tabX[numTabs]:
		for i := range numTabs {
			if x >= a.tabX[i] && x < a.tabX[i+1] {
				a.tab = i
			}
		}

		a.focus = focusDetail
	case inList:
		a.focus = focusList
		a.selectRow(a.top + y - a.listY)
	case inDetail && y >= a.detailY:
		a.focus = focusDetail
		if a.tab != tabGeneral {
			a.moveTo(a.dtop[a.tab] + y - a.detailY)
		}
	default:
		return
	}

	a.refresh()
	a.draw()
}

// copyText puts text on the clipboard through the terminal (OSC 52), which
// works over SSH too.
func (a *App) copyText(text string) {
	tty, ok := a.s.Tty()
	if !ok {
		return
	}

	_, _ = tty.Write([]byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x1b\\"))
}
