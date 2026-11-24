package torrent

import (
	"bufio"
	"context"
	"crypto/sha1"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bt/bencode"
	"bt/peer"
)

// The ids this side gives its extensions in the extension handshake.
const (
	extMetadata = 1
	extPex      = 2
)

const (
	metaPiece    = 16 << 10
	maxMetaSize  = 16 << 20
	maxUploadQ   = 256
	maxRequest   = 128 << 10
	clientName   = "bt 0.1"
	dialTimeout  = 6 * time.Second
	shakeTimeout = 15 * time.Second
)

// peerConn is a connection to one peer. Fields up to qmu are guarded by the
// torrent's lock.
type peerConn struct {
	t        *Torrent
	conn     net.Conn
	addr     netip.AddrPort
	outgoing bool
	id       [20]byte
	client   string
	ext      bool
	since    time.Time

	have      bitfield // nil until the torrent's info is known
	haveCount int
	rawBits   []byte // a bitfield sent before the info was known
	rawHaves  []int

	amChoking, amInterested     bool
	peerChoking, peerInterested bool

	reqs      map[blockKey]time.Time
	reqq      int
	extIDs    map[string]byte
	down, up  rate
	lastBlock time.Time
	lastRecv  time.Time
	snubbed   bool

	lastSend atomic.Int64

	qmu    sync.Mutex
	queue  [][]byte
	wake   chan struct{}
	done   chan struct{}
	closed sync.Once
}

func newPeerConn(t *Torrent, conn net.Conn, ap netip.AddrPort, hs peer.Handshake, outgoing bool) *peerConn {
	now := time.Now()
	p := &peerConn{
		t:           t,
		conn:        conn,
		addr:        ap,
		outgoing:    outgoing,
		id:          hs.PeerID,
		client:      clientFromID(hs.PeerID),
		ext:         hs.Extensions(),
		since:       now,
		amChoking:   true,
		peerChoking: true,
		reqs:        map[blockKey]time.Time{},
		reqq:        250,
		extIDs:      map[string]byte{},
		lastBlock:   now,
		lastRecv:    now,
		wake:        make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	p.lastSend.Store(now.UnixNano())

	return p
}

func (t *Torrent) dial(ctx context.Context, ap netip.AddrPort) {
	d := net.Dialer{
		Timeout: dialTimeout,
	}

	conn, err := d.DialContext(ctx, "tcp", ap.String())

	var hs peer.Handshake

	if err == nil {
		_ = conn.SetDeadline(time.Now().Add(shakeTimeout))

		_, err = conn.Write(peer.NewHandshake(t.hash, t.s.peerID).Bytes())
		if err == nil {
			hs, err = peer.ReadHandshake(conn)
		}

		if err == nil && (hs.InfoHash != t.hash || hs.PeerID == t.s.peerID) {
			err = fmt.Errorf("wrong torrent")
		}
	}

	t.mu.Lock()
	t.halfOpen--

	if err != nil || t.cancel == nil || ctx.Err() != nil {
		c := t.cands[ap]
		if c != nil && err != nil {
			c.fails++
		}

		t.mu.Unlock()

		if conn != nil {
			conn.Close()
		}

		return
	}

	t.cands[ap].fails = 0

	p := newPeerConn(t, conn, ap, hs, true)
	t.addPeer(p)
	t.mu.Unlock()

	p.run()
}

// accept takes a connection that has sent its handshake for this torrent.
func (t *Torrent) accept(conn net.Conn, hs peer.Handshake) {
	ap, _ := netip.ParseAddrPort(conn.RemoteAddr().String())

	t.mu.Lock()

	if t.cancel == nil || t.state == Checking || len(t.peers) >= maxPeers {
		t.mu.Unlock()
		conn.Close()

		return
	}

	_, err := conn.Write(peer.NewHandshake(t.hash, t.s.peerID).Bytes())
	if err != nil {
		t.mu.Unlock()
		conn.Close()

		return
	}

	p := newPeerConn(t, conn, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()), hs, false)
	t.addPeer(p)
	t.mu.Unlock()

	p.run()
}

