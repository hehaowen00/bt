package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"bt/torrent"
)

func (a *App) draw() {
	a.s.HideCursor()

	w, h := a.s.Size()
	cv := canvas{
		s: a.s,
		w: w,
		h: h,
	}
	cv.fill(stNormal)

	if a.help != nil {
		a.help.draw(cv.sub(0, 0, w, h-1))
		a.drawStatus(cv, h-2)
		a.cmd.draw(cv, h-1)
		a.s.Show()

		return
	}

	body := h - 2
	detailH := 0

	if body >= 16 {
		detailH = max(9, body*11/20)
	}

	a.drawList(cv.sub(0, 0, w, body-detailH))

	if detailH > 0 {
		a.drawDetail(cv.sub(0, body-detailH, w, detailH))
	} else {
		a.detailRows = 0
	}

	a.drawStatus(cv, h-2)
	a.cmd.draw(cv, h-1)
	a.s.Show()
}

// column is one column of a table: its title, width, alignment and the
// order it is dropped in when the screen is narrow (higher goes first).
type column struct {
	title string
	w     int
	right bool
	drop  int
}

// fit drops columns until the flexible column, the one with no width, has
// at least minFlex cells, and gives it what is left.
func fit(cols []column, w, minFlex int) []column {
	cols = slices.Clone(cols)

	for {
		used := 0
		flex := -1

		for i, c := range cols {
			if c.w == 0 && flex < 0 {
				flex = i
				continue
			}

			used += c.w + 1
		}

		worst := -1

		for i, c := range cols {
			if c.drop > 0 && (worst < 0 || c.drop > cols[worst].drop) {
				worst = i
			}
		}

		if w-used >= minFlex || worst < 0 {
			cols[flex].w = max(1, w-used)
			return cols
		}

		cols = slices.Delete(cols, worst, worst+1)
	}
}

func drawHeader(cv canvas, cols []column) {
	x := 0

	for _, c := range cols {
		if c.right {
			cv.right(x, 0, c.w, c.title, stDim)
		} else {
			cv.textMax(x, 0, c.w, c.title, stDim)
		}

		x += c.w + 1
	}
}

// scroll keeps sel in rows visible lines from top.
func scroll(top, sel, rows, n int) int {
	top = min(top, sel)
	if sel >= top+rows {
		top = sel - rows + 1
	}

	return max(0, min(top, n-rows))
}

var listCols = []column{
	{
		title: colName,
	},
	{
		title: colSize,
		w:     9,
		right: true,
		drop:  2,
	},
	{
		title: colDone,
		w:     16,
		drop:  1,
	},
	{
		title: colStatus,
		w:     17,
		drop:  3,
	},
	{
		title: colDown,
		w:     11,
		right: true,
		drop:  4,
	},
	{
		title: colUp,
		w:     11,
		right: true,
		drop:  7,
	},
	{
		title: colPeers,
		w:     7,
		right: true,
		drop:  6,
	},
	{
		title: colETA,
		w:     8,
		right: true,
		drop:  5,
	},
}

func (a *App) drawList(cv canvas) {
	right := fmt.Sprint(len(a.all))

	if a.filter != "" {
		right = sprintf(strFilterLabel, a.filter) + " · " + fmt.Sprintf("%d/%d", len(a.rows), len(a.all))
	}

	in := cv.box(strTorrents, right, a.focus == focusList)
	if in.h < 2 {
		a.listRows = 0
		return
	}

	in = in.sub(1, 0, in.w-2, in.h)

	if len(a.rows) == 0 {
		msg := strNoTorrents

		if len(a.all) > 0 {
			msg = strNoMatches
		}

		in.text(max(0, (in.w-width(msg))/2), in.h/2, msg, stBold)

		a.listRows = 0

		return
	}

	cols := fit(listCols, in.w, 20)
	drawHeader(in, cols)

	rows := in.sub(0, 1, in.w, in.h-1)
	a.listY = rows.y
	a.listRows = rows.h
	a.top = scroll(a.top, a.sel, rows.h, len(a.rows))

	for y := range rows.h {
		r := a.top + y
		if r >= len(a.rows) {
			break
		}

		a.drawRow(rows, y, cols, a.all[a.rows[r]], r == a.sel)
	}
}

