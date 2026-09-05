# GoTorrent — Codebase Review, Grade & Improvement Plan

**Reviewed commit:** `6e96d74` (branch `main`) · **Date:** 2026-09-05
**Scope:** all 11 Go files, ~2,670 LOC, plus build/CI/tooling config.

---

## 1. Verdict

# **56 / 100**

Measured as a *general-purpose BitTorrent client*. Measured only against its own stated goal — "an educational deep dive into network programming and Go concurrency" — it earns roughly **78/100**: the protocol is genuinely implemented from scratch, it downloads and seeds real torrents, and the structure is clean enough to read in an afternoon. The gap between those two numbers is where all the interesting work lives.

### Rubric

| Category | Weight | Score | Notes |
|---|---:|---:|---|
| Protocol correctness & conformance | 25 | 15 | Core wire protocol right; no UDP/DHT/magnet/Fast/extension; no incoming connections |
| Concurrency & memory safety | 20 | 11 | Real data races, interleaved socket writes, goroutine leaks, no cancellation |
| Performance & resource use | 15 | 9 | O(torrent-size) RAM at startup; O(pieces×peers) hot loop under a global lock |
| Security & input validation | 15 | 6 | Path traversal from a hostile `.torrent`; unbounded allocation from a hostile peer |
| Testing & QA | 10 | 4 | 2 test functions, one package covered; no race/fuzz/integration tests |
| Code quality & maintainability | 10 | 7 | Readable and consistently structured; one 1,000-line god-file; mixed-language comments |
| Docs & tooling | 5 | 4 | Strong README; CI runs only build+test |
| **Total** | **100** | **56** | |

### What is genuinely good

Worth stating plainly before the criticism, because these are not small things:

- **Zero external dependencies.** Bencode, the peer wire protocol and the tracker client are all hand-rolled and all work. That is the hard part of the project and it is done.
- **The bencode decoder is stricter than most.** It enforces lexicographic dictionary key ordering (`decode.go:210`), rejects leading zeros in integers and string lengths, and caps string length at 64 MiB. Strict-by-default is the right instinct for a format that feeds a hash.
- **The download loop's single-owner design is correct in spirit.** All piece state mutates inside one `select` in `downloadLoop` (`session.go:549`). That is the right architecture; it is just not yet enforced by the type system or the lock discipline.
- **Endgame handling is thoughtful.** Adaptive timeouts, duplicate-request overlap, stall detection with peer eviction, tie-shuffled rarest-first — these are refinements most from-scratch clients never reach.
- **Files are pre-allocated up front** (`session.go:769`), so every write is a plain seek+write with no allocation-time surprises mid-download.
- **The README is better than the code.** Honest about scope, has a demo GIF, has a roadmap.

---

## 2. Critical findings (P0 — fix before anything else)

### P0-1 · Startup allocates the entire torrent in RAM

`session.go:427`

```go
pw := &PieceWork{
    ...
    Buffer: make([]byte, pieceLength),   // for EVERY missing piece, at startup
}
```

`populateWorkQueue` runs once before the download begins and allocates a full piece buffer for **every piece not yet held**. For the 6.2 GB Ubuntu ISO sitting in this working directory, a fresh download allocates ~6.2 GB of heap before the first byte arrives. On a 8 GB machine this either OOM-kills the process or drives it into swap. This is the single worst defect in the codebase and it scales linearly with torrent size — the client cannot download anything larger than available RAM.

**Fix.** Allocate lazily when a piece becomes active, cap concurrent in-flight pieces, and release the buffer immediately after the piece is written:

```go
const maxInFlightPieces = 64   // 64 × 2 MiB pieces = 128 MiB ceiling, size-independent

// in populateWorkQueue: leave Buffer nil.
// in downloadLoop, when pulling from PieceWorkQueue:
if len(s.ActivePieces) >= maxInFlightPieces {
    // push back / don't drain the queue this iteration
    break
}
pieceWork.Buffer = s.bufPool.Get(pieceWork.Length)   // sync.Pool of piece-sized buffers
```

The buffer is already nilled after a successful write (`session.go:645`) — return it to a pool there instead of dropping it on the floor for the GC.

Longer term the buffer disappears entirely: write each block straight to its final file offset with `WriteAt` as it arrives, track arrival in a per-piece bitmap, and hash by reading the piece back once when complete. Memory then becomes O(in-flight blocks), not O(pieces).