func (t *Torrent) addPeer(p *peerConn) {
	_ = p.conn.SetDeadline(time.Time{})
	t.peers[p] = struct{}{}

	if p.ext {
		p.sendExtHandshake()
	}

	if t.meta.Info != nil {
		p.have = newBitfield(len(t.info().Pieces))

		if t.have.count() > 0 {
			p.send(peer.Encode(peer.Bitfield, t.have))
		}
	}

	go p.writer()
}

func (t *Torrent) removePeer(p *peerConn) {
	if _, ok := t.peers[p]; !ok {
		return
	}

	delete(t.peers, p)
	t.releaseRequests(p)

	if p.have != nil && t.avail != nil {
		for i := range t.avail {
			if p.have.get(i) {
				t.avail[i]--
			}
		}
	}

	if t.optimist == p {
		t.optimist = nil
	}
}

func (p *peerConn) sendExtHandshake() {
	t := p.t
	m := map[string]any{"ut_metadata": extMetadata}

	if t.meta.Info == nil || !t.meta.Info.Private {
		m["ut_pex"] = extPex
	}

	d := map[string]any{
		"m":    m,
		"v":    clientName,
		"reqq": 500,
	}

	if t.meta.InfoRaw != nil {
		d["metadata_size"] = len(t.meta.InfoRaw)
	}

	port := t.s.port()
	if port != 0 {
		d["p"] = port
	}

	p.send(peer.EncodeExtended(0, bencode.Encode(d)))
}

func (p *peerConn) send(msg []byte) {
	p.qmu.Lock()
	p.queue = append(p.queue, msg)
	p.qmu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *peerConn) queued() int {
	p.qmu.Lock()
	defer p.qmu.Unlock()

	return len(p.queue)
}

func (p *peerConn) close() {
	p.closed.Do(func() {
		close(p.done)
		p.conn.Close()
	})
}

func (p *peerConn) writer() {
	w := bufio.NewWriterSize(p.conn, 64<<10)

	for {
		select {
		case <-p.wake:
		case <-p.done:
			return
		}

		p.qmu.Lock()
		q := p.queue
		p.queue = nil
		p.qmu.Unlock()

		_ = p.conn.SetWriteDeadline(time.Now().Add(time.Minute))

		for _, m := range q {
			_, _ = w.Write(m)
		}

		err := w.Flush()
		if err != nil {
			p.close()
			return
		}

		p.lastSend.Store(time.Now().UnixNano())
	}
}

// run reads messages until the connection ends.
func (p *peerConn) run() {
	t := p.t
	r := bufio.NewReaderSize(p.conn, 64<<10)
	buf := make([]byte, peer.BlockSize+64)

	for {
		_ = p.conn.SetReadDeadline(time.Now().Add(3 * time.Minute))

		msg, err := peer.Read(r, buf)
		if err != nil {
			break
		}

		t.mu.Lock()

		if t.cancel == nil {
			t.mu.Unlock()
			break
		}

		p.lastRecv = time.Now()
		job, err := p.handle(msg)
		t.mu.Unlock()

		if err != nil {
			break
		}

		if job != nil {
			job()
		}
	}

	t.mu.Lock()
	t.removePeer(p)
	t.mu.Unlock()
	p.close()
}