func (a *App) drawRow(cv canvas, y int, cols []column, t torrent.Summary, sel bool) {
	base := stNormal

	if sel {
		base = stSelected

		if a.focus != focusList {
			base = stBold.Reverse(false).Underline(true)
		}

		cv.fillRow(0, y, base)
	}

	x := 0

	for _, c := range cols {
		switch c.title {
		case colName:
			cv.textMax(x, y, c.w, t.Name, base)
		case colSize:
			if t.Size > 0 {
				cv.right(x, y, c.w, size(t.Size), base)
			}
		case colDone:
			pct := percent(t.Progress)
			barW := c.w - 7
			cv.bar(x, y, barW, t.Progress, stateStyle(t.State, stBarDone))
			cv.right(x+barW, y, 7, pct, base)
		case colStatus:
			cv.textMax(x, y, c.w, statusText(t), stateStyle(t.State, base))
		case colDown:
			cv.right(x, y, c.w, speed(t.DownRate), base)
		case colUp:
			cv.right(x, y, c.w, speed(t.UpRate), base)
		case colPeers:
			if t.Peers > 0 {
				cv.right(x, y, c.w, fmt.Sprintf("%d/%d", t.Seeds, t.Peers), base)
			}
		case colETA:
			cv.right(x, y, c.w, eta(t.ETA), base)
		}

		x += c.w + 1
	}
}

func statusText(t torrent.Summary) string {
	if t.State == torrent.Failed && t.Err != "" {
		return t.State.String() + ": " + t.Err
	}

	if t.State == torrent.Stopped {
		return strPaused
	}

	return t.State.String()
}

// stateStyle colours a state: blue downloading, green seeding, yellow
// waiting, red failed, dim paused.
func stateStyle(s torrent.State, base tcell.Style) tcell.Style {
	if base == stSelected {
		return base
	}

	switch s {
	case torrent.Downloading:
		return stInfo
	case torrent.Seeding:
		return stOK
	case torrent.Checking, torrent.Metadata:
		return stWarn
	case torrent.Failed:
		return stError
	case torrent.Stopped:
		return stDim
	}

	return base
}

func (a *App) drawDetail(cv canvas) {
	in := cv.box("", "", a.focus == focusDetail)

	// the tabs sit in the top edge
	x := 2
	a.tabY = cv.y

	for i, name := range tabNames {
		a.tabX[i] = cv.x + x
		label := " " + name + " "
		st := stDim

		if i == a.tab {
			st = stAccent.Reverse(true)

			if a.focus != focusDetail {
				st = stBold.Reverse(true)
			}
		}

		x += cv.textMax(x, 0, cv.w-x-2, label, st) + 1
	}

	a.tabX[numTabs] = cv.x + x

	in = in.sub(1, 0, in.w-2, in.h)
	a.detailY = in.y + 1
	a.detailRows = in.h - 1

	d := a.detail
	if d == nil {
		in.text(0, in.h/2, strNoneChosen, stDim)
		return
	}

	switch a.tab {
	case tabGeneral:
		a.detailY = in.y
		a.detailRows = in.h
		a.drawGeneral(in, d)
	case tabFiles:
		a.drawFiles(in, d)
	case tabPeers:
		a.drawPeers(in, d)
	case tabTrackers:
		a.drawTrackers(in, d)
	}
}

