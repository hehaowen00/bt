// Package torrent runs torrents: it finds peers, downloads and checks pieces,
// writes them to disk and uploads to others.
package torrent

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"bt/dht"
	"bt/metainfo"
	"bt/peer"
)

type Config struct {
	DownloadDir string
	StateDir    string  // "" keeps nothing between runs
	Port        int     // for incoming peers; taken by another program, any free port is used
	DHT         bool    // find peers on the DHT
	SeedRatio   float64 // stop seeding at this upload ratio; 0 seeds until stopped

	// Notify is told when a torrent finishes or fails, from the engine's
	// goroutines.
	Notify func(name, msg string, isErr bool)
}

type Session struct {
	cfg    Config
	peerID [20]byte
	key    uint32
	ln     net.Listener
	dht    *dht.DHT

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	torrents map[metainfo.Hash]*Torrent
	order    []metainfo.Hash

	stopping sync.WaitGroup
}

// New starts the session and the torrents kept from the last run.
func New(cfg Config) (*Session, error) {
	s := &Session{
		cfg:      cfg,
		torrents: map[metainfo.Hash]*Torrent{},
	}

	copy(s.peerID[:], "-XB0100-")
	_, _ = rand.Read(s.peerID[8:])

	var kb [4]byte

	_, _ = rand.Read(kb[:])
	s.key = binary.BigEndian.Uint32(kb[:])
	s.ctx, s.cancel = context.WithCancel(context.Background())

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
	}

	if err == nil {
		s.ln = ln
		go s.acceptLoop()
	}

	if cfg.DHT {
		s.dht, _ = dht.New()
	}

	err = s.load()

	return s, err
}

func (s *Session) port() uint16 {
	if s.ln == nil {
		return 0
	}

	return uint16(s.ln.Addr().(*net.TCPAddr).Port)
}

// Port is the port peers can connect to, 0 when none could be opened.
func (s *Session) Port() int { return int(s.port()) }

// DHTNodes is how many DHT nodes have answered.
func (s *Session) DHTNodes() int {
	if s.dht == nil {
		return -1
	}

	return s.dht.Nodes()
}

func (s *Session) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}

			time.Sleep(100 * time.Millisecond)

			continue
		}

		go s.handshake(conn)
	}
}

func (s *Session) handshake(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(shakeTimeout))

	hs, err := peer.ReadHandshake(conn)
	if err != nil || hs.PeerID == s.peerID {
		conn.Close()
		return
	}

	s.mu.Lock()
	t := s.torrents[hs.InfoHash]
	s.mu.Unlock()

	if t == nil {
		conn.Close()
		return
	}

	t.accept(conn, hs)
}

func (s *Session) get(hash string) (*Torrent, error) {
	h, err := parseHash(hash)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	t := s.torrents[h]
	if t == nil {
		return nil, errors.New(strNoSuchTorrent)
	}

	return t, nil
}

func parseHash(s string) (metainfo.Hash, error) {
	var h metainfo.Hash

	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 20 {
		return h, errors.New(strNoSuchTorrent)
	}

	copy(h[:], b)

	return h, nil
}

var bareHash = regexp.MustCompile(`^([0-9a-fA-F]{40}|[A-Za-z2-7]{32})$`)

// Add adds a .torrent file, a magnet link or a bare info hash, saving into
// dir ("" for the default), and returns the torrent's name.
func (s *Session) Add(src, dir string) (string, error) {
	src = strings.TrimSpace(src)

	var (
		m   *metainfo.Torrent
		err error
	)

	switch {
	case strings.HasPrefix(src, "magnet:"):
		m, err = metainfo.ParseMagnet(src)
	case bareHash.MatchString(src):
		m, err = metainfo.ParseMagnet("magnet:?xt=urn:btih:" + src)
	default:
		var data []byte

		data, err = os.ReadFile(expandHome(src))
		if err == nil {
			m, err = metainfo.Load(data)
		}
	}

	if err != nil {
		return "", err
	}

	return s.add(m, dir, false)
}

// Seed adds a torrent just made from the files in dir, as complete, so they
// are not hashed a second time.
func (s *Session) Seed(m *metainfo.Torrent, dir string) (string, error) {
	return s.add(m, dir, true)
}

func (s *Session) add(m *metainfo.Torrent, dir string, complete bool) (string, error) {
	if dir == "" {
		dir = s.cfg.DownloadDir
	}

	s.mu.Lock()

	old := s.torrents[m.InfoHash]
	if old != nil {
		s.mu.Unlock()

		return old.merge(m)
	}

	t := s.newTorrent(m, expandHome(dir))
	t.added = time.Now()

	s.torrents[m.InfoHash] = t
	s.order = append(s.order, m.InfoHash)

	if m.Info != nil {
		s.saveTorrentFile(t)
	}

	t.mu.Lock()

	if complete && m.Info != nil {
		for i := range m.Info.Pieces {
			t.have.set(i)
		}

		t.finished = t.added
		t.resumeDirt = true
		t.saveResume()
	}

	t.start()
	name := t.meta.DisplayName()
	t.mu.Unlock()
	s.mu.Unlock()

	s.saveSession()

	return name, nil
}