---

### P0-2 · Concurrent writes to the same TCP connection corrupt the protocol stream

`peer.go:332`

```go
func (c *Client) SendMessage(id MessageID, payload []byte) error {
    msg := &Message{ID: id, Payload: payload}
    _, err := c.Conn.Write(msg.Serialize())
    ...
}
```

Four different goroutines call this on the same `net.Conn`:

| Goroutine | Messages sent |
|---|---|
| `Client.writeLoop` (`peer.go:271`) | `Request` |
| `Client.Run` read loop (`peer.go:172`) | `Unchoke`, `Piece` (16 KiB+) |
| `session.downloadLoop` (`session.go:640`) | `Have`, broadcast to every peer |
| `session.chokingLoop` (`session.go:947`) | `Choke`, `Unchoke` |

`net.TCPConn.Write` is safe from concurrent use in the sense that it will not crash, but it does **not** guarantee that one call's bytes land contiguously — a large `Piece` frame can be split across multiple `write(2)` syscalls, and another goroutine's `Have` frame can land in the middle of it. The remote peer then reads a garbage length prefix and drops the connection. This is almost certainly a contributor to the mysterious stalls the recent commits have been chasing, and it gets *more* likely the better the download is going (bigger `Piece` frames, more `Have` broadcasts).

**Fix.** One goroutine owns the socket. Everything else enqueues frames.

```go
type Client struct {
    outbound chan []byte      // buffered, e.g. 64
    done     chan struct{}
}

func (c *Client) send(id MessageID, payload []byte) error {
    frame := (&Message{ID: id, Payload: payload}).Serialize()
    select {
    case c.outbound <- frame:
        return nil
    case <-c.done:
        return net.ErrClosed
    default:
        return errPeerBackpressure     // slow peer: drop it rather than block the session
    }
}

func (c *Client) sendLoop() {
    defer c.Conn.Close()
    for {
        select {
        case <-c.done:
            return
        case frame := <-c.outbound:
            c.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
            if _, err := c.Conn.Write(frame); err != nil {
                close(c.done)
                return
            }
        }
    }
}
```

The `default:` branch matters: today a single stalled peer can block `downloadLoop`'s `Have` broadcast and freeze the whole session.

---

### P0-3 · Path traversal — a hostile `.torrent` can write anywhere on disk

`session.go:782` (pre-allocation), `session.go:895` (piece write), `session.go:466` (seed read)

```go
pathParts := append([]string{torrentBaseDir}, fileInfo.Path...)
fullFilePath := filepath.Join(pathParts...)
```

`fileInfo.Path` comes straight from the torrent file with no validation beyond "is a non-empty list of strings" (`metainfo.go:183`). `filepath.Join` *cleans* the result — it does not confine it. A torrent containing

```
path = ["..", "..", "..", "Users", "DT User3", "AppData", "Roaming", "Microsoft",
        "Windows", "Start Menu", "Programs", "Startup", "run.bat"]
```

causes `preallocateFiles` to create that file and `writePieceToDisk` to fill it with attacker-controlled bytes. `Info.Name` (`metainfo.go:136`) is equally unchecked and is used as a directory name — `name = ".."` alone escapes one level. On Windows, `name = "C:"` or a path containing a drive letter, an ADS marker (`file.txt:stream`), or a reserved device name (`CON`, `NUL`, `LPT1`) opens further variants.

This is **arbitrary file write triggered by opening a downloaded file** — the highest-severity class of bug a torrent client can have.

**Fix.** Sanitize once, in `metainfo`, and make the unsafe join impossible to write:

```go
// metainfo/path.go
var reservedWindows = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true,
    "com1": true /* … com2-9, lpt1-9 … */}

func sanitizeSegment(s string) error {
    switch {
    case s == "", s == ".", s == "..":
        return fmt.Errorf("illegal path segment %q", s)
    case strings.ContainsAny(s, `/\:` + "\x00"):
        return fmt.Errorf("path segment %q contains a separator or NUL", s)
    case reservedWindows[strings.ToLower(strings.SplitN(s, ".", 2)[0])]:
        return fmt.Errorf("path segment %q is a reserved device name", s)
    }
    return nil
}

// storage/layout.go — the ONLY place a torrent path is turned into a filesystem path
func (l *Layout) resolve(parts []string) (string, error) {
    full := filepath.Join(append([]string{l.base}, parts...)...)
    rel, err := filepath.Rel(l.base, full)
    if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
        return "", fmt.Errorf("path %v escapes the download directory", parts)
    }
    return full, nil
}
```