// handle acts on one message under the torrent's lock, and returns work to
// do once the lock is released.
func (p *peerConn) handle(msg *peer.Message) (func(), error) {
	t := p.t

	if msg == nil {
		return nil, nil
	}

	switch msg.ID {
	case peer.Choke:
		p.peerChoking = true
		t.releaseRequests(p)
	case peer.Unchoke:
		p.peerChoking = false
		t.fillRequests(p)
	case peer.Interested:
		p.peerInterested = true

		if t.unchoked() < unchokeSlots {
			p.setChoking(false)
		}
	case peer.NotInterested:
		p.peerInterested = false
	case peer.Have:
		i, err := peer.ParseHave(msg.Payload)
		if err != nil {
			return nil, err
		}

		if p.have == nil {
			if len(p.rawHaves) < 1<<16 {
				p.rawHaves = append(p.rawHaves, i)
			}

			return nil, nil
		}

		p.gotHave(i)
		t.fillRequests(p)
	case peer.Bitfield:
		if p.have == nil {
			p.rawBits = append([]byte(nil), msg.Payload...)
			return nil, nil
		}

		if len(msg.Payload) != len(p.have) {
			return nil, fmt.Errorf("bitfield of the wrong size")
		}

		bits := bitfield(msg.Payload)

		for i := range t.info().Pieces {
			if bits.get(i) {
				p.gotHave(i)
			}
		}

		t.fillRequests(p)
	case peer.Request:
		return p.request(msg.Payload)
	case peer.Piece:
		i, begin, data, err := peer.ParsePiece(msg.Payload)
		if err != nil {
			return nil, err
		}

		return p.block(i, begin, data), nil
	case peer.Extended:
		return p.extended(msg.Payload)
	}

	return nil, nil
}

func (p *peerConn) gotHave(i int) {
	t := p.t
	if i < 0 || i >= len(t.avail) || p.have.get(i) {
		return
	}

	p.have.set(i)
	p.haveCount++
	t.avail[i]++

	if !p.amInterested && t.pieceWant[i] && !t.have.get(i) {
		p.setInterested(true)
	}
}

// infoArrived turns what the peer said before the info was known into
// a bitfield.
func (p *peerConn) infoArrived() {
	t := p.t
	p.have = newBitfield(len(t.info().Pieces))

	if len(p.rawBits) == len(p.have) {
		bits := bitfield(p.rawBits)

		for i := range t.info().Pieces {
			if bits.get(i) {
				p.gotHave(i)
			}
		}
	}

	for _, i := range p.rawHaves {
		p.gotHave(i)
	}

	p.rawBits = nil
	p.rawHaves = nil
}

func (p *peerConn) seed() bool {
	return p.have != nil && p.haveCount == len(p.t.info().Pieces)
}

func (p *peerConn) has(i int) bool { return p.have != nil && p.have.get(i) }

func (p *peerConn) setChoking(choke bool) {
	if p.amChoking == choke {
		return
	}

	p.amChoking = choke

	if choke {
		p.send(peer.Encode(peer.Choke))
	} else {
		p.send(peer.Encode(peer.Unchoke))
	}
}

func (p *peerConn) setInterested(want bool) {
	if p.amInterested == want {
		return
	}

	p.amInterested = want

	if want {
		p.send(peer.Encode(peer.Interested))
	} else {
		p.send(peer.Encode(peer.NotInterested))
	}
}

// updateInterest checks whether the peer still has a piece this side wants.
func (p *peerConn) updateInterest() {
	t := p.t

	if p.have == nil {
		return
	}

	for i := range t.pieceWant {
		if t.pieceWant[i] && !t.have.get(i) && p.have.get(i) {
			p.setInterested(true)
			return
		}
	}

	p.setInterested(false)
}

// request answers a request for a block, reading it once the lock is
// released.
func (p *peerConn) request(payload []byte) (func(), error) {
	t := p.t

	i, begin, length, err := peer.ParseRequest(payload)
	if err != nil {
		return nil, err
	}

	if p.amChoking || t.meta.Info == nil || !t.have.get(i) || length <= 0 || length > maxRequest {
		return nil, nil
	}

	if int64(begin)+int64(length) > t.info().PieceSize(i) || p.queued() > maxUploadQ {
		return nil, nil
	}

	st := t.storage
	off := int64(i)*t.info().PieceLength + int64(begin)

	return func() {
		data := make([]byte, length)

		err := st.ReadAt(data, off)
		if err != nil {
			return
		}

		p.send(peer.EncodePiece(i, begin, data))

		t.mu.Lock()
		p.up.add(length)
		t.up.add(length)
		t.uploaded += int64(length)
		t.resumeDirt = true
		t.mu.Unlock()
	}, nil
}

