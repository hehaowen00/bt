//go:build unix

package terminal

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// query writes the questions and returns everything the terminal answered,
// or nothing when its answers did not all arrive in time.
func query(timeout time.Duration) []byte {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}

	defer tty.Close()

	old, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		return nil
	}

	defer term.Restore(int(tty.Fd()), old)

	// the questions, and a Primary Device Attributes request behind them: a
	// terminal answers in order, so its DA1 reply is the mark that every OSC
	// answer is already in hand. Without it there is no knowing when the last
	// one has arrived, and an answer read after the tty goes back to normal is
	// an answer echoed onto the shell prompt on the way out.
	q := "\x1b]10;?\x07\x1b]11;?\x07"

	for i := range 16 {
		q += fmt.Sprintf("\x1b]4;%d;?\x07", i)
	}

	q += "\x1b[c"

	if _, err := tty.WriteString(q); err != nil {
		return nil
	}

	// select(2) rather than a read deadline: a deadline on a tty is not
	// supported everywhere Go runs, and where it is not the read fails at
	// once — which looks exactly like a terminal that said nothing, and
	// leaves the answers in the buffer to land on the shell later.
	deadline := time.Now().Add(timeout)
	var buf []byte
	chunk := make([]byte, 4096)

	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil
		}

		fds := &unix.FdSet{}
		fds.Set(int(tty.Fd()))
		tv := unix.NsecToTimeval(int64(left))
		n, err := unix.Select(int(tty.Fd())+1, fds, nil, nil, &tv)
		if err == unix.EINTR {
			continue
		}

		if err != nil || n == 0 {
			return nil // nothing to read, and no more time to wait
		}

		read, err := tty.Read(chunk)
		buf = append(buf, chunk[:read]...)

		if err != nil {
			return nil
		}

		if da1.Match(buf) {
			break // every answer asked for is in: nothing is left to echo
		}
	}

	return buf
}