Validate at parse time so a malicious torrent is rejected before a single directory is created, and keep the belt-and-braces `Rel` check at use time. Add the attack vectors above as a table-driven test — they are the easiest high-value tests in the whole project.

---

### P0-4 · A single peer can OOM the client with one 12-byte message

`peer.go:245`

```go
case MsgRequest:
    var reqPayload MsgRequestPayload
    if err := reqPayload.Parse(msg.Payload); err == nil {
        if c.ourBitfield.HasPiece(reqPayload.Index) && !c.AmChoking {
            blockData, err := c.readBlockFromDisk(reqPayload.Index, reqPayload.Begin, reqPayload.Length)
```

`Length` is an unvalidated `uint32` supplied by the remote peer, and `readBlockFromDisk` does `make([]byte, length)` (`session.go:455`). A peer that requests a 4 GiB block gets a 4 GiB allocation. Repeat across the 50 allowed peers and the process dies. `Index` and `Begin` are equally unchecked, so a request can also seek past the end of a file (returning zeroed sparse regions) or produce a negative-offset panic path.

The receive direction has a milder version of the same problem: `session.go:582` copies `resultBlock.Block` into the piece buffer without checking that its length matches the block that was requested. `copy` is bounds-safe, so there is no overflow, but an oversized block silently overwrites neighbouring blocks in the piece, guaranteeing a hash failure and an endless re-download of that piece.

**Fix.** Validate every peer-supplied triple against the torrent geometry before it reaches storage:

```go
const maxBlockLength = 16 * 1024   // BEP 3: clients may reject > 16 KiB

func (c *Client) validRequest(r MsgRequestPayload) bool {
    if r.Length == 0 || r.Length > maxBlockLength {
        return false
    }
    if int(r.Index) >= c.numPiecesInTorrent {
        return false
    }
    return int64(r.Begin)+int64(r.Length) <= c.pieceLength(r.Index)
}
```

An invalid request is grounds for dropping the connection, not just ignoring the message. Symmetrically, in `downloadLoop`, require `uint32(len(resultBlock.Block)) == block.Length` before accepting a block.

---

### P0-5 · Data races on peer state

Confirmed by inspection; `go test -race` cannot see them today because no test starts a peer.

| Field | Written by | Read by |
|---|---|---|
| `Client.PeerChoking` (`peer.go:197,200`) | peer read loop | `downloadLoop` under `s.mu`, `writeLoop` |
| `Client.Bitfield` (`peer.go:220,225`) | peer read loop | `downloadLoop` rarity scan under `s.mu` |
| `Client.AmChoking` (`peer.go:210`, `session.go:982`) | peer read loop **and** `chokingLoop` | both |
| `TrackerRequest.Event` (`session.go:383`, `session.go:257`) | `trackerLoop`, main goroutine | `announceToTrackers` from three goroutines |

The session takes `s.mu` for these reads; the peer goroutine takes nothing when writing them. Under the Go memory model that is a race, with real consequences: a torn/stale `Bitfield` read makes the picker assign blocks to peers that do not have them, and a stale `PeerChoking` makes `writeLoop` silently drop work (`peer.go:275`) that the session has already marked in-flight — the block then sits until the 15 s timeout. That pattern matches the "65% progress stall" the last two commits were fighting.

There is also a **logic** conflict layered on the race: the read loop unchokes anybody who expresses interest (`peer.go:202-211`), while `chokingLoop` independently re-chokes everyone outside its 4 slots every 10 s. The two disagree permanently.

**Fix.** Peers stop being shared-memory objects. Each peer publishes state changes as events to the torrent actor, which owns the authoritative view:

```go
type peerEvent struct {
    peer *Client
    kind eventKind          // evChoke, evUnchoke, evHave, evBitfield, evBlock, evGone
    ...
}
```

If a full actor refactor is too large a first step, the minimum viable fix is: make `PeerChoking`/`AmChoking`/`PeerInterested` `atomic.Bool`, guard `Bitfield` with a per-client `RWMutex`, and delete the ad-hoc unchoke in the read loop so `chokingLoop` is the sole authority.