func (a *App) drawGeneral(cv canvas, d *torrent.Detail) {
	type line struct {
		label, value string
		st           tcell.Style
		pieces       bool
	}

	status := statusText(d.Summary)

	if d.Size > 0 {
		status += " · " + percent(d.Progress) + " (" + sprintf(strOf, size(d.Done), size(d.Size)) + ")"
	}

	ratio := 0.0

	if d.Downloaded > 0 {
		ratio = float64(d.Uploaded) / float64(d.Downloaded)
	}

	lines := []line{
		{
			label: lblName,
			value: d.Name,
		},
		{
			label: lblStatus,
			value: status,
			st:    stateStyle(d.State, stNormal),
		},
	}

	if d.Pieces > 0 {
		lines = append(lines,
			line{
				label:  lblPieces,
				pieces: true,
			},
			line{
				value: sprintf(strPieceInfo, d.HavePieces, d.Pieces, size(d.PieceLength)),
			},
		)
	}

	rem := eta(d.ETA)

	if d.State == torrent.Seeding {
		rem = strDone
	}

	lines = append(lines,
		line{
			label: lblSpeed,
			value: "↓ " + orZero(speed(d.DownRate)) + "   ↑ " + orZero(speed(d.UpRate)),
		},
		line{
			label: lblTransfer,
			value: "↓ " + size(d.Downloaded) + "   ↑ " + size(d.Uploaded) + "   " + sprintf(strRatio, ratio),
		},
		line{
			label: lblPeers,
			value: sprintf(strPeerInfo, d.Peers, d.Seeds, d.Known),
		},
		line{
			label: lblETA,
			value: rem,
		},
		line{
			label: lblSavedIn,
			value: d.Dir,
		},
		line{
			label: lblHash,
			value: d.Hash,
		},
		line{
			label: lblAdded,
			value: d.Added.Local().Format(time.DateTime),
		},
	)

	if d.Private {
		lines = append(lines, line{
			label: lblPrivate,
			value: strYes,
		})
	}

	if d.Comment != "" {
		lines = append(lines, line{
			label: lblComment,
			value: d.Comment,
		})
	}

	if d.Err != "" {
		lines = append(lines, line{
			label: lblError,
			value: d.Err,
			st:    stError,
		})
	}

	top := min(a.dtop[tabGeneral], max(0, len(lines)-cv.h))
	a.dtop[tabGeneral] = top
	labelW := 13

	for y := 0; y < cv.h && top+y < len(lines); y++ {
		l := lines[top+y]
		cv.text(0, y, l.label, stLabel)

		if l.pieces {
			cv.pieceMap(labelW, y, cv.w-labelW, d.Have)
			continue
		}

		st := l.st
		if st == (tcell.Style{}) {
			st = stNormal
		}

		cv.text(labelW, y, l.value, st)
	}
}

func orZero(s string) string {
	if s == "" {
		return "0 B/s"
	}

	return s
}

// table draws a header and rows with a cursor, for the Files, Peers and
// Trackers tabs.
func (a *App) table(
	cv canvas,
	cols []column,
	minFlex, n int,
	empty string,
	cell func(r int, c column) (string, tcell.Style),
) {
	if n == 0 {
		cv.text(0, cv.h/2, empty, stDim)
		return
	}

	cols = fit(cols, cv.w, minFlex)
	drawHeader(cv, cols)

	rows := cv.sub(0, 1, cv.w, cv.h-1)
	a.dsel[a.tab] = min(a.dsel[a.tab], n-1)
	a.dtop[a.tab] = scroll(a.dtop[a.tab], a.dsel[a.tab], rows.h, n)

	for y := range rows.h {
		r := a.dtop[a.tab] + y
		if r >= n {
			break
		}

		sel := r == a.dsel[a.tab] && a.focus == focusDetail
		if sel {
			rows.fillRow(0, y, stSelected)
		}

		x := 0

		for _, c := range cols {
			s, st := cell(r, c)

			if sel {
				st = stSelected
			}

			if c.title == colDone && !sel {
				var frac float64

				_, _ = fmt.Sscanf(s, "%f", &frac)
				rows.bar(x, y, c.w-7, frac/100, stBarDone)
				rows.right(x+c.w-7, y, 7, s, st)
			} else if c.right {
				rows.right(x, y, c.w, s, st)
			} else {
				rows.textMax(x, y, c.w, s, st)
			}

			x += c.w + 1
		}
	}
}

var fileCols = []column{
	{
		title: " ",
		w:     3,
	},
	{
		title: colPath,
	},
	{
		title: colDone,
		w:     16,
		drop:  2,
	},
	{
		title: colSize,
		w:     9,
		right: true,
		drop:  1,
	},
}

func (a *App) drawFiles(cv canvas, d *torrent.Detail) {
	a.table(cv, fileCols, 20, len(d.Files), strNoFiles, func(r int, c column) (string, tcell.Style) {
		f := d.Files[r]
		st := stNormal

		if !f.Wanted {
			st = stDim
		}

		switch c.title {
		case colPath:
			return f.Path, st
		case " ":
			if f.Wanted {
				return "[x]", stOK
			}

			return "[ ]", stDim
		case colDone:
			frac := 1.0

			if f.Size > 0 {
				frac = float64(f.Done) / float64(f.Size)
			}

			return percent(frac), st
		case colSize:
			return size(f.Size), st
		}

		return "", st
	})
}

