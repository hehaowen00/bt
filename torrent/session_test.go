package torrent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bt/bencode"
	"bt/metainfo"
)

// makeTorrent writes files of the given sizes under dir/name and returns a
// .torrent for them.
func makeTorrent(t *testing.T, dir, name string, pieceLen int, sizes ...int) []byte {
	t.Helper()

	var (
		stream []byte
		files  []any
	)

	for i, n := range sizes {
		data := make([]byte, n)
		_, _ = rand.Read(data)
		stream = append(stream, data...)

		path := filepath.Join(dir, name, fmt.Sprintf("file%d.bin", i))

		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, data, 0o644)
		if err != nil {
			t.Fatal(err)
		}

		files = append(files, map[string]any{
			"length": n,
			"path":   []any{fmt.Sprintf("file%d.bin", i)},
		})
	}

	var hashes []byte

	for off := 0; off < len(stream); off += pieceLen {
		h := sha1.Sum(stream[off:min(off+pieceLen, len(stream))])
		hashes = append(hashes, h[:]...)
	}

	return bencode.Encode(map[string]any{
		"info": map[string]any{
			"name":         name,
			"piece length": pieceLen,
			"pieces":       hashes,
			"files":        files,
		},
	})
}

func newTestSession(t *testing.T, dir string) *Session {
	t.Helper()

	s, err := New(Config{
		DownloadDir: dir,
		StateDir:    filepath.Join(dir, ".state"),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(s.Close)

	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(50 * time.Millisecond)
	}
}

// TestMagnetDownload: a leecher given only a magnet link with the seeder's
// address fetches the metadata and every piece, and its files match.
func TestMagnetDownload(t *testing.T) {
	seedDir := t.TempDir()
	leechDir := t.TempDir()
	data := makeTorrent(t, seedDir, "set", 32<<10, 100_000, 5, 70_000)

	tf := filepath.Join(seedDir, "set.torrent")

	err := os.WriteFile(tf, data, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	seeder := newTestSession(t, seedDir)

	_, err = seeder.Add(tf, "")
	if err != nil {
		t.Fatal(err)
	}

	m, _ := metainfo.Load(data)
	hash := m.InfoHash.Hex()

	waitFor(t, "the seeder to check its files", func() bool {
		return seeder.List()[0].State == Seeding
	})

	leecher := newTestSession(t, leechDir)
	link := fmt.Sprintf("magnet:?xt=urn:btih:%s&x.pe=127.0.0.1:%d", hash, seeder.Port())

	_, err = leecher.Add(link, "")
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the download", func() bool {
		return leecher.List()[0].State == Seeding
	})

	for i := range 3 {
		name := fmt.Sprintf("file%d.bin", i)
		want, _ := os.ReadFile(filepath.Join(seedDir, "set", name))
		got, _ := os.ReadFile(filepath.Join(leechDir, "set", name))

		if !bytes.Equal(want, got) {
			t.Errorf("%s differs: %d bytes, want %d", name, len(got), len(want))
		}
	}

	up := seeder.List()[0].Uploaded
	if up < 170_005 {
		t.Errorf("seeder uploaded %d bytes", up)
	}
}

// TestSkippedFile: a file not wanted is not downloaded, beyond the edges of
// pieces it shares with wanted files.
func TestSkippedFile(t *testing.T) {
	seedDir := t.TempDir()
	leechDir := t.TempDir()
	data := makeTorrent(t, seedDir, "set", 16<<10, 50_000, 200_000, 50_000)

	tf := filepath.Join(seedDir, "set.torrent")
	_ = os.WriteFile(tf, data, 0o644)

	seeder := newTestSession(t, seedDir)
	_, _ = seeder.Add(tf, "")

	waitFor(t, "the seeder to check its files", func() bool {
		return seeder.List()[0].State == Seeding
	})

	leecher := newTestSession(t, leechDir)
	m, _ := metainfo.Load(data)
	hash := m.InfoHash.Hex()

	_, err := leecher.Add(tf, "")
	if err != nil {
		t.Fatal(err)
	}

	err = leecher.SetWanted(hash, []int{1}, false)
	if err != nil {
		t.Fatal(err)
	}

	lt, _ := leecher.get(hash)
	lt.mu.Lock()
	lt.addCandidate(mustAddr(t, seeder.Port()))
	lt.connectMore(time.Now())
	lt.mu.Unlock()

	waitFor(t, "the wanted files", func() bool {
		return leecher.List()[0].State == Seeding
	})

	d, _ := leecher.Detail(hash)
	if d.Files[1].Done > 2*16<<10 {
		t.Errorf("skipped file has %d bytes done", d.Files[1].Done)
	}

	if d.Done != 100_000 || d.Size != 100_000 {
		t.Errorf("done %d of %d, want 100000 of 100000", d.Done, d.Size)
	}
}

func mustAddr(t *testing.T, port int) netip.AddrPort {
	t.Helper()

	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))
}

// TestSeedMade: a torrent made from files seeds them at once, without a
// check, and a leecher gets them.
func TestSeedMade(t *testing.T) {
	seedDir := t.TempDir()
	leechDir := t.TempDir()
	makeTorrent(t, seedDir, "set", 16<<10, 50_000, 0, 70_000)

	m, err := metainfo.Create(context.Background(), metainfo.CreateOptions{
		Path: filepath.Join(seedDir, "set"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	seeder := newTestSession(t, seedDir)

	_, err = seeder.Seed(m, seedDir)
	if err != nil {
		t.Fatal(err)
	}

	st := seeder.List()[0].State
	if st != Seeding {
		t.Fatalf("state %v, want seeding", st)
	}

	leecher := newTestSession(t, leechDir)
	link := fmt.Sprintf("magnet:?xt=urn:btih:%s&x.pe=127.0.0.1:%d", m.InfoHash.Hex(), seeder.Port())

	_, err = leecher.Add(link, "")
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the download", func() bool {
		return leecher.List()[0].State == Seeding
	})

	want, _ := os.ReadFile(filepath.Join(seedDir, "set", "file2.bin"))
	got, _ := os.ReadFile(filepath.Join(leechDir, "set", "file2.bin"))

	if !bytes.Equal(want, got) {
		t.Errorf("file2.bin differs: %d bytes, want %d", len(got), len(want))
	}
}
