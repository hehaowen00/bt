// Package metainfo reads .torrent files, info dictionaries and magnet links.
package metainfo

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"bt/bencode"
)

type Hash [20]byte

func (h Hash) Hex() string { return hex.EncodeToString(h[:]) }

// File is one file of a torrent, at Offset in the torrent's byte stream.
type File struct {
	Path   []string // under the torrent's name for a multi-file torrent
	Length int64
	Offset int64
}

// Info is the info dictionary.
type Info struct {
	Name        string
	PieceLength int64
	Pieces      []Hash
	Files       []File
	Length      int64
	Private     bool
	Multi       bool // files live in a folder named Name
}

// Torrent is a .torrent file, or what a magnet link knows before the info
// arrives.
type Torrent struct {
	InfoHash Hash
	InfoRaw  []byte // nil until known, for a magnet
	Info     *Info
	Trackers [][]string // tiers
	Name     string     // the magnet's dn, until Info arrives
	Peers    []string   // x.pe from a magnet
	Comment  string
}

// Load reads a .torrent file's bytes.
func Load(data []byte) (*Torrent, error) {
	v, err := bencode.DecodeAll(data)
	if err != nil {
		return nil, err
	}

	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a torrent file")
	}

	d := bencode.Dict(m)

	raw, err := bencode.RawValue(data, "info")
	if err != nil {
		return nil, errors.New("not a torrent file: no info dictionary")
	}

	t := &Torrent{
		InfoHash: sha1.Sum(raw),
		Comment:  d.Str("comment"),
	}

	err = t.SetInfo(raw)
	if err != nil {
		return nil, err
	}

	for _, tier := range d.List("announce-list") {
		var urls []string

		list, _ := tier.([]any)
		for _, u := range list {
			if s, ok := u.(string); ok && s != "" {
				urls = append(urls, s)
			}
		}

		if len(urls) > 0 {
			t.Trackers = append(t.Trackers, urls)
		}
	}

	if len(t.Trackers) == 0 && d.Str("announce") != "" {
		t.Trackers = [][]string{{d.Str("announce")}}
	}

	return t, nil
}

// SetInfo checks raw against the info hash and parses it.
func (t *Torrent) SetInfo(raw []byte) error {
	if sha1.Sum(raw) != t.InfoHash {
		return errors.New("info does not match the info hash")
	}

	info, err := ParseInfo(raw)
	if err != nil {
		return err
	}

	t.InfoRaw = raw
	t.Info = info
	t.Name = info.Name

	return nil
}

// DisplayName is the best name known.
func (t *Torrent) DisplayName() string {
	if t.Name != "" {
		return t.Name
	}

	return t.InfoHash.Hex()
}

// ParseInfo reads an info dictionary.
func ParseInfo(raw []byte) (*Info, error) {
	v, err := bencode.DecodeAll(raw)
	if err != nil {
		return nil, err
	}

	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("info is not a dictionary")
	}

	d := bencode.Dict(m)
	info := &Info{
		Name:        d.Str("name"),
		PieceLength: d.Int("piece length"),
		Private:     d.Int("private") == 1,
	}

	utf8Name := d.Str("name.utf-8")
	if utf8Name != "" {
		info.Name = utf8Name
	}

	if info.PieceLength <= 0 || info.PieceLength > 1<<28 {
		return nil, fmt.Errorf("bad piece length %d", info.PieceLength)
	}

	info.Name = cleanName(info.Name)

	pieces := d.Str("pieces")
	if len(pieces)%20 != 0 || len(pieces) == 0 {
		return nil, errors.New("bad piece hashes")
	}

	info.Pieces = make([]Hash, len(pieces)/20)
	for i := range info.Pieces {
		copy(info.Pieces[i][:], pieces[i*20:])
	}

	files := d.List("files")
	if files == nil {
		n := d.Int("length")
		if n < 0 {
			return nil, errors.New("bad length")
		}

		info.Files = []File{{
			Path:   []string{info.Name},
			Length: n,
		}}
		info.Length = n
	} else {
		info.Multi = true

		for _, f := range files {
			fm, _ := f.(map[string]any)
			fd := bencode.Dict(fm)

			path := fd.List("path.utf-8")
			if path == nil {
				path = fd.List("path")
			}

			var parts []string

			for _, p := range path {
				s, _ := p.(string)
				s = cleanName(s)
				if s != "" {
					parts = append(parts, s)
				}
			}

			n := fd.Int("length")
			if len(parts) == 0 || n < 0 {
				return nil, errors.New("bad file entry")
			}

			// BEP 47 padding files are not real files
			if strings.Contains(fd.Str("attr"), "p") {
				info.Files = append(info.Files, File{
					Length: n,
					Offset: info.Length,
				})
				info.Length += n

				continue
			}

			info.Files = append(info.Files, File{
				Path:   parts,
				Length: n,
				Offset: info.Length,
			})
			info.Length += n
		}
	}

	want := (info.Length + info.PieceLength - 1) / info.PieceLength
	if want != int64(len(info.Pieces)) {
		return nil, fmt.Errorf("%d piece hashes for %d pieces", len(info.Pieces), want)
	}

	return info, nil
}

