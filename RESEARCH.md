# Research

Notes on the BitTorrent protocol and on the choices made here, written before
the code and kept alongside it.

## Speaking the protocol natively

The established Go library, `anacrolix/torrent`, pulls in some forty modules
(sqlite, webrtc, a metrics stack). The parts a downloader needs are small and
documented as BEPs, so they are written here against the specs, with tcell as
the only real dependency. The BEPs used:

| BEP | what                                  | here                        |
| --- | ------------------------------------- | --------------------------- |
| 3   | the protocol: metainfo, tracker, wire | `bencode`, `metainfo`, `peer` |
| 5   | DHT                                   | `dht`, read-only            |
| 9   | metadata from peers (magnets)         | `peer`, `torrent`           |
| 10  | extension protocol                    | `peer`                      |
| 11  | peer exchange (PEX)                   | `torrent`, receive only     |
| 12  | multitracker (`announce-list`)        | `metainfo`, `torrent`       |
| 15  | UDP trackers                          | `tracker`                   |
| 20  | peer id convention (`-XB0100-…`)      | `torrent`                   |
| 23  | compact peer lists                    | `tracker`                   |
| 43  | read-only DHT nodes                   | `dht`                       |

## Bencode

Integers, byte strings, lists and dictionaries. The decoder works on a byte
slice and returns `any` (`int64`, `string`, `[]any`, `map[string]any`).
The info hash is the SHA-1 of the info dictionary's bytes as they appear in
the file, not of a re-encoding, since a sloppy encoder (unsorted keys) would
otherwise give a different hash. The decoder therefore reports the span of
the `info` value so the raw bytes can be hashed.

Depth and length are bounded: a hostile peer sends metadata and tracker
replies, so nesting is capped (64) and string lengths are checked against
what is left of the input before slicing.

## Magnet links

`magnet:?xt=urn:btih:<hash>&dn=<name>&tr=<tracker>&x.pe=<host:port>`

- The hash is 40 hex characters or 32 base32 characters.
- `tr` repeats; each is one tier, as in `announce-list`.
- `x.pe` gives peers directly.
- `xt=urn:btmh:` (v2) alone is refused; a hybrid link also has `btih`.

With only a hash, the info dictionary comes from peers (BEP 9): after the
extension handshake advertises `ut_metadata` and `metadata_size`, the
dictionary is requested in 16 KiB pieces, put together and checked against
the info hash. Peers come from trackers in the link, the DHT and PEX.

## Trackers

- HTTP: `GET announce?info_hash=…&peer_id=…&port=…&uploaded=…&downloaded=…&left=…&compact=1&event=…`.
  `info_hash` and `peer_id` are percent-encoded raw bytes, every byte
  but the unreserved ones: Go's `url.QueryEscape` writes a space as `+`,
  which opentracker and others read as a plus, giving the wrong hash. The reply is a
  bencoded dictionary with `interval` and `peers`, a string of 6-byte
  entries (or a list of dictionaries from old trackers), and `peers6`, 18
  bytes each.
- UDP (BEP 15): connect (magic `0x41727101980`, action 0) gives a
  connection id valid for a minute; announce is action 1. Retries wait
  15·2ⁿ seconds by the spec. That is far too slow for an interactive
  program, so here a try waits 3 s, then 6, then gives up until the next
  round. A dead UDP tracker costs 9 s, not the spec's hour.
- Tiers (BEP 12): trackers in a tier are tried in order; one that answers
  moves to the front of its tier. Every tier is announced to, since public
  magnets list many trackers and most are dead.

Announces run at the tracker's interval (clamped to 1 to 30 minutes) and
sooner, at its `min interval`, while the torrent has too few peers.

## DHT

A Kademlia table over UDP with 160-bit ids, bencoded KRPC messages (`q`,
`r`, `e`). Only lookups are needed, so the node is read-only (BEP 43: `ro=1`
in queries). Other nodes then do not add it to their tables or send it
queries, which saves answering them, and there is no need for the
announce_peer token dance or for a stable id across runs.

Lookup for an info hash, the standard iterative search:

1. Seed a candidate set from the bootstrap routers
   (`router.bittorrent.com:6881`, `dht.transmissionbt.com:6881`,
   `router.utorrent.com:6881`, `dht.libtorrent.org:25401`) and from nodes
   kept from earlier lookups this session.
2. Keep the candidates sorted by XOR distance to the target. Send `get_peers`
   to the α = 8 closest not yet asked, with no more than α in flight.
   Libtorrent uses 3; more in flight finds peers sooner at little cost since
   the node only searches.
3. Each reply adds `nodes` (26-byte compact entries) to the candidates and
   `values` (6-byte peers) to the result.
4. Stop when the k = 8 closest candidates have all answered or timed out, or
   after a time budget. Repeat every 5 minutes while the torrent wants peers.

Measured against the live DHT: from a bare info hash with no trackers, a
lookup found about 300 peers within 6 s, and the metadata arrived in
about 35 s, most of it waiting on peers that did not answer the request.
Metadata requests are therefore sent again to another peer after 5 s.

A full routing table with buckets is not kept: the good nodes from each
lookup (those that answered) are remembered, up to 256, to start the next.

## Connecting to peers

