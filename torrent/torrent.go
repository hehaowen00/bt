package torrent

import (
	"context"
	"crypto/sha1"
	"math/rand/v2"
	"net/netip"
	"slices"
	"sync"
	"time"

	"bt/dht"
	"bt/metainfo"
	"bt/peer"
)

type State int

const (
	Stopped State = iota
	Checking
	Metadata
	Downloading
	Seeding
	Failed
)

const (
	maxPeers        = 60
	maxHalfOpen     = 32
	unchokeSlots    = 4
	snubAfter       = 60 * time.Second
	resumeEvery     = 30 * time.Second
	dhtEvery        = 5 * time.Minute
	enoughPeers     = 30
	retryPeerAfter  = 2 * time.Minute
	maxPartialBytes = 256 << 20
)

type blockKey struct{ piece, begin int }

// partial is a piece being downloaded, held in memory until it is whole.
type partial struct {
	buf       []byte
	have      []bool
	requested []uint8 // requests out for each block
	got       int
}

type candidate struct {
	lastTry time.Time
	fails   int
}

type trackerState struct {
	url      string
	tier     int
	next     time.Time
	busy     bool
	fails    int
	status   string
	err      string
	peers    int
	seeders  int
	leechers int
	started  bool
	lastOK   time.Time
}

// Torrent is one torrent in the session. Everything below mu is guarded by
// it; peers' goroutines take it for each message.
type Torrent struct {
	s    *Session
	hash metainfo.Hash

	mu       sync.Mutex
	meta     *metainfo.Torrent
	dir      string
	state    State
	err      string
	paused   bool
	added    time.Time
	finished time.Time

	have      bitfield
	wanted    []bool // per file
	pieceWant []bool // per piece, from wanted
	avail     []int  // per piece, how many connected peers have it
	partials  map[int]*partial
	verifying map[int]bool

	storage *storage

	peers     map[*peerConn]struct{}
	halfOpen  int
	cands     map[netip.AddrPort]*candidate
	trackers  []*trackerState
	lastDHT   time.Time
	dhtBusy   bool
	optimist  *peerConn
	chokeTick int

	downloaded, uploaded int64 // payload, over the torrent's life
	down, up             rate

	metaSize   int
	metaBuf    []byte
	metaHave   []bool
	metaAsked  []time.Time
	checkDone  int
	resumeDirt bool
	lastResume time.Time

	ctx    context.Context
	cancel context.CancelFunc
}

func (t *Torrent) info() *metainfo.Info { return t.meta.Info }

// setupInfo builds the piece tables once the info is known.
func (t *Torrent) setupInfo() {
	info := t.info()
	n := len(info.Pieces)

	if len(t.have) != len(newBitfield(n)) {
		t.have = newBitfield(n)
	}

	if len(t.wanted) != len(info.Files) {
		t.wanted = make([]bool, len(info.Files))
		for i := range t.wanted {
			t.wanted[i] = true
		}
	}

	t.avail = make([]int, n)
	t.partials = map[int]*partial{}
	t.verifying = map[int]bool{}
	t.storage = newStorage(t.dir, info)
	t.updateWant()
}

// updateWant marks the pieces that overlap a wanted file.
func (t *Torrent) updateWant() {
	info := t.info()
	t.pieceWant = make([]bool, len(info.Pieces))

	for i, f := range info.Files {
		if !t.wanted[i] || f.Length == 0 || f.IsPad() {
			continue
		}

		first := int(f.Offset / info.PieceLength)
		last := int((f.Offset + f.Length - 1) / info.PieceLength)

		for p := first; p <= last; p++ {
			t.pieceWant[p] = true
		}
	}
}

// complete reports whether every wanted piece is here.
func (t *Torrent) complete() bool {
	for i, w := range t.pieceWant {
		if w && !t.have.get(i) {
			return false
		}
	}

	return true
}

func (t *Torrent) start() {
	if t.cancel != nil {
		return
	}

	t.ctx, t.cancel = context.WithCancel(t.s.ctx)
	t.err = ""
	t.paused = false

	for _, tr := range t.trackers {
		tr.next = time.Time{}
		tr.fails = 0
		tr.started = false
	}

	switch {
	case t.meta.Info == nil:
		t.state = Metadata
	case t.needsCheck():
		t.state = Checking
		go t.check()
	default:
		t.afterCheck()
	}

	for _, p := range t.meta.Peers {
		ap, err := netip.ParseAddrPort(p)
		if err == nil {
			t.addCandidate(ap)
		}
	}

	go t.loop(t.ctx)
}