---

## 3. High-priority findings (P1)

**P1-1 · `writeLoop` goroutine leaks on every disconnect.** `peer.go:272` ranges over `c.WorkQueue`, which is never closed anywhere in the codebase (`close(` appears exactly once, at `peer.go:174`). Every peer that disconnects leaves a goroutine parked forever holding its `Client`, its 50-entry queue, and transitively its `Bitfield`. Over a long session with peer churn this is an unbounded leak. Fix: a `done` channel closed by `Run`'s defer, selected on in `writeLoop`.

**P1-2 · No listening socket.** `-port` is advertised to the tracker (`tracker.go:57`) but `net.Listen` appears nowhere in the repo. The client can never accept an incoming connection, so it only ever talks to peers it dials, cannot be reached by peers behind the same NAT, and — despite "Seeding" being ticked off in the README roadmap — will almost never actually seed to anyone. This is the largest functional gap relative to the advertised feature set.

**P1-3 · Uploaded bytes are never counted.** `TODO` at `peer.go:255`; `Uploaded` is hardcoded to 0 at `session.go:152` and announced as 0 forever. Any ratio-enforcing tracker reads this as leeching and will ban the peer ID.

**P1-4 · Resume state is trusted blindly and saved only on Ctrl-C.** `loadState` (`session.go:97`) accepts any file of the right length as truth. If the data files were deleted, truncated, or edited between runs, the client claims pieces it does not have and will serve garbage to the swarm — and to itself on the next resume. Meanwhile the only `saveState` call is in the SIGINT handler (`session.go:237`), so a crash, a power cut, or a `kill -9` loses *all* progress. Fix: checkpoint every ~30 s and on every N verified pieces; add a header to the state file (magic, version, infohash, total length, per-file size+mtime); offer `-verify` to re-hash on demand and auto-verify when the recorded file metadata does not match.

**P1-5 · Unbounded initial peer fan-out, no dedupe.** `session.go:225` spawns `connectToPeer` for every peer in the first tracker response with no `maxPeers` check (the cap exists only in `trackerLoop`, `session.go:400`) and no check against already-connected addresses. Re-announces therefore open duplicate connections to peers already held.

**P1-6 · `ConnectedPeers` is keyed by the remote peer ID.** `session.go:533`. Peer IDs are self-reported and unverified; two peers claiming the same ID silently evict each other from the map, and the eviction on disconnect (`session.go:545`) can delete the *other* peer's entry. Key by `net.Addr` — the one identifier the remote cannot forge.

**P1-7 · Bencode recursion has no depth limit.** `decodeRecursive` (`decode.go:21`) → `parseList`/`parseDictionary` → `decodeRecursive`, with no depth counter. A 100 KB file of `lllllll…` exhausts the goroutine stack and panics the process. Reachable from both a malicious `.torrent` *and* a malicious tracker response. Add a `depth int` parameter capped at 64.

**P1-8 · Tracker responses are read without a size limit.** `tracker.go:116` hands `resp.Body` straight to the decoder. A hostile or compromised tracker can stream unbounded data; the 64 MiB per-string cap does not bound a list of a million strings. Wrap in `io.LimitReader(resp.Body, 4<<20)`. While there: set a `User-Agent`, cap redirects, and consider refusing peer entries pointing at loopback/link-local/RFC-1918 addresses, which a tracker can otherwise use to make the client port-scan its own network.

**P1-9 · The 50 ms assignment scan holds the global mutex.** `session.go:699-757` recomputes rarity across every active piece × every connected peer, sorts, then walks every block of every piece — all inside `s.mu`, twenty times a second. Since `ActivePieces` drains the entire work queue (nothing bounds it), that is O(all-pieces × 50 peers) per tick, and every peer goroutine touching session state blocks behind it. Maintain an availability counter incrementally on `Have`/`Bitfield`, keep pieces in rarity buckets, and bound `ActivePieces` (see P0-1).

**P1-10 · Duplicate requests are issued outside the endgame.** `session.go:740` re-offers any block in flight for more than 2.5 s to a second peer, regardless of `inEndgame`. `MsgCancel` is defined (`message.go:19`) but never sent, so the first peer keeps uploading the block anyway. On a slow-but-healthy connection this doubles bandwidth use for no benefit. Restrict duplication to true endgame and send `Cancel` to the losers when a block lands.

