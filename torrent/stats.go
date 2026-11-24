package torrent

import (
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Summary is a torrent's row in the list.
type Summary struct {
	Hash       string
	Name       string
	State      State
	Paused     bool // stopped, by choice or by an error
	Err        string
	Size       int64 // of the wanted files
	Done       int64
	Progress   float64 // of the check while checking, of the metadata while fetching it
	DownRate   float64
	UpRate     float64
	Downloaded int64
	Uploaded   int64
	Peers      int
	Seeds      int
	Known      int
	ETA        time.Duration // -1 when unknown
	Added      time.Time
}

type FileStat struct {
	Path   string
	Size   int64
	Done   int64
	Wanted bool
}

type PeerStat struct {
	Addr     string
	Client   string
	Flags    string
	Progress float64
	DownRate float64
	UpRate   float64
}

type TrackerStat struct {
	URL      string
	Tier     int
	Status   string
	Err      string
	Peers    int
	Seeders  int
	Leechers int
	Next     time.Time
}

// Detail is everything the detail pane shows.
type Detail struct {
	Summary
	Dir         string
	Magnet      string
	Comment     string
	Private     bool
	PieceLength int64
	Pieces      int
	HavePieces  int
	Have        []bool
	Files       []FileStat
	PeerList    []PeerStat
	Trackers    []TrackerStat
}

// List is every torrent, in the order added.
func (s *Session) List() []Summary {
	s.mu.Lock()
	ts := make([]*Torrent, 0, len(s.order))

	for _, h := range s.order {
		ts = append(ts, s.torrents[h])
	}

	s.mu.Unlock()

	out := make([]Summary, len(ts))

	for i, t := range ts {
		t.mu.Lock()
		out[i] = t.summary(nil)
		t.mu.Unlock()
	}

	return out
}

// fileDone is the bytes of each file in pieces that are done.
func (t *Torrent) fileDone() []int64 {
	info := t.info()
	done := make([]int64, len(info.Files))

	for i, f := range info.Files {
		if f.Length == 0 {
			continue
		}

		first := int(f.Offset / info.PieceLength)
		last := int((f.Offset + f.Length - 1) / info.PieceLength)

		for p := first; p <= last; p++ {
			if !t.have.get(p) {
				continue
			}

			lo := max(f.Offset, int64(p)*info.PieceLength)
			hi := min(f.Offset+f.Length, int64(p)*info.PieceLength+info.PieceSize(p))
			done[i] += hi - lo
		}
	}

	return done
}

func (t *Torrent) summary(fileDone []int64) Summary {
	sm := Summary{
		Hash:       t.hash.Hex(),
		Name:       t.meta.DisplayName(),
		State:      t.state,
		Paused:     t.cancel == nil,
		Err:        t.err,
		DownRate:   t.down.get(),
		UpRate:     t.up.get(),
		Downloaded: t.downloaded,
		Uploaded:   t.uploaded,
		Peers:      len(t.peers),
		Known:      len(t.cands),
		ETA:        -1,
		Added:      t.added,
	}

	for p := range t.peers {
		if p.have != nil && p.seed() {
			sm.Seeds++
		}
	}

	switch {
	case t.meta.Info == nil:
		if len(t.metaHave) > 0 {
			n := 0

			for _, h := range t.metaHave {
				if h {
					n++
				}
			}

			sm.Progress = float64(n) / float64(len(t.metaHave))
		}

		return sm
	case fileDone == nil:
		fileDone = t.fileDone()
	}

	for i, f := range t.info().Files {
		if t.wanted[i] && !f.IsPad() {
			sm.Size += f.Length
			sm.Done += fileDone[i]
		}
	}

	if sm.Size > 0 {
		sm.Progress = float64(sm.Done) / float64(sm.Size)
	}

	if t.state == Checking {
		sm.Progress = float64(t.checkDone) / float64(len(t.info().Pieces))
	}

	if t.state == Downloading && sm.DownRate > 0 {
		sm.ETA = time.Duration(float64(sm.Size-sm.Done) / sm.DownRate * float64(time.Second))
	}

	return sm
}

// Detail is the full state of one torrent.
func (s *Session) Detail(hash string) (*Detail, error) {
	t, err := s.get(hash)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	d := &Detail{
		Dir:     t.dir,
		Magnet:  t.meta.Magnet(),
		Comment: t.meta.Comment,
	}

	if t.meta.Info == nil {
		d.Summary = t.summary(nil)
	} else {
		info := t.info()
		done := t.fileDone()
		d.Summary = t.summary(done)
		d.Private = info.Private
		d.PieceLength = info.PieceLength
		d.Pieces = len(info.Pieces)
		d.HavePieces = t.have.count()
		d.Have = make([]bool, len(info.Pieces))

		for i := range d.Have {
			d.Have[i] = t.have.get(i)
		}

		for i, f := range info.Files {
			if f.IsPad() {
				continue
			}

			d.Files = append(d.Files, FileStat{
				Path:   filepath.Join(f.Path...),
				Size:   f.Length,
				Done:   done[i],
				Wanted: t.wanted[i],
			})
		}
	}

	for p := range t.peers {
		ps := PeerStat{
			Addr:     p.addr.String(),
			Client:   p.client,
			Flags:    p.flags(),
			DownRate: p.down.get(),
			UpRate:   p.up.get(),
		}

		if p.have != nil && len(p.have) > 0 {
			ps.Progress = float64(p.haveCount) / float64(len(t.info().Pieces))
		}

		d.PeerList = append(d.PeerList, ps)
	}

	slices.SortFunc(d.PeerList, func(a, b PeerStat) int {
		switch {
		case a.DownRate+a.UpRate > b.DownRate+b.UpRate:
			return -1
		case a.DownRate+a.UpRate < b.DownRate+b.UpRate:
			return 1
		}

		return strings.Compare(a.Addr, b.Addr)
	})

	for _, tr := range t.trackers {
		d.Trackers = append(d.Trackers, TrackerStat{
			URL:      tr.url,
			Tier:     tr.tier,
			Status:   tr.status,
			Err:      tr.err,
			Peers:    tr.peers,
			Seeders:  tr.seeders,
			Leechers: tr.leechers,
			Next:     tr.next,
		})
	}

	return d, nil
}

// FileIndex maps a row of Detail.Files, which leaves out padding, to the
// torrent's file index.
func (s *Session) FileIndex(hash string, row int) int {
	t, err := s.get(hash)
	if err != nil {
		return -1
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.meta.Info == nil {
		return -1
	}

	for i, f := range t.info().Files {
		if f.IsPad() {
			continue
		}

		if row == 0 {
			return i
		}

		row--
	}

	return -1
}

// flags are the peer's state, as other clients show it:
//
//	D downloading from it   d it would send, but this side is not interested
//	U uploading to it       u it wants to download, but is choked
//	O optimistic unchoke    S snubbed   I it connected to this side
func (p *peerConn) flags() string {
	var b strings.Builder

	switch {
	case p.amInterested && !p.peerChoking:
		b.WriteByte('D')
	case !p.peerChoking:
		b.WriteByte('d')
	}

	switch {
	case p.peerInterested && !p.amChoking:
		b.WriteByte('U')
	case p.peerInterested:
		b.WriteByte('u')
	}

	if p.t.optimist == p {
		b.WriteByte('O')
	}

	if p.snubbed {
		b.WriteByte('S')
	}

	if !p.outgoing {
		b.WriteByte('I')
	}

	return b.String()
}
