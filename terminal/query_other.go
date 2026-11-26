//go:build !unix

package terminal

import (
	"time"
)

// query: only a unix tty is asked about itself. Everywhere else the sixteen
// terminal colours are all there is.
func query(time.Duration) []byte { return nil }
