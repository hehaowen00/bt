# bt

A BitTorrent client for the terminal, written in Go. It speaks the protocol
itself (trackers, the DHT, the peer wire protocol, magnet metadata), so it is
one binary with no daemon behind it.

## Features

- `.torrent` files, magnet links and bare info hashes.
- Peers from HTTP and UDP trackers, the DHT (no trackers needed) and peer
  exchange.
- Metadata for a magnet link is fetched from peers.
- Rarest-first piece picking, request pipelining sized to each peer's
  speed, and an endgame so the last piece does not wait on the slowest peer.
- Uploads to other peers, with the usual choking: the four best, plus one
  at random.
- Choose which files of a torrent to download.
- Make a torrent from a file or folder, with your own trackers, and seed it
  straight away.
- Files already on disk are checked rather than downloaded again; `r`
  checks them on request.
- Torrents, their progress and file choices carry over between runs.
- Only verified pieces are written, each once; progress is saved at most
  every 30 seconds.
- Seven colour themes, mouse support, and a command palette.

## Installation

You need Go.

```bash
make build                               # builds ./bt
make install                             # into ~/.local/bin
sudo make install PREFIX=/usr/local      # or system-wide
```

`make help` lists every target.

## Use

```bash
bt                                    # open the list
bt file.torrent 'magnet:?xt=urn:btih:…'  # and add these
```

Press `n` to make a torrent: it asks for the file or folder, the
trackers (URLs separated by spaces, or none to rely on the DHT), whether
it is private when there are trackers, and where to save the `.torrent`.
The files are hashed in the background, and the new torrent seeds from
where they are.

Press `a` to add a torrent. The prompt completes paths with Tab. Pasting a
magnet link anywhere opens the prompt with the link in it.

The screen has the torrent list at the top and a detail pane below it, with
four tabs: General, Files, Peers and Trackers. Errors and questions appear
on the bottom line.

| key                    | action                                     |
| ---------------------- | ------------------------------------------ |
| ↓ ↑ PgDn PgUp Home End | move                                       |
| Tab Enter Esc          | move between the list and the detail pane  |
| ← → `1`–`4`            | General, Files, Peers, Trackers tab        |
| `a`                    | add a .torrent file or magnet link         |
| `A`                    | add, choosing the folder to save in        |
| `n`                    | make a torrent from a file or folder       |
| Space `p`              | pause or resume                            |
| Space                  | in the Files tab: download or skip the file |
| `+` `-`                | in the Files tab: download or skip every file |
| `r`                    | check the files on disk again              |
| Delete                 | remove the torrent, keeping its files      |
| Shift-Delete `D`       | remove the torrent and delete its files    |
| `c`                    | copy the magnet link                       |
| `/` Ctrl-F             | filter the list by name                    |
| Ctrl-P                 | command palette: every command, by name    |
| `t`                    | next theme                                 |
| `?` F1                 | every key                                  |
| `q` Ctrl-Q Ctrl-C      | quit; torrents resume next time            |

The palette also copies the info hash or the path and picks a theme by
name. Copying goes through the terminal (OSC 52), so it works over SSH.

Peer flags in the Peers tab: `D` downloading from it, `d` it would send but
nothing is wanted from it, `U` uploading to it, `u` it wants to download but
is choked, `O` optimistic unchoke, `S` snubbed, `I` it connected to us.

## Configuration

`~/.config/bt/config` (or `$XDG_CONFIG_HOME/bt/config`), every line
optional:

```
download_dir = ~/Downloads
port = 6881          # for incoming peers; if taken, any free port is used
dht = yes            # find peers without trackers
seed_ratio = 0       # stop seeding at this upload ratio; 0 never stops
theme = terminal     # terminal, dark, light, solarized-dark, solarized-light, gruvbox, nord
```

State is kept in `~/.local/state/bt` (or `$XDG_STATE_HOME/bt`):
the torrent list, each `.torrent`, and which pieces are done.

## Layout

| package    | what                                                 |
| ---------- | ---------------------------------------------------- |
| `bencode`  | the encoding                                         |
| `metainfo` | .torrent files and magnet links, and making torrents |
| `tracker`  | HTTP and UDP announces                               |
| `dht`      | a read-only DHT node that finds peers                |
| `peer`     | the wire protocol's messages                         |
| `torrent`  | the engine: sessions, peers, picking, storage, state |
| `app`      | the terminal interface                               |

RESEARCH.md explains the choices; PENDING.md lists what is not done.
