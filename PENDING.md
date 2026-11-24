# Pending

Not done yet, roughly in order of how much they would be missed.

## Not supported

- **Encryption (MSE/PE)**: connections are plain. Peers that insist on
  encryption are not reachable, and ISPs that throttle BitTorrent can see
  it.
- **uTP**: TCP only. Many peers accept both, but some are only reachable
  over uTP.
- **Port forwarding (UPnP, NAT-PMP)**: behind a router, peers cannot connect
  to this side unless the port is forwarded by hand. Downloading still works;
  uploading is limited to peers this side connected to.
- **BitTorrent v2** (`btmh` magnets, v2-only torrents): refused. Hybrid
  torrents work through their v1 half.
- **Web seeds** (BEP 19, `ws=` in magnets): ignored.
- **The DHT as a full node**: it only searches (read-only, BEP 43). It does
  not answer other nodes or announce itself as a peer.

## Known limits

- A skipped file can still be created, holding the bytes of pieces it
  shares with wanted files, since only whole pieces are written.
- Moving a torrent's files to another folder after it is added is not
  offered.

## Wanted

- Download and upload rate limits, global and per torrent.
- A queue: a maximum number of active downloads, the rest waiting.
- Sorting the list by a column.
- File priorities beyond download or skip.
- Watching a folder for new .torrent files.
