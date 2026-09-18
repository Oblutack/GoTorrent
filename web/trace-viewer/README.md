# GoTorrent Trace Viewer

Replays a Phase 8 explain/trace mode JSONL log — see `internal/trace` and
`gottrent`/`gottrentd`'s own `-trace` flag — in the browser.

## Usage

Open `index.html` directly in a browser (double-click it, or `file://` it) —
no build step, no server, no dependencies. Some browsers restrict what a
`file://` page can do; if the file picker or rendering misbehaves, serve the
directory instead, e.g. `python -m http.server` from inside `web/trace-viewer/`
and open `http://localhost:8000/`.

Record a trace, then load it:

```sh
gottrent -torrent path/to/file.torrent -trace trace.jsonl
```

Click "Choose a trace file…" and pick the resulting `trace.jsonl`. If the
file covers more than one torrent (one fleet-wide trace file, several
managed torrents), pick which one to view from the dropdown.

## What it shows

- **Piece map** — one cell per piece: missing, requested (in flight), or
  have. A have-piece is colored by whichever peer's `piece_verified` event
  delivered it, when known — the same idea as Desktop's own
  piece-map-with-peer-attribution feature, computed fresh here from the
  trace file rather than shared code (different runtime).
- **Swarm** — every peer seen so far, positioned around a ring. Node size
  reflects cumulative bytes downloaded from that peer; the spoke color
  reflects whether this client currently has that peer's upload access
  choked (amber) or unchoked (green); a dashed spoke means the peer has
  since disconnected.
- **Per-peer contribution** — cumulative bytes downloaded from each peer,
  as bars, sorted highest first.
- **Choke timeline** — one row per peer, showing this client's own outbound
  choke/unchoke decisions over the course of the trace. This is *our*
  choke decision (do we allow this peer to download from us), not whether
  the peer is choking us — the engine does not currently trace the
  inbound direction.
- **Event log** — every event as a human-readable line, including the
  picker's own reasoning (priority tier, strategy, rarity, endgame) for
  each `picker_decision`. Click a line to jump the scrubber there.

## Known limitations

- Piece count is inferred as `(highest piece index seen in the trace) + 1`,
  not read from the `.torrent` file itself (the trace format doesn't carry
  metadata) — accurate for a trace covering the whole download, potentially
  short for one cut off early.
- Only this client's own downloading activity and outbound choke decisions
  are traced today; nothing about what this client uploads to peers (the
  engine wires that from `internal/peer`'s own goroutines, a separate
  package boundary from `internal/torrent`'s actor — not done yet).
- A full re-replay runs on every scrub position change. Fine for any trace
  size seen in practice; if a very large trace ever makes this noticeably
  slow, an incremental-state alternative would be the fix, not attempted
  here since nothing has needed it yet.
