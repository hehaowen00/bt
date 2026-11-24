package torrent

import "math"

// rate is bytes a second, a moving average of one-second samples with a
// five-second time constant.
type rate struct {
	pending int64
	avg     float64
}

var rateAlpha = 1 - math.Exp(-1.0/5)

func (r *rate) add(n int) { r.pending += int64(n) }

// tick closes a one-second sample.
func (r *rate) tick() {
	r.avg += rateAlpha * (float64(r.pending) - r.avg)
	r.pending = 0

	if r.avg < 1 {
		r.avg = 0
	}
}

func (r *rate) get() float64 { return r.avg }