---

## 4. Medium findings (P2)

- **`downloadLoop`'s in-band error signalling is fragile.** `Buffer == nil` means "disk write failed" and `ReceivedBlocks == -1` means "hash mismatch" (`session.go:622-630`). Use an explicit result struct with an `error` field.
- **No `context.Context` anywhere.** `Run` ends in `select {}` (`session.go:271`) and the SIGINT handler calls `os.Exit(0)` (`session.go:246`) — no goroutine can be cancelled, no in-flight disk write is flushed, no test can shut a session down. Thread a context from `main` through every loop.
- **`loadState` runs twice** — once in `New` (`session.go:175`) and again in `Run` (`session.go:192`) — recomputing the byte accounting both times.
- **Speed accounting counts discarded duplicates.** `bytesDownloaded` is incremented on every received block (`session.go:571`), including ones dropped as unsolicited, so the displayed rate overstates real throughput. It also takes a mutex per block; use `atomic.Int64`.
- **`metainfo` requires `announce`.** `metainfo.go:70` errors out if the top-level `announce` key is missing, even when a perfectly valid `announce-list` is present. Trackerless (DHT-only) torrents are rejected outright.
- **No sanity bounds on torrent geometry.** `TotalLength` and piece count are accepted as-is, then `preallocateFiles` truncates files to that size — a torrent claiming 900 TB will happily try. Cap piece count, cross-check against available disk space before allocating, and reject absurd `piece length` values (outside 16 KiB … 64 MiB).
- **One file handle open/close per piece write and per seed read** (`session.go:897`, `session.go:469`). Keep an LRU cache of open handles and use `WriteAt`/`ReadAt`, which are one syscall, need no seek, and are safe under concurrency.
- **No rate limiting** — global or per-peer, up or down. A single torrent will saturate the uplink.
- **Deprecated stdlib.** `ioutil.Discard` (`logger.go:19-20`) and `ioutil.ReadAll` (`tracker.go:112`) have been superseded by `io.*` since Go 1.16; `go.mod` targets 1.24.
- **Dead and unreachable code.** `decode.go:36` and `decode.go:39` are unreachable statements after `return` (this is what makes `go vet ./...` fail); `session.go:516` is an empty `if`; `chokingLoop` uses `for { select { case <-ticker.C: } }` where `for range ticker.C` would do.
- **`go.sum` lists `jackpal/bencode-go` with no corresponding `go.mod` requirement.** Run `go mod tidy`.
- **`session.go` is a 1,003-line god-file** holding orchestration, piece picking, choking, disk I/O, tracker logic and terminal rendering.
- **Mixed-language comments.** Bosnian comments remain throughout `session.go`; the project standard is English (see `CLAUDE.md`).

---

## 5. Performance deep dive

Ordered by expected impact on real download throughput.

1. **Fix the write interleaving (P0-2) and the choking contradiction (P0-5).** Corrupted frames and dropped requests cost more throughput than any tuning.
2. **Bound memory (P0-1).** Swapping is not a download strategy.
3. **Adaptive pipelining.** `PipelineSize` is a fixed 50 (`peer.go:22`). The right queue depth is the bandwidth-delay product: measure per-peer throughput and RTT and set `depth = clamp(throughput × rtt / 16 KiB, 2, 500)`. A 100 Mbps/10 ms peer wants ~8 outstanding blocks; a 1 Gbps/100 ms peer wants ~800. One constant cannot serve both, and 50 is badly wrong at both ends.
4. **Incremental rarity (P1-9).** Turn a 20 Hz O(P×N) scan into O(1) updates plus O(log P) selection.
5. **Buffer pooling.** Every block allocates a fresh `[]byte` in `ReadMessage` (`peer.go:317`) and every piece allocates a buffer. At 50 MB/s that is ~3,200 allocations/second feeding the GC. A `sync.Pool` of 16 KiB blocks and one of piece buffers removes nearly all of it.
6. **`WriteAt`/`ReadAt` + handle cache.** Removes two syscalls and a lock per block on the seed path.
7. **Streaming hash.** Instead of buffering a whole piece and calling `sha1.Sum`, hash blocks into a rolling `hash.Hash` as they arrive in offset order (falling back to a full hash when they do not). Removes one full pass over every piece.
8. **Bencode `[]byte` instead of `string`.** The decoder converts every string — including the multi-megabyte `pieces` blob — through `string(strBytes)` (`decode.go:142`), then converts back with `[]byte(...)` at every use site. Returning `[]byte` removes two copies of the largest object in the file.
9. **Reduce lock scope.** Split `s.mu` into `piecesMu` and `peersMu`, or eliminate shared state entirely with the actor model below.