// needsCheck: a torrent with no resume data and files on disk is checked,
// and so is one whose resume data names a piece in a missing file.
func (t *Torrent) needsCheck() bool {
	if t.have.count() == 0 {
		return t.storage.anyExists()
	}

	info := t.info()
	for i, f := range info.Files {
		if f.IsPad() || f.Length == 0 {
			continue
		}

		first := int(f.Offset / info.PieceLength)
		if !t.have.get(first) {
			continue
		}

		st, err := statFile(info.DiskPath(t.dir, info.Files[i]))
		if err != nil || st < f.Length {
			return true
		}
	}

	return false
}

func (t *Torrent) afterCheck() {
	if t.complete() {
		t.state = Seeding
	} else {
		t.state = Downloading
	}
}

func (t *Torrent) stop() {
	if t.cancel == nil {
		return
	}

	t.cancel()
	t.cancel = nil

	for p := range t.peers {
		p.close()
	}

	t.peers = map[*peerConn]struct{}{}
	t.partials = map[int]*partial{}
	t.optimist = nil

	if t.meta.Info != nil {
		t.storage.Close()
		clear(t.avail)
	}

	t.down = rate{}
	t.up = rate{}
	t.state = Stopped
	t.saveResume()

	for _, tr := range t.trackers {
		if tr.started {
			t.s.stopping.Add(1)
			go t.s.announceStopped(t.announceReq(), tr.url)
		}

		tr.status = ""
		tr.busy = false
	}
}

// check hashes every piece on disk.
func (t *Torrent) check() {
	t.mu.Lock()
	info := t.info()
	st := t.storage
	ctx := t.ctx
	t.checkDone = 0
	t.mu.Unlock()

	have := newBitfield(len(info.Pieces))
	buf := make([]byte, info.PieceLength)

	for i := range info.Pieces {
		if ctx.Err() != nil {
			return
		}

		b := buf[:info.PieceSize(i)]

		err := st.ReadAt(b, int64(i)*info.PieceLength)
		if err == nil && sha1.Sum(b) == info.Pieces[i] {
			have.set(i)
		}

		t.mu.Lock()
		t.checkDone = i + 1
		t.mu.Unlock()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if ctx.Err() != nil {
		return
	}

	t.have = have
	t.resumeDirt = true
	t.saveResume()
	t.afterCheck()
}

// loop runs the once-a-second work until the torrent stops.
func (t *Torrent) loop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	t.mu.Lock()
	t.tick()
	t.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.mu.Lock()

			if ctx.Err() == nil {
				t.tick()
			}

			t.mu.Unlock()
		}
	}
}

func (t *Torrent) tick() {
	now := time.Now()

	t.down.tick()
	t.up.tick()

	for p := range t.peers {
		p.down.tick()
		p.up.tick()

		if len(p.reqs) > 0 && now.Sub(p.lastBlock) > snubAfter {
			p.snubbed = true
			t.releaseRequests(p)
		}

		if now.Sub(time.Unix(0, p.lastSend.Load())) > 90*time.Second {
			p.send(peer.KeepAlive())
		}

		if now.Sub(p.lastRecv) > 3*time.Minute {
			p.close()
		}
	}

	if t.state == Checking {
		return
	}

	t.announce(now)

	t.maybeDHT(now)

	t.connectMore(now)

	if t.state == Metadata {
		t.requestMetadata()
		return
	}

	t.chokeTick++
	if t.chokeTick%10 == 1 {
		t.rechoke()
	}

	for p := range t.peers {
		t.fillRequests(p)
	}

	if t.reachedRatio() {
		t.stop()
		t.paused = true

		go t.s.saveSession()

		return
	}

	if t.resumeDirt && now.Sub(t.lastResume) > resumeEvery {
		t.saveResume()
	}
}

func (t *Torrent) addCandidate(ap netip.AddrPort) {
	if !ap.IsValid() || ap.Port() == 0 {
		return
	}

	if _, ok := t.cands[ap]; !ok {
		t.cands[ap] = &candidate{}
	}
}

