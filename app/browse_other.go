//go:build !darwin

package app

import (
	"io/fs"
	"time"
)

// createdAt is zero where plain stat has no creation time: the column shows
// a dash rather than a wrong answer.
func createdAt(fs.FileInfo) time.Time { return time.Time{} }