---

## 6. Security deep dive

### Threat model

| Attacker | Capability today | Highest-severity outcome |
|---|---|---|
| Hostile `.torrent` file | Fully controls paths, sizes, piece count, nesting depth | **Arbitrary file write** (P0-3); stack-exhaustion panic (P1-7); disk exhaustion |
| Hostile / compromised tracker | Controls response body and the peer list | Unbounded memory (P1-8); directs the client at internal IPs for port scanning |
| Hostile peer | Sends arbitrary wire messages | **Memory exhaustion** from one request (P0-4); poisons piece buffers into an infinite re-download loop; unlimited message rate with no throttling |
| Network observer | Sees all traffic | Everything is plaintext — no MSE/PE encryption, so traffic is trivially classified and throttled by ISPs |
| Local | Reads/writes the `.state` file | Forge progress; make the client serve corrupt data to the swarm (P1-4) |

### Additional hardening

- **Fingerprinting.** The peer ID prefix is a fixed `-GT0001-` (`tracker.go:86`) with no version rotation, and the client sends no `User-Agent`. Between that and the absence of encryption, this client is trivially identifiable in a swarm.
- **No connection limits per IP or subnet** — one host can occupy all 50 peer slots.
- **No message rate limiting** — a peer can flood `Have` messages as cheap CPU/lock pressure on the session.
- **Add `govulncheck` and `gosec` to CI.** Both are free and would have flagged part of the above.
- **Consider `-privacy` / proxy support** (SOCKS5 for both tracker and peers) as a first-class flag rather than an afterthought.

---

## 7. Proposed target architecture

The current design is one process, one torrent, one god-struct guarded by one mutex. The natural evolution keeps the good instinct (a single owner of piece state) and makes it structural.

```
cmd/gottrent/          CLI, flags, signal handling, context root
internal/
  bencode/             (renamed from gobencode) []byte-based, depth-limited,
                       + raw-slice capture so InfoHash never depends on re-encoding
  metainfo/            v1 + v2 (BEP 52) parsing; path sanitization lives HERE
  storage/             file layout, handle cache, ReadAt/WriteAt, allocation policy,
                       verification, per-file priorities
  tracker/
    http/  udp/        behind one Tracker interface
  dht/                 BEP 5
  peer/
    conn.go            framing + read/write goroutine pair, owns the socket
    client.go          per-peer state machine, publishes events
    handshake.go       + BEP 10 extension handshake, MSE
  picker/              PiecePicker interface: rarest-first | sequential | priority,
                       endgame policy, incremental availability index
  choker/              tit-for-tat + optimistic unchoke, upload-rate ranked
  ratelimit/           token buckets, global and per-peer
  torrent/             ONE actor goroutine per torrent; owns all piece state;
                       inbox of typed events; no shared mutexes
  engine/              multi-torrent manager, lifecycle, persistence
  rpc/                 daemon API
  ui/                  status line, TUI
```

Two rules make the concurrency tractable:

1. **State is owned, never shared.** The torrent actor owns piece state; each peer owns its own connection state. They exchange typed events over channels. `s.mu` disappears, and with it every race in P0-5.
2. **Every goroutine takes a `context.Context` and returns on cancellation.** Lifecycle managed with `errgroup`. `os.Exit(0)` from a signal handler disappears; shutdown flushes state properly.

---

## 8. Protocol roadmap (by value per unit of work)

