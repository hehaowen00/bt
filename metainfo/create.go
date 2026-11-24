package metainfo

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"bt/bencode"
)

const (
	minPieceLength = 16 << 10
	maxPieceLength = 16 << 20
	targetPieces   = 2048
	maxHashBuffers = 64 << 20
)

// CreateOptions describes a torrent to make from files on disk.
type CreateOptions struct {
	Path     string // a file, or a folder for a multi-file torrent
	Trackers [][]string
	Private  bool
	Comment  string
}

type diskFile struct {
	path   string
	rel    []string
	length int64
}

// Create hashes the files at opts.Path and returns the torrent for them.
// progress, when set, is called after each piece is read.
func Create(ctx context.Context, opts CreateOptions, progress func(done, total int)) (*Torrent, error) {
	root, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, err
	}

	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}

	files, err := listFiles(root, st)
	if err != nil {
		return nil, err
	}

	var total int64

	for _, f := range files {
		total += f.length
	}

	if total == 0 {
		return nil, fmt.Errorf("%s has nothing to share", filepath.Base(root))
	}

	pieceLen := PieceLengthFor(total)

	pieces, err := hashFiles(ctx, files, total, pieceLen, progress)
	if err != nil {
		return nil, err
	}

	name := filepath.Base(root)
	info := map[string]any{
		"name":         name,
		"piece length": pieceLen,
		"pieces":       pieces,
	}

	if st.IsDir() {
		list := make([]any, len(files))

		for i, f := range files {
			list[i] = map[string]any{
				"length": f.length,
				"path":   f.rel,
			}
		}

		info["files"] = list
	} else {
		info["length"] = total
	}

	if opts.Private {
		info["private"] = 1
	}

	t := &Torrent{
		Trackers: opts.Trackers,
		Comment:  opts.Comment,
	}
	raw := bencode.Encode(info)
	t.InfoHash = sha1.Sum(raw)

	err = t.SetInfo(raw)
	if err != nil {
		return nil, err
	}

	return t, nil
}

// PieceLengthFor is the smallest power of two from 16 KiB to 16 MiB that
// keeps a torrent of total bytes at targetPieces or fewer.
func PieceLengthFor(total int64) int64 {
	n := int64(minPieceLength)
	for n < maxPieceLength && (total+n-1)/n > targetPieces {
		n <<= 1
	}

	return n
}

func listFiles(root string, st fs.FileInfo) ([]diskFile, error) {
	if !st.IsDir() {
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a file or folder", filepath.Base(root))
		}

		return []diskFile{{
			path:   root,
			rel:    []string{st.Name()},
			length: st.Size(),
		}}, nil
	}

	var files []diskFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		fi, err := os.Stat(path)
		if err != nil {
			return err
		}

		if !fi.Mode().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		files = append(files, diskFile{
			path:   path,
			rel:    strings.Split(filepath.ToSlash(rel), "/"),
			length: fi.Size(),
		})

		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("%s has no files", filepath.Base(root))
	}

	return files, nil
}

type hashJob struct {
	i   int
	buf []byte
}

// hashFiles reads the files front to back into piece buffers, which a few
// goroutines hash, and returns the concatenated piece hashes.
func hashFiles(
	ctx context.Context,
	files []diskFile,
	total, pieceLen int64,
	progress func(done, total int),
) ([]byte, error) {
	n := int((total + pieceLen - 1) / pieceLen)
	out := make([]byte, n*sha1.Size)

	bufs := max(2, min(int(maxHashBuffers/pieceLen), 2*runtime.GOMAXPROCS(0)))
	workers := max(1, min(runtime.GOMAXPROCS(0), bufs-1))

	free := make(chan []byte, bufs)

	for range bufs {
		free <- make([]byte, pieceLen)
	}

	jobs := make(chan hashJob)
	done := make(chan struct{})

	for range workers {
		go func() {
			for j := range jobs {
				h := sha1.Sum(j.buf)
				copy(out[j.i*sha1.Size:], h[:])
				free <- j.buf[:cap(j.buf)]
			}

			done <- struct{}{}
		}()
	}

	err := readPieces(ctx, files, n, total, pieceLen, free, jobs, progress)

	close(jobs)

	for range workers {
		<-done
	}

	if err != nil {
		return nil, err
	}

	return out, nil
}

func readPieces(
	ctx context.Context,
	files []diskFile,
	n int,
	total, pieceLen int64,
	free chan []byte,
	jobs chan hashJob,
	progress func(done, total int),
) error {
	var (
		f    *os.File
		fi   int
		left int64
	)

	defer func() {
		if f != nil {
			f.Close()
		}
	}()

	if len(files) > 0 {
		left = files[0].length
	}

	fill := func(b []byte) error {
		for len(b) > 0 {
			for left == 0 {
				if f != nil {
					f.Close()
					f = nil
				}

				fi++
				left = files[fi].length
			}

			if f == nil {
				var err error

				f, err = os.Open(files[fi].path)
				if err != nil {
					return err
				}
			}

			k := int(min(int64(len(b)), left))

			_, err := io.ReadFull(f, b[:k])
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return fmt.Errorf("%s changed while it was read", files[fi].path)
			}

			if err != nil {
				return err
			}

			b = b[k:]
			left -= int64(k)
		}

		return nil
	}

	for i := range n {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		buf := <-free
		size := min(pieceLen, total-int64(i)*pieceLen)
		buf = buf[:size]

		err := fill(buf)
		if err != nil {
			return err
		}

		jobs <- hashJob{
			i,
			buf,
		}

		if progress != nil {
			progress(i+1, n)
		}
	}

	return nil
}