Most addresses from the DHT and trackers are dead or firewalled. With 12
attempts at a time and a 10 s timeout, a live test held only 4 to 8 peers of
337 known. With 32 at a time and 6 s, it held 23 within 12 s and downloaded
at 2.8 MiB/s. A failed address waits 2 minutes before another try, doubling
with each failure.

## Peer wire

Handshake: `\x13BitTorrent protocol`, 8 reserved bytes (bit 0x10 of byte 5
for BEP 10; bit 0x01 of byte 7 for DHT), info hash, peer id. Then
length-prefixed messages: choke, unchoke, interested, not interested, have,
bitfield, request, piece, cancel, port, extended (20).

Requests are for 16 KiB blocks (the de facto maximum; larger are refused by
most clients).

### Pipelining

A peer is kept busy by keeping several requests outstanding. A fixed depth
starves fast peers: at 16 KiB blocks and 100 ms round trip, 5 requests cap a
peer at 800 KiB/s. The depth here follows the peer's measured rate, as
libtorrent does: enough to cover 2 s of transfer at its rate, at least 4 and
at most 256, and at most what the peer's `reqq` (BEP 10) says it accepts.

### Piece picking

- **Rarest first**: availability is counted per piece from bitfields and
  haves. A peer is given blocks of a piece already in progress first (so
  pieces finish and can be written and shared), then a new piece from the
  rarest it has. Ties are broken randomly so peers spread over pieces.
- **Random first**: until 4 pieces are complete, any piece the peer has is
  picked at random; a rare piece is slow to get and the first goal is
  something to trade.
- **Endgame**: when every missing block is requested, the outstanding
  blocks are also requested from other peers, and a cancel goes to the rest
  when one arrives. This avoids the last piece waiting on the slowest peer.
- Files marked not wanted are excluded: a piece is wanted when it overlaps a
  wanted file.

Availability is a slice of counts indexed by piece. Picking scans it, which
for 100 000 pieces is a fraction of a millisecond and happens once per
piece, not per block. A bucketed priority queue would be faster but must be
updated on every `have`, and has no use at these sizes.

## Choking (uploading)

The reference algorithm: every 10 s, unchoke the 4 interested peers that
send to us fastest (when seeding, those we send to fastest), and every 30 s
rotate one optimistic unchoke to a random interested choked peer so new
peers get a chance. Snubbed peers (nothing for 60 s) are not counted.

## Storage and disk writes

AGENTS.md asks for no excessive disk writes. So:

- Blocks are kept in memory until their piece is complete, hashed, and only
  a verified piece is written, once, with `WriteAt`. A bad piece costs no
  writes. Files are not preallocated: the OS makes them sparse.
- The number of pieces in progress is bounded so memory stays small:
  partial pieces at most twice the number of unchoked peers, and new pieces
  are not started while that many are open.
- Resume data (which pieces are done) is a bitfield file per torrent,
  written at most every 30 s while pieces are arriving, when the torrent
  completes or stops, and at exit. A crash loses at most 30 s of bookkeeping,
  not data: the pieces are on disk and a recheck finds them.
- Files are opened lazily and kept in a small cache of open handles.
- The session (torrent list, download folder, file choices, paused state) is
  written only when it changes. The `.torrent` itself (or the metadata
  fetched for a magnet) is stored once.

A newly added torrent whose files already exist is checked piece by piece
(a recheck) instead of being downloaded again.

## Rate display

Rates are an exponential moving average over 1 s samples, with a 5 s time
constant, so the numbers are steady enough to read. The ETA uses the same
average.

## Interface

The interface follows ix, rd and bw: a list on top, a detail pane below
(General, Files, Peers, Trackers), arrow keys and single-key actions with
no chords, `/` to filter, a Ctrl-P command palette, and errors on the
bottom line. The engine never
touches the screen; the interface takes a snapshot of every torrent once a
second (and after each keystroke) under the engine's lock.

## Making torrents

A torrent is made from one file (a single-file torrent named after it) or
a folder (a multi-file torrent: every regular file under it, in lexical
path order, which is what `filepath.WalkDir` gives and what mktorrent and
libtorrent do). Symlinked files are followed; symlinked folders are not,
so a loop cannot make the walk endless. Empty files are kept, since they
are part of what was shared.

Piece length: a power of two from 16 KiB to 16 MiB, the smallest that
keeps the torrent at 2048 pieces or fewer. The piece hashes are 20 bytes
each, so this bounds the `.torrent` at about 40 KiB while keeping pieces
small for small torrents (a 700 MiB file gets 512 KiB pieces, 8 GiB gets
4 MiB), close to the sizes libtorrent and qBittorrent choose.

Hashing reads the files once, front to back, since sequential reads are
what disks are fastest at, into piece-sized buffers that a few goroutines
hash with SHA-1 (which Go runs on the CPU's SHA instructions). The buffer
pool is capped at 64 MiB, so a 16 MiB piece length does not mean a
gigabyte of buffers. Only the declared length of each file is read, and a
file that came up short is an error rather than a wrong hash.

Trackers are typed as URLs separated by spaces; each goes in its own tier
of `announce-list`, with the first also as `announce` for old clients.
With no trackers the torrent is found through the DHT. A torrent for a
private tracker sets `private` (BEP 27), which keeps clients off the DHT
and PEX for it.

The new torrent is added to the session as complete, seeding from where
its files are, without a recheck: the files were just hashed, and reading
them again would double the I/O. If a file is missing or shorter by the
time it is added, the usual check on start catches it.