| BEP | Feature | Why it matters | Effort |
|---|---|---|---|
| **15** | UDP trackers | The majority of public trackers are UDP-only. Today those torrents simply fail. | S |
| — | **Incoming connections + NAT-PMP/UPnP** | Without a listener the client barely participates in swarms (P1-2). | S |
| **5** | DHT | Trackerless operation; dramatically better peer discovery. | L |
| **9 + 10** | Magnet links + extension protocol | Magnet links are how torrents are shared in 2026. Requires metadata exchange. | M |
| **11** | PEX | Nearly free peer discovery once BEP 10 exists. | S |
| **6** | Fast extension | `HaveAll`/`HaveNone`/`AllowedFast` — better cold-start and less handshake traffic. | S |
| **23** | Compact peer lists | Already implemented for responses; formalize. | done |
| **19** | WebSeeds | HTTP mirrors — perfect for the Linux ISO use case sitting in this repo. | M |
| **29** | µTP / LEDBAT | Background-friendly congestion control; stops saturating the user's uplink. | L |
| **52** | BitTorrent v2 | SHA-256 merkle trees, per-block verification, hybrid torrents. Few Go clients support it — genuine differentiation. | L |
| **14** | Local Service Discovery | Instant LAN peers; makes demos impressive. | S |
| — | MSE/PE encryption | Avoids ISP throttling; many peers require it. | M |

---

## 9. Innovation — where this project could actually stand out

The BitTorrent client space is crowded. These are the directions where a from-scratch Go client can be *better*, not just *another*.

### 9.1 "Explain mode" — turn the educational goal into the product

The project's stated purpose is education, but nothing about the runtime teaches. Add `-trace out.jsonl` emitting a structured event per protocol action (handshake, choke, request, block, hash result, picker decision *with its reasoning*), plus a small static web viewer that replays the file: a piece map filling in over time, a swarm graph, per-peer contribution, and an annotated timeline of every choke/unchoke decision.

Nothing else in the ecosystem does this well. It is the single highest-leverage differentiator available here, it directly serves the project's own goal, and it doubles as the best debugging tool you could build for the stalls in P0-2/P0-5.

### 9.2 Deterministic swarm simulator

A virtual clock plus an in-memory network (configurable latency, bandwidth, loss, seeder/leecher mix, churn) lets you run a full 200-peer swarm in milliseconds with reproducible results. Then:

- piece-picking and choking strategies become pluggable and **measurable** — run a tournament, publish the numbers;
- the stall bugs above become regression tests rather than field reports;
- `go test` covers the concurrent logic that is currently untestable.

This is a genuinely novel contribution for a Go BitTorrent client and it is mostly ordinary engineering.

### 9.3 Streaming mode

`-stream :8080` with a sequential-plus-deadline picker and an HTTP byte-range server: point VLC or a browser at the client and watch a video while it downloads. Requires a picker that prioritizes the playback window and a small readahead buffer — a natural payoff for the pluggable `picker` package, and immediately impressive in a demo.

### 9.4 Transmission-compatible RPC daemon

Implement the `transmission-rpc` JSON API and the entire existing ecosystem of remote GUIs, mobile apps and shell tools works with GoTorrent on day one. Far more leverage than the Fyne GUI on the current roadmap — and it makes the Fyne/TUI front end a client of a clean API rather than a reach into internals.

### 9.5 Smaller ideas worth having

- **Bubble Tea TUI** — a much better fit for a CLI tool than Fyne, with a live piece map rendered in Unicode block glyphs (have / in-flight / missing / verified).
- **`gottrent create`** — torrent creation (v1, v2 and hybrid). Turns a client into a complete tool and exercises the bencode encoder that currently exists only to compute an InfoHash.
- **`gottrent verify`** — parallel re-hash of on-disk data with a progress bar.
- **Selective download** — per-file priorities with `skip`, so a 40 GB collection can fetch one file.
- **Cross-torrent block dedupe** — content-address downloaded pieces so re-downloading the same file under a different torrent is instant.
- **Live profiling** — `-pprof :6060` and expvar/Prometheus metrics (`gottrent_pieces_verified_total`, per-peer throughput histograms).
- **`log/slog`** replacing the hand-rolled logger: structured, levelled, and it feeds 9.1 for free.

---

## 10. Testing & CI strategy

Current state: 2 test functions, both in `internal/gobencode`, ~11% of packages covered, CI runs `build` and `test` only.

**Unit.** Bitfield edge cases (last-byte padding, out-of-range index); message serialize/parse round-trips; metainfo golden files (single-file, multi-file, nested paths, missing keys, **the path-traversal attack vectors from P0-3**); tracker response parsing (compact, dictionary, failure, malformed).

**Property & fuzz.** `decode(encode(x)) == x` for canonical values; `go test -fuzz=FuzzDecode ./internal/bencode` and `FuzzParseMessage ./internal/peer` — both are ideal fuzz targets that parse untrusted input, and both currently have known panics waiting (P1-7).