// merge takes what a second add of the same torrent knows: the info, for
// a magnet link still waiting on it, and trackers.
func (t *Torrent) merge(m *metainfo.Torrent) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.meta.Info == nil && m.Info != nil {
		err := t.meta.SetInfo(m.InfoRaw)
		if err == nil {
			t.infoArrived()
		}
	}

	known := map[string]bool{}

	for _, tr := range t.trackers {
		known[tr.url] = true
	}

	for _, tier := range m.Trackers {
		for _, u := range tier {
			if !known[u] {
				known[u] = true
				t.meta.Trackers = append(t.meta.Trackers, []string{u})
				t.trackers = append(t.trackers, &trackerState{
					url:  u,
					tier: len(t.meta.Trackers) - 1,
				})
			}
		}
	}

	name := t.meta.DisplayName()

	return name, fmt.Errorf(strAlreadyAdded, name)
}

func (s *Session) newTorrent(m *metainfo.Torrent, dir string) *Torrent {
	t := &Torrent{
		s:     s,
		hash:  m.InfoHash,
		meta:  m,
		dir:   dir,
		peers: map[*peerConn]struct{}{},
		cands: map[netip.AddrPort]*candidate{},
	}
	t.setTrackers(m.Trackers)

	if m.Info != nil {
		t.setupInfo()
	}

	return t
}

func expandHome(p string) string {
	rest, ok := strings.CutPrefix(p, "~")
	if !ok || rest != "" && rest[0] != '/' {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	return filepath.Join(home, rest)
}

// Pause stops a torrent; Resume starts it again.
func (s *Session) Pause(hash string) error {
	t, err := s.get(hash)
	if err != nil {
		return err
	}

	t.mu.Lock()
	t.stop()
	t.paused = true
	t.mu.Unlock()
	s.saveSession()

	return nil
}

func (s *Session) Resume(hash string) error {
	t, err := s.get(hash)
	if err != nil {
		return err
	}

	t.mu.Lock()
	t.start()
	t.mu.Unlock()
	s.saveSession()

	return nil
}

// Recheck hashes the torrent's files again.
func (s *Session) Recheck(hash string) error {
	t, err := s.get(hash)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.meta.Info == nil {
		return errors.New(strNoInfoYet)
	}

	t.stop()
	t.have = newBitfield(len(t.info().Pieces))
	t.start()

	return nil
}

// Remove drops a torrent, and its files too when deleteFiles is set.
func (s *Session) Remove(hash string, deleteFiles bool) error {
	t, err := s.get(hash)
	if err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.torrents, t.hash)
	s.order = slices.DeleteFunc(s.order, func(h metainfo.Hash) bool {
		return h == t.hash
	})
	s.mu.Unlock()

	t.mu.Lock()
	t.stop()

	if deleteFiles && t.meta.Info != nil {
		t.storage.remove()
	}

	t.mu.Unlock()

	s.removeState(t.hash)
	s.saveSession()

	return nil
}

// SetWanted chooses whether files are downloaded.
func (s *Session) SetWanted(hash string, files []int, want bool) error {
	t, err := s.get(hash)
	if err != nil {
		return err
	}

	t.mu.Lock()

	if t.meta.Info == nil {
		t.mu.Unlock()
		return errors.New(strNoInfoYet)
	}

	for _, i := range files {
		if i >= 0 && i < len(t.wanted) {
			t.wanted[i] = want
		}
	}

	t.updateWant()

	if t.cancel != nil && t.state != Checking {
		t.afterCheck()

		for p := range t.peers {
			p.updateInterest()
			t.fillRequests(p)
		}
	}

	t.mu.Unlock()
	s.saveSession()

	return nil
}

// Magnet is a magnet link for the torrent.
func (s *Session) Magnet(hash string) (string, error) {
	t, err := s.get(hash)
	if err != nil {
		return "", err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.meta.Magnet(), nil
}

func (s *Session) notifyComplete(t *Torrent) {
	if s.cfg.Notify != nil {
		go s.cfg.Notify(t.meta.DisplayName(), strFinished, false)
	}

	go s.saveSession()
}

func (s *Session) notifyError(t *Torrent, msg string) {
	if s.cfg.Notify != nil {
		go s.cfg.Notify(t.meta.DisplayName(), msg, true)
	}
}

// infoArrived keeps the info fetched for a magnet link, called with t.mu
// held.
func (s *Session) infoArrived(t *Torrent) {
	s.saveTorrentFile(t)

	go s.saveSession()
}

// Close stops every torrent, tells the trackers, and keeps what is needed
// for next time.
func (s *Session) Close() {
	s.mu.Lock()
	ts := make([]*Torrent, 0, len(s.torrents))

	for _, h := range s.order {
		ts = append(ts, s.torrents[h])
	}

	s.mu.Unlock()

	for _, t := range ts {
		t.mu.Lock()
		paused := t.paused
		t.stop()
		t.paused = paused
		t.mu.Unlock()
	}

	s.saveSession()
	s.cancel()

	if s.ln != nil {
		s.ln.Close()
	}

	if s.dht != nil {
		s.dht.Close()
	}

	done := make(chan struct{})

	go func() {
		s.stopping.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}
