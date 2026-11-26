package terminal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestParseColourReplies: the answers a terminal gives are read back as the
// colours it gave, under the keys the theme looks them up by.
func TestParseColourReplies(t *testing.T) {
	// kitty's own answer shape, in the widths it uses, for the onedark conf
	var b strings.Builder
	b.WriteString("\x1b]10;rgb:abab/b2b2/bfbf\x1b\\")
	b.WriteString("\x1b]11;rgb:2828/2c2c/3434\x1b\\")

	hexes := map[int]string{
		1:  "e0/6c/75",
		2:  "98/c3/79",
		4:  "61/af/ef",
		8:  "5c/63/70",
		10: "b5/e8/90",
		11: "ff/d7/7b",
	}
	for slot, hex := range hexes {
		fmt.Fprintf(&b, "\x1b]4;%d;rgb:%s\x1b\\", slot, hex)
	}

	got := parseColours([]byte(b.String()))
	c := got[Background]
	if c != tcell.NewRGBColor(0x28, 0x2c, 0x34) {
		t.Fatalf("background %v", c)
	}

	// OSC 4's slot 10 is bright green, not OSC 10's foreground
	c = got[10]
	if c != tcell.NewRGBColor(0xb5, 0xe8, 0x90) {
		t.Fatalf("slot 10 %v, want the bright green and not the foreground", c)
	}

	c = got[Foreground]
	if c != tcell.NewRGBColor(0xab, 0xb2, 0xbf) {
		t.Fatalf("foreground %v", c)
	}
}

// the mark that the colour answers have all arrived, in the shapes terminals
// send it: without it the reading stops early and the rest lands on the shell.
func TestDA1Reply(t *testing.T) {
	ins := []string{"\x1b[?62;c", "\x1b[?1;2c", "\x1b[?6c"}
	for _, in := range ins {
		if !da1.MatchString(in) {
			t.Fatalf("%q is a device attributes reply", in)
		}
	}

	if da1.MatchString("\x1b]11;rgb:2222/2424/3636\x1b\\") {
		t.Fatal("a colour answer is not the mark")
	}
}

// hexChannel takes an answer in whatever width the terminal chose.
func TestHexChannelWidths(t *testing.T) {
	cs := []struct {
		in   string
		want int32
	}{{
		"f",
		255,
	}, {
		"ff",
		255,
	}, {
		"fff",
		255,
	}, {
		"ffff",
		255,
	}, {
		"0",
		0,
	}, {
		"8080",
		128,
	}}
	for _, c := range cs {
		got := hexChannel(c.in)
		if got != c.want {
			t.Fatalf("%q: %d, want %d", c.in, got, c.want)
		}
	}
}
