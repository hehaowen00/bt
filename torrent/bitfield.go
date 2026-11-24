package torrent

import "math/bits"

// bitfield is a set of pieces in the wire format: the high bit of the first
// byte is piece 0.
type bitfield []byte

func newBitfield(n int) bitfield { return make(bitfield, (n+7)/8) }

func (b bitfield) get(i int) bool {
	return i >= 0 && i/8 < len(b) && b[i/8]&(0x80>>(i%8)) != 0
}

func (b bitfield) set(i int) {
	if i >= 0 && i/8 < len(b) {
		b[i/8] |= 0x80 >> (i % 8)
	}
}

func (b bitfield) clear(i int) {
	if i >= 0 && i/8 < len(b) {
		b[i/8] &^= 0x80 >> (i % 8)
	}
}

func (b bitfield) count() int {
	n := 0

	for _, x := range b {
		n += bits.OnesCount8(x)
	}

	return n
}
