package torrent

import (
	"math"
	"math/rand/v2"
	"time"

	"bt/peer"
)

const (
	randomFirst = 4
	minDepth    = 4
	maxDepth    = 256
	endgameDups = 2
)

// depth is how many requests to keep out to p: two seconds at its rate.
func (p *peerConn) depth() int {
	d := int(p.down.get() * 2 / peer.BlockSize)
	return max(minDepth, min(d, maxDepth, p.reqq))
}

func (t *Torrent) blockLen(i, begin int) int {
	return int(min(int64(peer.BlockSize), t.info().PieceSize(i)-int64(begin)))
}

// fillRequests tops up the requests out to p.
func (t *Torrent) fillRequests(p *peerConn) {
	if t.state != Downloading || p.have == nil {
		return
	}

	if p.peerChoking || !p.amInterested {
		return
	}

	var batch []byte

	now := time.Now()
	endgame := -1

	for len(p.reqs) < p.depth() {
		k, ok := t.pick(p, &endgame)
		if !ok {
			break
		}

		p.reqs[k] = now
		t.partials[k.piece].requested[k.begin/peer.BlockSize]++
		batch = append(batch, peer.EncodeRequest(k.piece, k.begin, t.blockLen(k.piece, k.begin))...)
	}

	if len(batch) > 0 {
		p.send(batch)
	}
}

// pick chooses the next block to ask p for. endgame caches, for one
// fillRequests, whether every missing piece is started (-1 unknown).
func (t *Torrent) pick(p *peerConn, endgame *int) (blockKey, bool) {
	for i, pc := range t.partials {
		if !p.has(i) {
			continue
		}

		for b, got := range pc.have {
			if !got && pc.requested[b] == 0 {
				return blockKey{
					i,
					b * peer.BlockSize,
				}, true
			}
		}
	}

	if t.roomForPartial() {
		i := t.choosePiece(p)
		if i >= 0 {
			size := t.info().PieceSize(i)
			blocks := int((size + peer.BlockSize - 1) / peer.BlockSize)
			t.partials[i] = &partial{
				buf:       make([]byte, size),
				have:      make([]bool, blocks),
				requested: make([]uint8, blocks),
			}

			return blockKey{
				i,
				0,
			}, true
		}
	}

	if *endgame < 0 {
		*endgame = 0

		if t.allStarted() {
			*endgame = 1
		}
	}

	if *endgame == 0 {
		return blockKey{}, false
	}

	for i, pc := range t.partials {
		if !p.has(i) {
			continue
		}

		for b, got := range pc.have {
			k := blockKey{
				i,
				b * peer.BlockSize,
			}
			_, mine := p.reqs[k]
			if !got && !mine && pc.requested[b] < endgameDups {
				return k, true
			}
		}
	}

	return blockKey{}, false
}

// roomForPartial bounds the pieces held in memory: twice the peers sending,
// and no more than maxPartialBytes.
func (t *Torrent) roomForPartial() bool {
	sending := 0

	for p := range t.peers {
		if !p.peerChoking {
			sending++
		}
	}

	limit := max(8, 2*sending)
	limit = min(limit, int(max(1, maxPartialBytes/t.info().PieceLength)))

	return len(t.partials) < limit
}

// choosePiece picks a new piece p has: random for the first few, then the
// rarest, ties broken at random.
func (t *Torrent) choosePiece(p *peerConn) int {
	random := t.have.count() < randomFirst
	best := -1
	bestAvail := math.MaxInt
	ties := 0

	for i, want := range t.pieceWant {
		if !want || t.have.get(i) || !p.has(i) || t.partials[i] != nil || t.verifying[i] {
			continue
		}

		a := t.avail[i]

		if random {
			a = 0
		}

		switch {
		case a < bestAvail:
			best = i
			bestAvail = a
			ties = 1
		case a == bestAvail:
			ties++
			if rand.IntN(ties) == 0 {
				best = i
			}
		}
	}

	return best
}

// allStarted reports whether every wanted missing piece is in progress.
func (t *Torrent) allStarted() bool {
	for i, want := range t.pieceWant {
		if want && !t.have.get(i) && t.partials[i] == nil && !t.verifying[i] {
			return false
		}
	}

	return true
}

// releaseRequests forgets what was asked of p, so others are asked instead.
func (t *Torrent) releaseRequests(p *peerConn) {
	for k := range p.reqs {
		pc := t.partials[k.piece]
		if pc != nil && pc.requested[k.begin/peer.BlockSize] > 0 {
			pc.requested[k.begin/peer.BlockSize]--
		}
	}

	clear(p.reqs)
	t.dropIdlePartials()
}

// dropIdlePartials frees pieces nothing has arrived for and nobody is asked
// for, such as those only a peer that left had.
func (t *Torrent) dropIdlePartials() {
	for i, pc := range t.partials {
		if pc.got > 0 {
			continue
		}

		idle := true

		for _, r := range pc.requested {
			if r > 0 {
				idle = false
				break
			}
		}

		if idle {
			delete(t.partials, i)
		}
	}
}
