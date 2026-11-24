package torrent

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"bt/metainfo"
)

const maxOpenFiles = 32

// storage maps the torrent's byte stream onto its files. Only whole,
// verified pieces are written, so each byte is written once.
type storage struct {
	dir  string
	info *metainfo.Info

	mu    sync.Mutex
	open  map[int]*os.File
	write map[int]bool // opened for writing
	order []int        // oldest first, to close when too many are open
}

func newStorage(dir string, info *metainfo.Info) *storage {
	return &storage{
		dir:   dir,
		info:  info,
		open:  map[int]*os.File{},
		write: map[int]bool{},
	}
}

func (s *storage) file(i int, forWrite bool) (*os.File, error) {
	f := s.open[i]
	if f != nil && (s.write[i] || !forWrite) {
		return f, nil
	}

	if f != nil {
		s.closeFile(i)
	}

	path := s.info.DiskPath(s.dir, s.info.Files[i])

	var err error

	if forWrite {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			return nil, err
		}

		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	} else {
		f, err = os.Open(path)
	}

	if err != nil {
		return nil, err
	}

	if len(s.open) >= maxOpenFiles {
		s.closeFile(s.order[0])
	}

	s.open[i] = f
	s.write[i] = forWrite
	s.order = append(s.order, i)

	return f, nil
}

func (s *storage) closeFile(i int) {
	s.open[i].Close()
	delete(s.open, i)
	delete(s.write, i)
	s.order = slices.DeleteFunc(s.order, func(j int) bool {
		return j == i
	})
}

// span calls fn for each file range under [off, off+n).
func (s *storage) span(off, n int64, fn func(file int, fileOff int64, lo, hi int64) error) error {
	for i, f := range s.info.Files {
		if f.Offset+f.Length <= off || f.Length == 0 {
			continue
		}

		if f.Offset >= off+n {
			break
		}

		lo := max(off, f.Offset)
		hi := min(off+n, f.Offset+f.Length)

		err := fn(i, lo-f.Offset, lo-off, hi-off)
		if err != nil {
			return err
		}
	}

	return nil
}

// WritePiece writes piece i.
func (s *storage) WritePiece(i int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.span(int64(i)*s.info.PieceLength, int64(len(data)), func(fi int, fileOff, lo, hi int64) error {
		if s.info.Files[fi].IsPad() {
			return nil
		}

		f, err := s.file(fi, true)
		if err != nil {
			return err
		}

		_, err = f.WriteAt(data[lo:hi], fileOff)

		return err
	})
}

// ReadAt reads the torrent's bytes at off; padding reads as zeros.
func (s *storage) ReadAt(p []byte, off int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.span(off, int64(len(p)), func(fi int, fileOff, lo, hi int64) error {
		if s.info.Files[fi].IsPad() {
			clear(p[lo:hi])
			return nil
		}

		f, err := s.file(fi, false)
		if err != nil {
			return err
		}

		_, err = f.ReadAt(p[lo:hi], fileOff)
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}

		return err
	})
}

// anyExists reports whether some file is already on disk.
func (s *storage) anyExists() bool {
	for _, f := range s.info.Files {
		if f.IsPad() {
			continue
		}

		_, err := os.Stat(s.info.DiskPath(s.dir, f))
		if err == nil {
			return true
		}
	}

	return false
}

func (s *storage) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, f := range s.open {
		f.Close()
	}

	s.open = map[int]*os.File{}
	s.write = map[int]bool{}
	s.order = nil
}

// remove deletes the torrent's files and the folders left empty.
func (s *storage) remove() {
	s.Close()

	for _, f := range s.info.Files {
		if !f.IsPad() {
			os.Remove(s.info.DiskPath(s.dir, f))
		}
	}

	if !s.info.Multi {
		return
	}

	root := filepath.Join(s.dir, s.info.Name)

	var dirs []string

	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}

		return nil
	})

	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // fails, as wanted, when not empty
	}
}
