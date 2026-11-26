//go:build darwin

package app

import (
	"io/fs"
	"syscall"
	"time"
)

func createdAt(st fs.FileInfo) time.Time {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}
	}

	return time.Unix(sys.Birthtimespec.Sec, sys.Birthtimespec.Nsec)
}