// connectMore dials known peers until there are enough.
func (t *Torrent) connectMore(now time.Time) {
	if t.state == Seeding {
		return
	}

	connected := map[netip.AddrPort]bool{}

	for p := range t.peers {
		connected[p.addr] = true
	}

	var ready []netip.AddrPort

	for ap, c := range t.cands {
		if connected[ap] || now.Sub(c.lastTry) < retryPeerAfter*time.Duration(1<<min(c.fails, 5)) {
			continue
		}

		ready = append(ready, ap)
	}

	rand.Shuffle(len(ready), func(i, j int) {
		ready[i], ready[j] = ready[j], ready[i]
	})

	for _, ap := range ready {
		if len(t.peers)+t.halfOpen >= maxPeers || t.halfOpen >= maxHalfOpen {
			break
		}

		t.cands[ap].lastTry = now
		t.halfOpen++

		go t.dial(t.ctx, ap)
	}
}

func (t *Torrent) wantsPeers() bool {
	return len(t.peers) < enoughPeers && t.state != Seeding
}

func (t *Torrent) maybeDHT(now time.Time) {
	d := t.s.dht
	if d == nil || t.dhtBusy || !t.wantsPeers() || now.Sub(t.lastDHT) < dhtEvery {
		return
	}

	if t.meta.Info != nil && t.meta.Info.Private {
		return
	}

	t.dhtBusy = true
	t.lastDHT = now
	ctx := t.ctx

	go func() {
		d.GetPeers(ctx, dht.ID(t.hash), func(ap netip.AddrPort) {
			t.mu.Lock()
			t.addCandidate(ap)
			t.mu.Unlock()
		})

		t.mu.Lock()
		t.dhtBusy = false
		t.mu.Unlock()
	}()
}

// rechoke unchokes the peers that give the most, and one at random.
func (t *Torrent) rechoke() {
	var interested []*peerConn

	for p := range t.peers {
		if p.peerInterested {
			interested = append(interested, p)
		}
	}

	seeding := t.state == Seeding

	slices.SortFunc(interested, func(a, b *peerConn) int {
		ra := a.down.get()
		rb := b.down.get()

		if seeding {
			ra = a.up.get()
			rb = b.up.get()
		}

		switch {
		case ra > rb:
			return -1
		case ra < rb:
			return 1
		}

		return 0
	})

	unchoke := map[*peerConn]bool{}

	for _, p := range interested {
		if len(unchoke) >= unchokeSlots {
			break
		}

		if !p.snubbed || seeding {
			unchoke[p] = true
		}
	}

	if t.chokeTick%30 == 1 || t.optimist == nil || !t.optimist.peerInterested {
		var rest []*peerConn

		for _, p := range interested {
			if !unchoke[p] {
				rest = append(rest, p)
			}
		}

		t.optimist = nil

		if len(rest) > 0 {
			t.optimist = rest[rand.IntN(len(rest))]
		}
	}

	if t.optimist != nil {
		unchoke[t.optimist] = true
	}

	for p := range t.peers {
		p.setChoking(!unchoke[p])
	}
}

// pieceDone runs after piece i is written: it is announced to every peer.
func (t *Torrent) pieceDone(i int) {
	t.have.set(i)
	t.resumeDirt = true

	for p := range t.peers {
		p.send(peer.EncodeHave(i))
		p.updateInterest()
	}

	if t.state == Downloading && t.complete() {
		t.state = Seeding
		t.finished = time.Now()
		t.saveResume()

		for _, tr := range t.trackers {
			tr.next = time.Time{}
		}

		// a peer that is also done is of no more use
		for p := range t.peers {
			if p.seed() {
				p.close()
			}
		}

		t.s.notifyComplete(t)
	}
}

// verify hashes a finished piece and writes it, outside the lock.
func (t *Torrent) verify(i int, pc *partial) {
	info := t.info()
	ok := sha1.Sum(pc.buf) == info.Pieces[i]

	var err error

	if ok {
		err = t.storage.WritePiece(i, pc.buf)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.verifying, i)

	if t.cancel == nil {
		return
	}

	switch {
	case err != nil:
		t.fail(err)
	case ok:
		t.pieceDone(i)
	}
}

func (t *Torrent) fail(err error) {
	msg := err.Error()

	t.stop()
	t.state = Failed
	t.err = msg
	t.s.notifyError(t, msg)
}

// reachedRatio reports a seeding torrent that has uploaded its share.
func (t *Torrent) reachedRatio() bool {
	r := t.s.cfg.SeedRatio
	if r <= 0 || t.state != Seeding {
		return false
	}

	base := t.downloaded
	if base == 0 {
		base = t.left() + t.info().Length
	}

	return float64(t.uploaded) >= r*float64(base)
}
