// Package tracker announces to HTTP (BEP 3, 23) and UDP (BEP 15) trackers.
package tracker

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bt/bencode"
)

type Event int

const (
	EventNone Event = iota
	EventCompleted
	EventStarted
	EventStopped
)

var eventNames = [...]string{"", "completed", "started", "stopped"}

type Request struct {
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       uint16
	Uploaded   int64
	Downloaded int64
	Left       int64
	Event      Event
	NumWant    int
	Key        uint32
}

type Response struct {
	Interval    time.Duration
	MinInterval time.Duration
	Peers       []netip.AddrPort
	Seeders     int
	Leechers    int
}

var client = &http.Client{
	Timeout: 20 * time.Second,
}

// Announce sends req to the tracker at rawURL.
func Announce(ctx context.Context, rawURL string, req Request) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "http", "https":
		return announceHTTP(ctx, u, req)
	case "udp":
		return announceUDP(ctx, u.Host, req)
	}

	return nil, fmt.Errorf("unsupported tracker scheme %q", u.Scheme)
}

func announceHTTP(ctx context.Context, u *url.URL, req Request) (*Response, error) {
	q := "info_hash=" + escapeBytes(req.InfoHash[:]) +
		"&peer_id=" + escapeBytes(req.PeerID[:]) +
		"&port=" + strconv.Itoa(int(req.Port)) +
		"&uploaded=" + strconv.FormatInt(req.Uploaded, 10) +
		"&downloaded=" + strconv.FormatInt(req.Downloaded, 10) +
		"&left=" + strconv.FormatInt(req.Left, 10) +
		"&compact=1&no_peer_id=1" +
		"&numwant=" + strconv.Itoa(req.NumWant) +
		"&key=" + strconv.FormatUint(uint64(req.Key), 16)

	if req.Event != EventNone {
		q += "&event=" + eventNames[req.Event]
	}

	full := u.String()

	if u.RawQuery == "" {
		full += "?" + q
	} else {
		full += "&" + q
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(hreq)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	v, _, err := bencode.Decode(body)
	if err != nil {
		return nil, errors.New("tracker sent an unreadable reply")
	}

	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("tracker sent an unreadable reply")
	}

	d := bencode.Dict(m)

	reason := d.Str("failure reason")
	if reason != "" {
		return nil, errors.New(reason)
	}

	r := &Response{
		Interval:    time.Duration(d.Int("interval")) * time.Second,
		MinInterval: time.Duration(d.Int("min interval")) * time.Second,
		Seeders:     int(d.Int("complete")),
		Leechers:    int(d.Int("incomplete")),
	}

	switch p := d["peers"].(type) {
	case string:
		r.Peers = ParseCompact([]byte(p), 6)
	case []any:
		for _, e := range p {
			pm, _ := e.(map[string]any)
			pd := bencode.Dict(pm)

			ip, err := netip.ParseAddr(pd.Str("ip"))
			port := pd.Int("port")

			if err == nil && port > 0 && port < 65536 {
				r.Peers = append(r.Peers, netip.AddrPortFrom(ip.Unmap(), uint16(port)))
			}
		}
	}

	r.Peers = append(r.Peers, ParseCompact([]byte(d.Str("peers6")), 18)...)

	return r, nil
}

// ParseCompact reads 6-byte (IPv4) or 18-byte (IPv6) peer entries.
func ParseCompact(b []byte, size int) []netip.AddrPort {
	var out []netip.AddrPort

	for ; len(b) >= size; b = b[size:] {
		ip, _ := netip.AddrFromSlice(b[:size-2])
		port := binary.BigEndian.Uint16(b[size-2:])

		if port != 0 {
			out = append(out, netip.AddrPortFrom(ip.Unmap(), port))
		}
	}

	return out
}

const (
	udpMagic    = 0x41727101980
	actConnect  = 0
	actAnnounce = 1
	actError    = 3
)

// udpTries waits 3 s, then 6 s. The spec's 15·2ⁿ is too slow to show anyone.
var udpTries = []time.Duration{3 * time.Second, 6 * time.Second}

