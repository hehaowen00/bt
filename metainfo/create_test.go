package metainfo

import (
	"bytes"
	"context"
	"crypto/sha1"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "share")
	sizes := map[string]int{
		"a.bin":      40000,
		"b/empty":    0,
		"b/c.bin":    70001,
		"b/d/e.bin":  5,
		"z-last.bin": 16384,
	}

	var all bytes.Buffer

	rng := rand.New(rand.NewPCG(1, 2))

	names := []string{"a.bin", "b/c.bin", "b/d/e.bin", "b/empty", "z-last.bin"}
	for _, name := range names {
		data := make([]byte, sizes[name])
		for i := range data {
			data[i] = byte(rng.UintN(256))
		}

		all.Write(data)

		p := filepath.Join(root, filepath.FromSlash(name))

		err := os.MkdirAll(filepath.Dir(p), 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, data, 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	m, err := Create(context.Background(), CreateOptions{
		Path: root,
		Trackers: [][]string{
			{
				"udp://tracker.example:1337/announce",
			},
			{
				"https://tracker.example/announce",
			},
		},
		Private: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Load(m.Encode())
	if err != nil {
		t.Fatal(err)
	}

	info := got.Info
	if got.InfoHash != m.InfoHash || !info.Multi || !info.Private || info.Name != "share" || len(got.Trackers) != 2 {
		t.Fatalf("got %+v, info %+v", got, info)
	}

	if len(info.Files) != 5 || info.Files[2].Path[1] != "d" || info.Length != int64(all.Len()) {
		t.Fatalf("files %+v", info.Files)
	}

	for i, h := range info.Pieces {
		off := int64(i) * info.PieceLength
		if sha1.Sum(all.Bytes()[off:off+info.PieceSize(i)]) != h {
			t.Fatalf("piece %d hash differs", i)
		}
	}
}

func TestPieceLengthFor(t *testing.T) {
	cs := []struct {
		total, want int64
	}{
		{
			1,
			16 << 10,
		},
		{
			700 << 20,
			512 << 10,
		},
		{
			8 << 30,
			4 << 20,
		},
		{
			1 << 50,
			16 << 20,
		},
	}
	for _, c := range cs {
		got := PieceLengthFor(c.total)
		if got != c.want {
			t.Errorf("PieceLengthFor(%d) = %d, want %d", c.total, got, c.want)
		}
	}
}
