// Package dht finds peers for an info hash on the BitTorrent DHT (BEP 5). It
// is a read-only node (BEP 43): it asks, and is not asked.
package dht

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"

	"bt/bencode"
	"bt/tracker"
)

var routers = []string{
	"router.bittorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"router.utorrent.com:6881",
	"dht.libtorrent.org:25401",
}

const (
	alpha       = 8
	k           = 8
	maxGood     = 256
	queryWait   = 3 * time.Second
	lookupLimit = 30 * time.Second
)

type ID [20]byte

type node struct {
	id   ID
	addr netip.AddrPort
}

type DHT struct {
	conn *net.UDPConn
	id   ID

	mu      sync.Mutex
	pending map[string]chan bencode.Dict
	good    []node
	tx      uint16
}

// New opens the node on a UDP port of the system's choosing.
func New() (*DHT, error) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, err
	}

	d := &DHT{
		conn:    conn,
		pending: map[string]chan bencode.Dict{},
	}

	_, _ = rand.Read(d.id[:])

	go d.read()

	return d, nil
}

func (d *DHT) Close() { d.conn.Close() }

// Nodes is how many nodes are known to answer.
func (d *DHT) Nodes() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.good)
}

func (d *DHT) read() {
	buf := make([]byte, 4096)

	for {
		n, _, err := d.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}

		v, _, err := bencode.Decode(buf[:n])
		if err != nil {
			continue
		}

		m, ok := v.(map[string]any)
		if !ok {
			continue
		}

		msg := bencode.Dict(m)
		if msg.Str("y") != "r" && msg.Str("y") != "e" {
			continue
		}

		d.mu.Lock()
		ch := d.pending[msg.Str("t")]
		delete(d.pending, msg.Str("t"))
		d.mu.Unlock()

		if ch != nil {
			ch <- msg
		}
	}
}

// query sends a get_peers and waits for the reply.
func (d *DHT) query(ctx context.Context, to netip.AddrPort, target ID) bencode.Dict {
	ch := make(chan bencode.Dict, 1)

	d.mu.Lock()
	d.tx++

	var t [2]byte

	binary.BigEndian.PutUint16(t[:], d.tx)
	d.pending[string(t[:])] = ch
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.pending, string(t[:]))
		d.mu.Unlock()
	}()

	msg := bencode.Encode(map[string]any{
		"t":  string(t[:]),
		"y":  "q",
		"q":  "get_peers",
		"ro": 1,
		"a": map[string]any{
			"id":        string(d.id[:]),
			"info_hash": string(target[:]),
		},
	})

	_, err := d.conn.WriteToUDPAddrPort(msg, to)
	if err != nil {
		return nil
	}

	timer := time.NewTimer(queryWait)
	defer timer.Stop()

	select {
	case r := <-ch:
		if r.Str("y") != "r" {
			return nil
		}

		return r.Dict("r")
	case <-timer.C:
	case <-ctx.Done():
	}

	return nil
}

func (d *DHT) remember(n node) {
	d.mu.Lock()
	defer d.mu.Unlock()

	i := slices.IndexFunc(d.good, func(g node) bool {
		return g.addr == n.addr
	})
	if i >= 0 {
		return
	}

	if len(d.good) >= maxGood {
		d.good = d.good[1:]
	}

	d.good = append(d.good, n)
}

func (d *DHT) seeds(ctx context.Context) []node {
	d.mu.Lock()
	seeds := slices.Clone(d.good)
	d.mu.Unlock()

	if len(seeds) >= k {
		return seeds
	}

	var r net.Resolver

	for _, router := range routers {
		host, port, _ := net.SplitHostPort(router)
		p, _ := strconv.Atoi(port)

		addrs, err := r.LookupNetIP(ctx, "ip4", host)
		if err != nil || len(addrs) == 0 {
			continue
		}

		seeds = append(seeds, node{
			addr: netip.AddrPortFrom(addrs[0].Unmap(), uint16(p)),
		})
	}

	return seeds
}

type candidate struct {
	node
	dist  ID
	state int // 0 new, 1 asked, 2 answered, 3 failed
}

const (
	stNew = iota
	stAsked
	stAnswered
	stFailed
)

// GetPeers searches for peers of target, calling found for each as it
// arrives, until the search settles, the time budget runs out or ctx ends.
func (d *DHT) GetPeers(ctx context.Context, target ID, found func(netip.AddrPort)) {
	ctx, cancel := context.WithTimeout(ctx, lookupLimit)
	defer cancel()

	var (
		mu     sync.Mutex
		cands  []*candidate
		seen   = map[netip.AddrPort]bool{}
		peers  = map[netip.AddrPort]bool{}
		wake   = make(chan struct{}, 1)
		flight int
	)

	add := func(n node) {
		if seen[n.addr] || !n.addr.IsValid() || n.addr.Port() == 0 {
			return
		}

		seen[n.addr] = true

		c := &candidate{
			node: n,
		}
		for i := range c.dist {
			c.dist[i] = n.id[i] ^ target[i]
		}

		cands = append(cands, c)
	}

	for _, n := range d.seeds(ctx) {
		add(n)
	}

	for {
		mu.Lock()

		slices.SortFunc(cands, func(a, b *candidate) int {
			return bytes.Compare(a.dist[:], b.dist[:])
		})

		// done once the k closest that may answer have all answered
		settled := 0
		waiting := false

		for _, c := range cands {
			if settled >= k {
				break
			}

			switch c.state {
			case stAnswered:
				settled++
			case stNew, stAsked:
				waiting = true
			}

			if waiting {
				break
			}
		}

		if !waiting && flight == 0 {
			mu.Unlock()
			return
		}

		for _, c := range cands {
			if flight >= alpha {
				break
			}

			if c.state != stNew {
				continue
			}

			c.state = stAsked
			flight++

			go func(c *candidate) {
				r := d.query(ctx, c.addr, target)

				mu.Lock()
				defer mu.Unlock()

				flight--

				if r == nil {
					c.state = stFailed
				} else {
					c.state = stAnswered

					var id ID

					copy(id[:], r.Str("id"))
					d.remember(node{
						id:   id,
						addr: c.addr,
					})

					for b := []byte(r.Str("nodes")); len(b) >= 26; b = b[26:] {
						var nid ID

						copy(nid[:], b[:20])
						addr := tracker.ParseCompact(b[20:26], 6)

						if len(addr) == 1 {
							add(node{
								id:   nid,
								addr: addr[0],
							})
						}
					}

					for _, v := range r.List("values") {
						s, _ := v.(string)
						for _, p := range tracker.ParseCompact([]byte(s), 6) {
							if !peers[p] {
								peers[p] = true
								found(p)
							}
						}
					}
				}

				select {
				case wake <- struct{}{}:
				default:
				}
			}(c)
		}

		mu.Unlock()

		select {
		case <-wake:
		case <-ctx.Done():
			return
		}
	}
}
