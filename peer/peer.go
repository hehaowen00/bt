// Package peer reads and writes the peer wire protocol (BEP 3, BEP 10).
package peer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const protocol = "\x13BitTorrent protocol"

type MsgID byte

const (
	Choke MsgID = iota
	Unchoke
	Interested
	NotInterested
	Have
	Bitfield
	Request
	Piece
	Cancel
	Port
	Extended MsgID = 20
)

// BlockSize is the size of a request.
const BlockSize = 16 << 10

// MaxMessage bounds a message: a block plus its header, or a bitfield for
// a million pieces.
const MaxMessage = 1 << 18

// Handshake is what each side sends first.
type Handshake struct {
	Reserved [8]byte
	InfoHash [20]byte
	PeerID   [20]byte
}

// NewHandshake sets the reserved bits for the extension protocol and DHT.
func NewHandshake(infoHash, peerID [20]byte) Handshake {
	h := Handshake{
		InfoHash: infoHash,
		PeerID:   peerID,
	}
	h.Reserved[5] |= 0x10
	h.Reserved[7] |= 0x01

	return h
}

func (h Handshake) Extensions() bool { return h.Reserved[5]&0x10 != 0 }

func (h Handshake) Bytes() []byte {
	b := make([]byte, 0, 68)
	b = append(b, protocol...)
	b = append(b, h.Reserved[:]...)
	b = append(b, h.InfoHash[:]...)
	b = append(b, h.PeerID[:]...)

	return b
}

// ReadHandshake reads the other side's handshake.
func ReadHandshake(r io.Reader) (Handshake, error) {
	var b [68]byte

	_, err := io.ReadFull(r, b[:])
	if err != nil {
		return Handshake{}, err
	}

	if !bytes.Equal(b[:20], []byte(protocol)) {
		return Handshake{}, errors.New("not a BitTorrent peer")
	}

	var h Handshake

	copy(h.Reserved[:], b[20:28])
	copy(h.InfoHash[:], b[28:48])
	copy(h.PeerID[:], b[48:68])

	return h, nil
}

// Message is one message; a keep-alive has a nil Message.
type Message struct {
	ID      MsgID
	Payload []byte
}

// Read reads the next message into buf if it fits, and returns nil for a
// keep-alive.
func Read(r io.Reader, buf []byte) (*Message, error) {
	var lb [4]byte

	_, err := io.ReadFull(r, lb[:])
	if err != nil {
		return nil, err
	}

	n := binary.BigEndian.Uint32(lb[:])
	if n == 0 {
		return nil, nil
	}

	if n > MaxMessage {
		return nil, fmt.Errorf("message of %d bytes is too long", n)
	}

	if int(n) > cap(buf) {
		buf = make([]byte, n)
	}

	buf = buf[:n]

	_, err = io.ReadFull(r, buf)
	if err != nil {
		return nil, err
	}

	return &Message{
		ID:      MsgID(buf[0]),
		Payload: buf[1:],
	}, nil
}

// Encode is the message as sent.
func Encode(id MsgID, payload ...[]byte) []byte {
	n := 1

	for _, p := range payload {
		n += len(p)
	}

	b := make([]byte, 4, 4+n)
	binary.BigEndian.PutUint32(b, uint32(n))
	b = append(b, byte(id))

	for _, p := range payload {
		b = append(b, p...)
	}

	return b
}

func KeepAlive() []byte { return make([]byte, 4) }

func uint32s(v ...uint32) []byte {
	b := make([]byte, 4*len(v))

	for i, x := range v {
		binary.BigEndian.PutUint32(b[4*i:], x)
	}

	return b
}

func EncodeHave(index int) []byte { return Encode(Have, uint32s(uint32(index))) }

func EncodeRequest(index, begin, length int) []byte {
	return Encode(Request, uint32s(uint32(index), uint32(begin), uint32(length)))
}

func EncodeCancel(index, begin, length int) []byte {
	return Encode(Cancel, uint32s(uint32(index), uint32(begin), uint32(length)))
}

func EncodePiece(index, begin int, data []byte) []byte {
	return Encode(Piece, uint32s(uint32(index), uint32(begin)), data)
}

func EncodeExtended(id byte, payload ...[]byte) []byte {
	return Encode(Extended, append([][]byte{{id}}, payload...)...)
}

// ParseHave reads a have's piece index.
func ParseHave(p []byte) (int, error) {
	if len(p) != 4 {
		return 0, errors.New("bad have")
	}

	return int(binary.BigEndian.Uint32(p)), nil
}

// ParseRequest reads a request or cancel.
func ParseRequest(p []byte) (index, begin, length int, err error) {
	if len(p) != 12 {
		return 0, 0, 0, errors.New("bad request")
	}

	index = int(binary.BigEndian.Uint32(p))
	begin = int(binary.BigEndian.Uint32(p[4:]))
	length = int(binary.BigEndian.Uint32(p[8:]))

	return index, begin, length, nil
}

// ParsePiece reads a piece message.
func ParsePiece(p []byte) (index, begin int, data []byte, err error) {
	if len(p) < 8 {
		return 0, 0, nil, errors.New("bad piece")
	}

	return int(binary.BigEndian.Uint32(p)), int(binary.BigEndian.Uint32(p[4:])), p[8:], nil
}