func announceUDP(ctx context.Context, host string, req Request) (*Response, error) {
	var d net.Dialer

	conn, err := d.DialContext(ctx, "udp", host)
	if err != nil {
		return nil, err
	}

	defer conn.Close()

	connID, err := udpRoundTrip(ctx, conn, func(tx uint32) []byte {
		b := make([]byte, 16)
		binary.BigEndian.PutUint64(b, udpMagic)
		binary.BigEndian.PutUint32(b[8:], actConnect)
		binary.BigEndian.PutUint32(b[12:], tx)

		return b
	}, actConnect, 16)
	if err != nil {
		return nil, err
	}

	cid := binary.BigEndian.Uint64(connID[8:])

	resp, err := udpRoundTrip(ctx, conn, func(tx uint32) []byte {
		b := make([]byte, 98)
		binary.BigEndian.PutUint64(b, cid)
		binary.BigEndian.PutUint32(b[8:], actAnnounce)
		binary.BigEndian.PutUint32(b[12:], tx)
		copy(b[16:], req.InfoHash[:])
		copy(b[36:], req.PeerID[:])
		binary.BigEndian.PutUint64(b[56:], uint64(req.Downloaded))
		binary.BigEndian.PutUint64(b[64:], uint64(req.Left))
		binary.BigEndian.PutUint64(b[72:], uint64(req.Uploaded))
		binary.BigEndian.PutUint32(b[80:], uint32(req.Event))
		binary.BigEndian.PutUint32(b[88:], req.Key)
		binary.BigEndian.PutUint32(b[92:], uint32(int32(req.NumWant)))
		binary.BigEndian.PutUint16(b[96:], req.Port)

		return b
	}, actAnnounce, 20)
	if err != nil {
		return nil, err
	}

	size := 6

	if conn.RemoteAddr().(*net.UDPAddr).IP.To4() == nil {
		size = 18
	}

	return &Response{
		Interval: time.Duration(binary.BigEndian.Uint32(resp[8:])) * time.Second,
		Leechers: int(binary.BigEndian.Uint32(resp[12:])),
		Seeders:  int(binary.BigEndian.Uint32(resp[16:])),
		Peers:    ParseCompact(resp[20:], size),
	}, nil
}

func udpRoundTrip(
	ctx context.Context,
	conn net.Conn,
	build func(tx uint32) []byte,
	action uint32,
	minLen int,
) ([]byte, error) {
	buf := make([]byte, 2048)

	for _, wait := range udpTries {
		var txb [4]byte

		_, _ = rand.Read(txb[:])
		tx := binary.BigEndian.Uint32(txb[:])

		_, err := conn.Write(build(tx))
		if err != nil {
			return nil, err
		}

		deadline := time.Now().Add(wait)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}

		_ = conn.SetReadDeadline(deadline)

		for {
			n, err := conn.Read(buf)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}

				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break
				}

				return nil, err
			}

			if n < 8 || binary.BigEndian.Uint32(buf[4:]) != tx {
				continue
			}

			got := binary.BigEndian.Uint32(buf)
			if got == actError {
				return nil, errors.New(strings.TrimSpace(string(buf[8:n])))
			}

			if got != action || n < minLen {
				return nil, errors.New("tracker sent an unreadable reply")
			}

			return append([]byte(nil), buf[:n]...), nil
		}
	}

	return nil, errors.New("tracker did not answer")
}

// escapeBytes percent-encodes all but the unreserved characters. url's
// QueryEscape writes a space as +, which some trackers read as a plus.
func escapeBytes(b []byte) string {
	const hexDigits = "0123456789ABCDEF"

	var sb strings.Builder

	for _, c := range b {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '.', c == '_', c == '~':
			sb.WriteByte(c)
		default:
			sb.WriteByte('%')
			sb.WriteByte(hexDigits[c>>4])
			sb.WriteByte(hexDigits[c&15])
		}
	}

	return sb.String()
}