// block takes a block of a piece.
func (p *peerConn) block(i, begin int, data []byte) func() {
	t := p.t
	key := blockKey{
		i,
		begin,
	}

	_, asked := p.reqs[key]
	delete(p.reqs, key)

	pc := t.partials[i]
	if pc == nil || begin%peer.BlockSize != 0 || begin+len(data) > len(pc.buf) {
		return nil
	}

	b := begin / peer.BlockSize

	if begin+len(data) != min(begin+peer.BlockSize, len(pc.buf)) {
		return nil
	}

	if asked && pc.requested[b] > 0 {
		pc.requested[b]--
	}

	now := time.Now()
	p.lastBlock = now
	p.snubbed = false
	p.down.add(len(data))
	t.down.add(len(data))
	t.downloaded += int64(len(data))
	t.resumeDirt = true

	if pc.have[b] {
		return nil
	}

	copy(pc.buf[begin:], data)
	pc.have[b] = true
	pc.got++

	// endgame: the same block was asked of others too
	if pc.requested[b] > 0 {
		for q := range t.peers {
			if _, ok := q.reqs[key]; ok {
				delete(q.reqs, key)
				q.send(peer.EncodeCancel(i, begin, len(data)))
				pc.requested[b]--
			}
		}
	}

	var job func()

	if pc.got == len(pc.have) {
		delete(t.partials, i)
		t.verifying[i] = true

		job = func() {
			t.verify(i, pc)
		}
	}

	t.fillRequests(p)

	return job
}

func (p *peerConn) extended(payload []byte) (func(), error) {
	t := p.t

	if len(payload) < 1 {
		return nil, fmt.Errorf("empty extended message")
	}

	v, n, err := bencode.Decode(payload[1:])
	if err != nil {
		return nil, err
	}

	m, _ := v.(map[string]any)
	d := bencode.Dict(m)
	data := payload[1+n:]

	switch payload[0] {
	case 0:
		for name, id := range d.Dict("m") {
			n, ok := id.(int64)
			if ok && n > 0 && n < 256 {
				p.extIDs[name] = byte(n)
			} else {
				delete(p.extIDs, name)
			}
		}

		v := d.Str("v")
		if v != "" {
			p.client = v
		}

		q := d.Int("reqq")
		if q > 0 {
			p.reqq = int(min(q, 2000))
		}

		port := d.Int("p")
		if port > 0 && port < 65536 && !p.outgoing {
			t.addCandidate(netip.AddrPortFrom(p.addr.Addr(), uint16(port)))
		}

		size := d.Int("metadata_size")
		if t.meta.Info == nil && size > 0 && size <= maxMetaSize && t.metaSize == 0 {
			t.metaSize = int(size)
			pieces := (t.metaSize + metaPiece - 1) / metaPiece
			t.metaBuf = make([]byte, t.metaSize)
			t.metaHave = make([]bool, pieces)
			t.metaAsked = make([]time.Time, pieces)
		}

		if t.state == Metadata {
			t.requestMetadata()
		}
	case extMetadata:
		p.metadata(d, data)
	case extPex:
		if t.meta.Info != nil && t.meta.Info.Private {
			return nil, nil
		}

		added := []byte(d.Str("added"))
		for b := added; len(b) >= 6 && len(added)-len(b) < 50*6; b = b[6:] {
			ap := netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[:4])), uint16(b[4])<<8|uint16(b[5]))
			t.addCandidate(ap)
		}
	}

	return nil, nil
}