**Integration.** An in-process seeder and leecher over `127.0.0.1` with an `httptest.Server` fake tracker, asserting byte-identical output. Fast, deterministic, no network, and it would catch P0-2 immediately.

**Race.** `go test -race ./...` in CI, with the integration test above as the vehicle. This is the only way the P0-5 races become visible.

**Benchmarks.** `BenchmarkDecode` on a real `.torrent`, `BenchmarkPicker` at 50 peers × 10,000 pieces, `BenchmarkPieceWrite`.

**CI upgrades.** OS matrix (linux/windows/macos — this is a Windows-developed project with POSIX assumptions); `-race`; `golangci-lint` (staticcheck, errcheck, ineffassign); `gosec`; `govulncheck`; `gofmt -l` gate; coverage reporting; `goreleaser` for tagged cross-platform binaries. Note that `go vet ./...` fails today on `decode.go:36,39` and `gofmt -l .` flags five files — fix both before turning the gates on.

---

## 11. Prioritized action plan

| Phase | Work | Effort | Grade after |
|---|---|---|---|
| **0 — Stop the bleeding** | P0-1 memory cap · P0-2 single socket writer · P0-3 path sanitization + tests · P0-4 request validation · P0-5 minimum-viable race fix (atomics + delete the duplicate unchoke) · P1-1 goroutine leak · P1-7 depth limit · P1-8 `LimitReader` | ~2 days | **68** |
| **1 — Structural** | `context` everywhere · split `session.go` into `torrent`/`storage`/`picker`/`choker` · actor model, drop `s.mu` · `WriteAt` + handle cache · incremental rarity · periodic state checkpointing + verify-on-resume | ~1–2 weeks | **78** |
| **2 — Protocol completeness** | Listener + NAT-PMP/UPnP · UDP trackers (BEP 15) · Fast extension (BEP 6) · extension protocol + magnet + metadata (BEP 9/10) · PEX (BEP 11) · DHT (BEP 5) · upload accounting · rate limiting | ~3–4 weeks | **86** |
| **3 — Quality gates** | Integration + fuzz + race tests · golangci-lint/gosec/govulncheck · OS matrix · benchmarks · goreleaser · finish translating comments to English | ~1 week | **91** |
| **4 — Differentiation** | Trace mode + web viewer (9.1) · swarm simulator (9.2) · streaming mode (9.3) · Transmission RPC (9.4) · TUI · BEP 52 v2 | ongoing | **95+** |

The ordering matters: phases 0 and 1 are prerequisites for everything else, because adding DHT and magnet links on top of racy shared state and interleaved socket writes multiplies the debugging surface rather than the feature set.

---

## 12. Reference — findings index

| ID | Severity | Location | Summary |
|---|---|---|---|
| P0-1 | Critical | `session.go:427` | Allocates the whole torrent in RAM at startup |
| P0-2 | Critical | `peer.go:332` | Four goroutines write the same socket; frames interleave |
| P0-3 | Critical | `session.go:466,782,895` | Path traversal → arbitrary file write |
| P0-4 | Critical | `peer.go:245`, `session.go:455` | Unvalidated peer request → unbounded allocation |
| P0-5 | Critical | `peer.go:197-225`, `session.go:982` | Data races on peer state + contradictory choke logic |
| P1-1 | High | `peer.go:272` | `writeLoop` goroutine leaks per disconnect |
| P1-2 | High | — | No listening socket; cannot accept peers or truly seed |
| P1-3 | High | `peer.go:255`, `session.go:152` | Uploaded bytes never counted |
| P1-4 | High | `session.go:97,237` | Resume state unverified and saved only on Ctrl-C |
| P1-5 | High | `session.go:225` | Unbounded initial peer fan-out, no dedupe |
| P1-6 | High | `session.go:533` | Peer map keyed by forgeable peer ID |
| P1-7 | High | `decode.go:21` | Unbounded bencode recursion → stack exhaustion |
| P1-8 | High | `tracker.go:116` | Tracker response read without a size limit |
| P1-9 | High | `session.go:699` | O(pieces×peers) scan at 20 Hz under the global lock |
| P1-10 | High | `session.go:740` | Duplicate requests outside endgame; `Cancel` never sent |
