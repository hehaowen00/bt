package torrent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bt/metainfo"
)

// What is kept between runs, under the state folder:
//
//	session.json        the torrents, their folders and choices
//	torrents/<hash>     each .torrent, written once
//	resume/<hash>.json  the pieces done, written at most every 30 s

type savedTorrent struct {
	Hash   string    `json:"hash"`
	Magnet string    `json:"magnet,omitempty"`
	Dir    string    `json:"dir"`
	Paused bool      `json:"paused,omitempty"`
	Added  time.Time `json:"added"`
	Skip   []int     `json:"skip,omitempty"` // files not wanted
}

type savedResume struct {
	Have       []byte    `json:"have"`
	Downloaded int64     `json:"downloaded"`
	Uploaded   int64     `json:"uploaded"`
	Finished   time.Time `json:"finished,omitzero"`
}

var saveMu sync.Mutex

func (s *Session) statePath(parts ...string) string {
	if s.cfg.StateDir == "" {
		return ""
	}

	return filepath.Join(append([]string{s.cfg.StateDir}, parts...)...)
}

// writeFile replaces path whole or not at all: written beside it and
// renamed over it.
func writeFile(path string, data []byte) error {
	if path == "" {
		return nil
	}

	dir := filepath.Dir(path)

	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}

	defer os.Remove(tmp.Name())

	_, err = tmp.Write(data)

	closeErr := tmp.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	return os.Rename(tmp.Name(), path)
}

func statFile(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	return st.Size(), nil
}

func (s *Session) saveSession() {
	saveMu.Lock()
	defer saveMu.Unlock()

	s.mu.Lock()

	list := make([]savedTorrent, 0, len(s.order))

	for _, h := range s.order {
		t := s.torrents[h]

		t.mu.Lock()
		st := savedTorrent{
			Hash:   h.Hex(),
			Dir:    t.dir,
			Paused: t.paused,
			Added:  t.added,
		}

		if t.meta.Info == nil {
			st.Magnet = t.meta.Magnet()
		}

		for i, w := range t.wanted {
			if !w {
				st.Skip = append(st.Skip, i)
			}
		}

		t.mu.Unlock()

		list = append(list, st)
	}

	s.mu.Unlock()

	data, err := json.MarshalIndent(list, "", "  ")
	if err == nil {
		_ = writeFile(s.statePath("session.json"), data)
	}
}

// saveTorrentFile keeps the .torrent once; it never changes.
func (s *Session) saveTorrentFile(t *Torrent) {
	path := s.statePath("torrents", t.hash.Hex()+".torrent")
	if path == "" {
		return
	}

	_, err := os.Stat(path)
	if err == nil {
		return
	}

	_ = writeFile(path, t.meta.Encode())
}

// saveResume writes which pieces are done, called with t.mu held.
func (t *Torrent) saveResume() {
	if !t.resumeDirt || t.meta.Info == nil {
		return
	}

	t.resumeDirt = false
	t.lastResume = time.Now()

	data, err := json.Marshal(savedResume{
		Have:       t.have,
		Downloaded: t.downloaded,
		Uploaded:   t.uploaded,
		Finished:   t.finished,
	})
	if err == nil {
		_ = writeFile(t.s.statePath("resume", t.hash.Hex()+".json"), data)
	}
}

func (s *Session) removeState(h metainfo.Hash) {
	if s.cfg.StateDir == "" {
		return
	}

	os.Remove(s.statePath("torrents", h.Hex()+".torrent"))
	os.Remove(s.statePath("resume", h.Hex()+".json"))
}

// load starts the torrents kept from the last run.
func (s *Session) load() error {
	path := s.statePath("session.json")
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	var list []savedTorrent

	err = json.Unmarshal(data, &list)
	if err != nil {
		return err
	}

	var firstErr error

	for _, st := range list {
		t, err := s.loadOne(st)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}

			continue
		}

		s.mu.Lock()
		s.torrents[t.hash] = t
		s.order = append(s.order, t.hash)
		s.mu.Unlock()

		t.mu.Lock()

		if !t.paused {
			t.start()
		}

		t.mu.Unlock()
	}

	return firstErr
}

func (s *Session) loadOne(st savedTorrent) (*Torrent, error) {
	var (
		m   *metainfo.Torrent
		err error
	)

	data, ferr := os.ReadFile(s.statePath("torrents", st.Hash+".torrent"))
	if ferr == nil {
		m, err = metainfo.Load(data)
	} else {
		link := st.Magnet
		if link == "" {
			link = "magnet:?xt=urn:btih:" + st.Hash
		}

		m, err = metainfo.ParseMagnet(link)
	}

	if err != nil {
		return nil, err
	}

	t := s.newTorrent(m, st.Dir)
	t.added = st.Added
	t.paused = st.Paused

	if m.Info == nil {
		return t, nil
	}

	for _, i := range st.Skip {
		if i >= 0 && i < len(t.wanted) {
			t.wanted[i] = false
		}
	}

	t.updateWant()

	data, err = os.ReadFile(s.statePath("resume", st.Hash+".json"))
	if err != nil {
		return t, nil
	}

	var r savedResume

	if json.Unmarshal(data, &r) == nil && len(r.Have) == len(t.have) {
		t.have = r.Have
		t.downloaded = r.Downloaded
		t.uploaded = r.Uploaded
		t.finished = r.Finished
	}

	return t, nil
}