func (p *peerConn) metadata(d bencode.Dict, data []byte) {
	t := p.t
	piece := int(d.Int("piece"))

	switch d.Int("msg_type") {
	case 0:
		id, ok := p.extIDs["ut_metadata"]
		if !ok {
			return
		}

		raw := t.meta.InfoRaw
		if raw == nil || piece < 0 || piece*metaPiece >= len(raw) {
			p.send(peer.EncodeExtended(id, bencode.Encode(map[string]any{
				"msg_type": 2,
				"piece":    piece,
			})))

			return
		}

		chunk := raw[piece*metaPiece : min(len(raw), (piece+1)*metaPiece)]
		p.send(peer.EncodeExtended(id, bencode.Encode(map[string]any{
			"msg_type":   1,
			"piece":      piece,
			"total_size": len(raw),
		}), chunk))
	case 1:
		if t.meta.Info != nil || t.metaBuf == nil || piece < 0 || piece >= len(t.metaHave) {
			return
		}

		want := min(metaPiece, t.metaSize-piece*metaPiece)
		if len(data) != want {
			return
		}

		copy(t.metaBuf[piece*metaPiece:], data)
		t.metaHave[piece] = true

		for _, h := range t.metaHave {
			if !h {
				t.requestMetadata()
				return
			}
		}

		raw := t.metaBuf
		if sha1.Sum(raw) != t.hash {
			clear(t.metaHave)
			clear(t.metaAsked)

			return
		}

		err := t.meta.SetInfo(raw)
		if err != nil {
			t.fail(err)
			return
		}

		t.metaBuf = nil
		t.infoArrived()
	case 2:
		if piece >= 0 && piece < len(t.metaAsked) {
			t.metaAsked[piece] = time.Time{}
		}
	}
}

// requestMetadata asks peers for the pieces of the info not yet here.
func (t *Torrent) requestMetadata() {
	if t.metaBuf == nil {
		return
	}

	var askable []*peerConn

	for p := range t.peers {
		if _, ok := p.extIDs["ut_metadata"]; ok {
			askable = append(askable, p)
		}
	}

	if len(askable) == 0 {
		return
	}

	now := time.Now()
	n := 0

	for i, h := range t.metaHave {
		if h || now.Sub(t.metaAsked[i]) < 5*time.Second {
			continue
		}

		p := askable[(i+n)%len(askable)]
		n++
		t.metaAsked[i] = now
		p.send(peer.EncodeExtended(p.extIDs["ut_metadata"], bencode.Encode(map[string]any{
			"msg_type": 0,
			"piece":    i,
		})))
	}
}

// infoArrived sets the torrent up once a magnet link's info is here.
func (t *Torrent) infoArrived() {
	t.setupInfo()
	t.s.infoArrived(t)

	if t.cancel == nil {
		return
	}

	for p := range t.peers {
		p.infoArrived()
	}

	if t.needsCheck() {
		t.state = Checking
		go t.check()

		return
	}

	t.afterCheck()

	for p := range t.peers {
		p.updateInterest()
		t.fillRequests(p)
	}
}

func (t *Torrent) unchoked() int {
	n := 0

	for p := range t.peers {
		if !p.amChoking {
			n++
		}
	}

	return n
}

var clientCodes = map[string]string{
	"qB": "qBittorrent",
	"TR": "Transmission",
	"DE": "Deluge",
	"UT": "µTorrent",
	"UM": "µTorrent Mac",
	"LT": "libtorrent",
	"lt": "rTorrent",
	"AZ": "Vuze",
	"BI": "BiglyBT",
	"BC": "BitComet",
	"KT": "KTorrent",
	"FD": "Free Download Manager",
	"WW": "WebTorrent",
	"XB": "bt",
}

// clientFromID reads the client from an Azureus-style peer id, -qB4250-.
func clientFromID(id [20]byte) string {
	s := string(id[:8])
	if s[0] != '-' || s[7] != '-' {
		return ""
	}

	name, ok := clientCodes[s[1:3]]
	if !ok {
		return strings.Trim(s, "-")
	}

	return name + " " + strings.Join(strings.Split(s[3:7], ""), ".")
}
