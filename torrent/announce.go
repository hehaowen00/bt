package torrent

import (
	"context"
	"time"

	"bt/tracker"
)

const (
	minAnnounce = time.Minute
	maxAnnounce = 30 * time.Minute
	numWant     = 80
)

func (t *Torrent) setTrackers(tiers [][]string) {
	seen := map[string]bool{}
	t.trackers = nil

	for tier, urls := range tiers {
		for _, u := range urls {
			if seen[u] {
				continue
			}

			seen[u] = true
			t.trackers = append(t.trackers, &trackerState{
				url:  u,
				tier: tier,
			})
		}
	}
}

func (t *Torrent) left() int64 {
	if t.meta.Info == nil {
		return 1 << 30
	}

	info := t.info()

	var left int64

	for i, want := range t.pieceWant {
		if want && !t.have.get(i) {
			left += info.PieceSize(i)
		}
	}

	return left
}

func (t *Torrent) announceReq() tracker.Request {
	return tracker.Request{
		InfoHash:   t.hash,
		PeerID:     t.s.peerID,
		Port:       t.s.port(),
		Uploaded:   t.uploaded,
		Downloaded: t.downloaded,
		Left:       t.left(),
		NumWant:    numWant,
		Key:        t.s.key,
	}
}

// announce starts an announce to each tracker that is due.
func (t *Torrent) announce(now time.Time) {
	for _, tr := range t.trackers {
		if tr.busy || now.Before(tr.next) {
			continue
		}

		req := t.announceReq()

		switch {
		case !tr.started:
			req.Event = tracker.EventStarted
		case t.state == Seeding && !t.finished.IsZero() && tr.lastOK.Before(t.finished):
			req.Event = tracker.EventCompleted
		}

		if t.state == Seeding {
			req.NumWant = 0
		}

		tr.busy = true
		tr.status = strAnnouncing

		go t.announceOne(t.ctx, tr, req)
	}
}

func (t *Torrent) announceOne(ctx context.Context, tr *trackerState, req tracker.Request) {
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	resp, err := tracker.Announce(actx, tr.url, req)
	cancel()

	t.mu.Lock()
	defer t.mu.Unlock()

	if ctx.Err() != nil {
		return
	}

	tr.busy = false
	now := time.Now()

	if err != nil {
		tr.fails++
		tr.err = err.Error()
		tr.status = strTrackerError
		tr.next = now.Add(min(maxAnnounce, 30*time.Second*time.Duration(1<<min(tr.fails, 6))))

		return
	}

	tr.fails = 0
	tr.err = ""
	tr.started = true
	tr.lastOK = now
	tr.status = strTrackerOK
	tr.peers = len(resp.Peers)
	tr.seeders = resp.Seeders
	tr.leechers = resp.Leechers

	interval := resp.Interval

	if t.wantsPeers() && resp.MinInterval > 0 {
		interval = resp.MinInterval
	}

	tr.next = now.Add(max(minAnnounce, min(interval, maxAnnounce)))

	for _, ap := range resp.Peers {
		t.addCandidate(ap)
	}

	t.connectMore(now)
}

// announceStopped tells a tracker this side has gone, giving up quickly.
func (s *Session) announceStopped(req tracker.Request, url string) {
	req.Event = tracker.EventStopped
	req.NumWant = 0

	defer s.stopping.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _ = tracker.Announce(ctx, url, req)
}
