package app

import (
	"fmt"
	"time"
)

var units = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}

// size is n bytes for reading: 512 B, 1.4 MiB, 23 GiB.
func size(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}

	f := float64(n)
	u := 0

	for f >= 1024 && u < len(units)-1 {
		f /= 1024
		u++
	}

	if f < 10 {
		return fmt.Sprintf("%.1f %s", f, units[u])
	}

	return fmt.Sprintf("%.0f %s", f, units[u])
}

// speed is a rate, blank when nothing moves.
func speed(r float64) string {
	if r < 1 {
		return ""
	}

	return size(int64(r)) + "/s"
}

// eta is a duration in its two largest units: 3d 4h, 12m 30s.
func eta(d time.Duration) string {
	if d < 0 {
		return ""
	}

	s := int64(d.Seconds())

	switch {
	case s >= 100*86400:
		return "∞"
	case s >= 86400:
		return fmt.Sprintf("%dd %dh", s/86400, s%86400/3600)
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
	case s >= 60:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}

	return fmt.Sprintf("%ds", s)
}

func percent(f float64) string {
	if f >= 1 {
		return "100%"
	}

	return fmt.Sprintf("%.1f%%", f*100)
}
