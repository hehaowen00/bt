// Package terminal asks the terminal what it is themed to: OSC 10, 11 and 4
// ask for the foreground, the background and the sixteen, and a terminal that
// answers hands back the real values behind the default colours and the
// palette slots. Without them there is no telling whether text on a coloured
// ground wants to be light or dark, nor a shade for the status line between
// the background and the text.
//
// The query has to happen before tcell takes the keyboard, since the reply
// arrives on the same input stream that it reads.
package terminal

import (
	"regexp"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
)

// The two defaults sit outside the numbered slots: a terminal's answer for
// its default foreground and the answer for palette slot 10 are different
// colours reported under the same digits, so they cannot share a key.
const (
	Foreground = -1
	Background = -2
)

// Colours is what the terminal said: the palette slots by number, and the
// two defaults under Foreground and Background.
type Colours map[int]tcell.Color

// oscReply matches one answer: OSC 10 and 11 are "10;rgb:..." and "11;rgb:...",
// and a palette slot is "4;7;rgb:...". The channels come back in 1, 2, 3 or 4
// hex digits, whichever the terminal likes.
var oscReply = regexp.MustCompile(`\x1b\]([0-9]{1,2});(?:([0-9]{1,2});)?` +
	`rgb:([0-9a-fA-F]+)/([0-9a-fA-F]+)/([0-9a-fA-F]+)`)

// da1 is the Primary Device Attributes reply, "\x1b[?<numbers>c", which comes
// back after the colour answers and marks the end of them.
var da1 = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// Detect asks the terminal what it is themed to and returns what it said
// within the timeout, or nil when it did not give its background, the one
// answer nothing can be derived without.
func Detect(timeout time.Duration) Colours {
	c := parseColours(query(timeout))
	if _, ok := c[Background]; !ok {
		return nil
	}

	return c
}

// parseColours reads every answer in what the terminal sent back.
func parseColours(buf []byte) Colours {
	out := Colours{}

	for _, m := range oscReply.FindAllStringSubmatch(string(buf), -1) {
		var key int

		switch m[1] {
		case "4": // a palette slot: its number is the second field
			n, err := strconv.Atoi(m[2])
			if err != nil || n > 15 {
				continue
			}

			key = n
		case "10":
			key = Foreground
		case "11":
			key = Background
		default:
			continue
		}

		out[key] = tcell.NewRGBColor(hexChannel(m[3]), hexChannel(m[4]), hexChannel(m[5]))
	}

	return out
}

// hexChannel is one channel of an "rgb:" reply, scaled to 8 bits from however
// many digits the terminal used: ffff, ff, f and fff all mean full.
func hexChannel(s string) int32 {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0
	}

	switch len(s) {
	case 1:
		return int32(v) * 17
	case 2:
		return int32(v)
	case 3:
		return int32(v >> 4)
	default:
		return int32(v >> 8)
	}
}