// cleanName keeps a path element from leaving the download folder.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}

		return r
	}, s)

	if s == "." || s == ".." {
		return "_"
	}

	return s
}

// IsPad reports a BEP 47 padding file, which takes room in the pieces but is
// never written.
func (f File) IsPad() bool { return f.Path == nil }

// DiskPath is where the file goes under dir.
func (info *Info) DiskPath(dir string, f File) string {
	parts := []string{dir}

	if info.Multi {
		parts = append(parts, info.Name)
	}

	return filepath.Join(append(parts, f.Path...)...)
}

// PieceSize is the length of piece i; the last is usually short.
func (info *Info) PieceSize(i int) int64 {
	if i == len(info.Pieces)-1 {
		return info.Length - int64(i)*info.PieceLength
	}

	return info.PieceLength
}

// ParseMagnet reads a magnet link.
func ParseMagnet(link string) (*Torrent, error) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "magnet" {
		return nil, errors.New("not a magnet link")
	}

	q := u.Query()
	t := &Torrent{
		Name: q.Get("dn"),
	}
	found := false

	for _, xt := range q["xt"] {
		h, ok := strings.CutPrefix(xt, "urn:btih:")
		if !ok {
			continue
		}

		var b []byte

		switch len(h) {
		case 40:
			b, err = hex.DecodeString(h)
		case 32:
			b, err = base32.StdEncoding.DecodeString(strings.ToUpper(h))
		default:
			err = errors.New("bad length")
		}

		if err != nil || len(b) != 20 {
			return nil, fmt.Errorf("bad info hash %q", h)
		}

		copy(t.InfoHash[:], b)

		found = true
	}

	if !found {
		if len(q["xt"]) > 0 {
			return nil, errors.New("only v1 (btih) magnet links are supported")
		}

		return nil, errors.New("magnet link has no info hash")
	}

	for _, tr := range q["tr"] {
		t.Trackers = append(t.Trackers, []string{tr})
	}

	t.Peers = q["x.pe"]

	return t, nil
}

// Magnet is a link for the torrent.
func (t *Torrent) Magnet() string {
	var b strings.Builder

	b.WriteString("magnet:?xt=urn:btih:" + t.InfoHash.Hex())

	n := t.DisplayName()
	if n != t.InfoHash.Hex() {
		b.WriteString("&dn=" + url.QueryEscape(n))
	}

	for _, tier := range t.Trackers {
		for _, tr := range tier {
			b.WriteString("&tr=" + url.QueryEscape(tr))
		}
	}

	return b.String()
}

// Encode writes the torrent as a .torrent file, for keeping one fetched from
// a magnet link.
func (t *Torrent) Encode() []byte {
	var b strings.Builder

	b.WriteString("d")

	if len(t.Trackers) > 0 {
		tiers := make([]any, len(t.Trackers))

		for i, tier := range t.Trackers {
			tiers[i] = tier
		}

		b.Write(bencode.Encode("announce"))
		b.Write(bencode.Encode(t.Trackers[0][0]))
		b.Write(bencode.Encode("announce-list"))
		b.Write(bencode.Encode(tiers))
	}

	if t.Comment != "" {
		b.Write(bencode.Encode("comment"))
		b.Write(bencode.Encode(t.Comment))
	}

	b.Write(bencode.Encode("info"))
	b.Write(t.InfoRaw)
	b.WriteString("e")

	return []byte(b.String())
}