var peerCols = []column{
	{
		title: colAddr,
	},
	{
		title: colClient,
		w:     22,
		drop:  3,
	},
	{
		title: colFlags,
		w:     6,
		drop:  4,
	},
	{
		title: colDone,
		w:     16,
		drop:  2,
	},
	{
		title: colDown,
		w:     11,
		right: true,
		drop:  1,
	},
	{
		title: colUp,
		w:     11,
		right: true,
		drop:  1,
	},
}

func (a *App) drawPeers(cv canvas, d *torrent.Detail) {
	a.table(cv, peerCols, 22, len(d.PeerList), strNoPeers, func(r int, c column) (string, tcell.Style) {
		p := d.PeerList[r]

		switch c.title {
		case colAddr:
			return p.Addr, stNormal
		case colClient:
			return p.Client, stDim
		case colFlags:
			return p.Flags, stLabel
		case colDone:
			return percent(p.Progress), stNormal
		case colDown:
			return speed(p.DownRate), stInfo
		case colUp:
			return speed(p.UpRate), stOK
		}

		return "", stNormal
	})
}

var trackerCols = []column{
	{
		title: colURL,
	},
	{
		title: colTier,
		w:     4,
		right: true,
		drop:  5,
	},
	{
		title: colStatus,
		w:     30,
		drop:  1,
	},
	{
		title: colPeers,
		w:     6,
		right: true,
		drop:  4,
	},
	{
		title: colSeeds,
		w:     6,
		right: true,
		drop:  3,
	},
	{
		title: colLeech,
		w:     8,
		right: true,
		drop:  3,
	},
	{
		title: colNext,
		w:     8,
		right: true,
		drop:  2,
	},
}

func (a *App) drawTrackers(cv canvas, d *torrent.Detail) {
	a.table(cv, trackerCols, 30, len(d.Trackers), strNoTrackers, func(r int, c column) (string, tcell.Style) {
		t := d.Trackers[r]

		switch c.title {
		case colURL:
			return t.URL, stNormal
		case colTier:
			return fmt.Sprint(t.Tier + 1), stDim
		case colStatus:
			if t.Err != "" {
				return t.Status + ": " + t.Err, stError
			}

			if t.Status == "" {
				return "", stDim
			}

			return t.Status, stOK
		case colPeers:
			if t.Status == "" {
				return "", stNormal
			}

			return fmt.Sprint(t.Peers), stNormal
		case colSeeds:
			if t.Status == "" {
				return "", stNormal
			}

			return fmt.Sprint(t.Seeders), stNormal
		case colLeech:
			if t.Status == "" {
				return "", stNormal
			}

			return fmt.Sprint(t.Leechers), stNormal
		case colNext:
			if t.Next.IsZero() || d.State == torrent.Stopped {
				return "", stDim
			}

			left := time.Until(t.Next)
			if left <= 0 {
				return strNow, stDim
			}

			return eta(left.Round(time.Second)), stDim
		}

		return "", stNormal
	})
}

// drawStatus is the status line: the name, then the totals.
func (a *App) drawStatus(cv canvas, y int) {
	cv.fillRow(0, y, stStatus)

	var down, up float64

	active := 0

	for _, t := range a.all {
		down += t.DownRate
		up += t.UpRate

		if t.State == torrent.Downloading || t.State == torrent.Metadata {
			active++
		}
	}

	cv.text(0, y, " "+appName, stStatus)

	dht := strDHTOff
	n := a.sess.DHTNodes()
	if n >= 0 {
		dht = sprintf(strDHTNodes, n)
	}

	port := strNoPort
	p := a.sess.Port()
	if p != 0 {
		port = sprintf(strPortInfo, p)
	}

	right := strings.Join([]string{
		"↓ " + orZero(speed(down)),
		"↑ " + orZero(speed(up)),
		dht,
		port,
	}, "   ") + " "

	cv.right(0, y, cv.w, right, stStatus)
}
