package app

import (
	"crypto/sha1"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"bt/bencode"
	"bt/torrent"
)

func newTestApp(t *testing.T, w, h int) (*App, tcell.SimulationScreen) {
	t.Helper()

	dir := t.TempDir()

	sess, err := torrent.New(torrent.Config{
		DownloadDir: dir,
		StateDir:    filepath.Join(dir, ".state"),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(sess.Close)

	s := tcell.NewSimulationScreen("UTF-8")

	err = s.Init()
	if err != nil {
		t.Fatal(err)
	}

	s.SetSize(w, h)
	t.Cleanup(s.Fini)

	a := New(s, sess, Config{
		DownloadDir: dir,
		Theme:       DefaultTheme,
	})

	return a, s
}

func addTestTorrent(t *testing.T, a *App) {
	t.Helper()

	data := []byte(strings.Repeat("x", 40_000))
	h := sha1.Sum(data)
	tf := filepath.Join(t.TempDir(), "t.torrent")
	raw := bencode.Encode(map[string]any{
		"announce": "udp://127.0.0.1:1/announce",
		"info": map[string]any{
			"name":         "Example Set",
			"piece length": 65536,
			"pieces":       h[:],
			"files": []any{
				map[string]any{
					"length": 30_000,
					"path":   []any{"alpha.txt"},
				},
				map[string]any{
					"length": 10_000,
					"path":   []any{"beta.txt"},
				},
			},
		},
	})

	err := os.WriteFile(tf, raw, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	a.Add(tf)
	a.refresh()
}

func screenText(s tcell.SimulationScreen) string {
	cells, w, h := s.GetContents()

	var b strings.Builder

	for y := range h {
		for x := range w {
			r := cells[y*w+x].Runes
			if len(r) == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(string(r))
			}
		}

		b.WriteByte('\n')
	}

	return b.String()
}

func press(a *App, keys string) {
	for _, r := range keys {
		pressKey(a, tcell.KeyRune, r)
	}
}

func pressKey(a *App, k tcell.Key, r rune) {
	a.key(tcell.NewEventKey(k, r, tcell.ModNone))
	a.refresh()
}

// TestScreens: the list, each tab and the help draw what they should.
func TestScreens(t *testing.T) {
	a, s := newTestApp(t, 120, 30)

	a.draw()

	if got := screenText(s); !strings.Contains(got, strNoTorrents) {
		t.Fatalf("empty list missing %q:\n%s", strNoTorrents, got)
	}

	addTestTorrent(t, a)

	checks := []struct {
		keys string
		want []string
	}{
		{"", []string{"Example Set", strTorrents, strGeneral, strTrackers, lblSavedIn}},
		{"2", []string{"[x]", "alpha.txt", "beta.txt"}},
		{"3", []string{strNoPeers}},
		{"4", []string{"udp://127.0.0.1:1/announce"}},
		{"?", []string{strHelp, helpTorrents}},
	}

	for _, c := range checks {
		press(a, c.keys)
		a.draw()

		got := screenText(s)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("after %q, screen lacks %q:\n%s", c.keys, w, got)
			}
		}
	}
}

// TestSkipFile: Space in the Files tab skips the file under the cursor.
func TestSkipFile(t *testing.T) {
	a, s := newTestApp(t, 100, 30)
	addTestTorrent(t, a)

	press(a, "2")
	pressKey(a, tcell.KeyEnter, 0)
	press(a, " ")
	a.draw()

	got := screenText(s)
	if !strings.Contains(got, "[ ]") || a.detail.Files[0].Wanted {
		t.Errorf("first file still wanted:\n%s", got)
	}
}

// TestPalette: typing the start of each word finds a command, and Enter runs
// the highlighted one.
func TestPalette(t *testing.T) {
	a, s := newTestApp(t, 100, 30)

	pressKey(a, tcell.KeyCtrlP, 0)
	press(a, "th no")
	a.draw()

	if got := screenText(s); !strings.Contains(got, sprintf(cmdTheme, "nord")) {
		t.Fatalf("palette lacks the nord theme:\n%s", got)
	}

	pressKey(a, tcell.KeyEnter, 0)

	if a.cfg.Theme != "nord" {
		t.Errorf("theme is %q, want nord", a.cfg.Theme)
	}
}

// TestSmallScreens: nothing panics however small the terminal.
func TestSmallScreens(t *testing.T) {
	a, s := newTestApp(t, 80, 24)
	addTestTorrent(t, a)

	for _, size := range [][2]int{{1, 1}, {10, 3}, {30, 8}, {40, 16}, {60, 20}, {200, 60}} {
		s.SetSize(size[0], size[1])

		for _, k := range "1234" {
			press(a, string(k))
			a.draw()
		}
	}
}
