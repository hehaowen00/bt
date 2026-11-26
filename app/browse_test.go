package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func browseNames(b *browser) string {
	var out []string

	for _, en := range b.entries {
		out = append(out, en.name)
	}

	return strings.Join(out, " ")
}

// TestBrowse: the browser lists folders then .torrent files, goes in and
// out of folders, shows dotfiles on I, and hands back the file picked.
func TestBrowse(t *testing.T) {
	a, s := newTestApp(t, 120, 30)
	dir := t.TempDir()

	files := []string{
		"b.torrent",
		"A.TORRENT",
		"notes.txt",
		".hidden.torrent",
		"sub/inner.torrent",
	}

	for _, f := range files {
		p := filepath.Join(dir, f)

		err := os.MkdirAll(filepath.Dir(p), 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, nil, 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	var picked string

	a.openBrowser(dir+"/", func(path string) {
		picked = path
	})

	if a.browse == nil {
		t.Fatalf("browser not open: %q", a.cmd.msg)
	}

	got := browseNames(a.browse)
	if got != ".. sub A.TORRENT b.torrent" {
		t.Fatalf("listing is %q", got)
	}

	a.draw()

	got = screenText(s)
	if !strings.Contains(got, strBrowse) || !strings.Contains(got, "sub/") {
		t.Errorf("screen lacks the browser:\n%s", got)
	}

	pressKey(a, tcell.KeyEnter, 0)

	got = browseNames(a.browse)
	if got != ".. inner.torrent" {
		t.Fatalf("in sub, listing is %q", got)
	}

	press(a, "-")

	if a.browse.dir != dir || a.browse.entries[a.browse.sel].name != "sub" {
		t.Fatalf("up went to %q, cursor on %d", a.browse.dir, a.browse.sel)
	}

	press(a, "I")

	got = browseNames(a.browse)
	if got != ".. sub .hidden.torrent A.TORRENT b.torrent" {
		t.Fatalf("with hidden files, listing is %q", got)
	}

	press(a, "G")
	pressKey(a, tcell.KeyEnter, 0)

	if a.browse != nil || picked != filepath.Join(dir, "b.torrent") {
		t.Errorf("picked %q, browser open %v", picked, a.browse != nil)
	}
}

// TestBrowseFromPrompt: Ctrl-O in the add prompt opens the browser at the
// folder typed.
func TestBrowseFromPrompt(t *testing.T) {
	a, _ := newTestApp(t, 100, 30)
	dir := t.TempDir()

	press(a, "a")
	a.cmd.paste(dir)
	pressKey(a, tcell.KeyCtrlO, 0)

	if a.browse == nil || a.browse.dir != dir || a.cmd.prompt != nil {
		t.Fatalf("browser not open at %q: %+v", dir, a.browse)
	}

	pressKey(a, tcell.KeyEscape, 0)

	if a.browse != nil {
		t.Error("Esc left the browser open")
	}
}
