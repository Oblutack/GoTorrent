# GoTorrent

<p align="center">
  <img src="https://raw.githubusercontent.com/Oblutack/GoTorrent/main/assets/logo.png" alt="GoTorrent Logo" width="300"/>
</p>
<p align="center">
  <em>A feature-rich BitTorrent ecosystem written from scratch — a dependency-free Go engine, a headless daemon with a REST/WebSocket control API, a .NET control-plane Hub, and an Avalonia desktop client.</em>
</p>
<p align="center">
    <a href="https://github.com/Oblutack/GoTorrent/actions/workflows/go.yml">
        <img src="https://github.com/Oblutack/GoTorrent/actions/workflows/go.yml/badge.svg" alt="Go Build Status">
    </a>
    <a href="https://github.com/Oblutack/GoTorrent/actions/workflows/dotnet.yml">
        <img src="https://github.com/Oblutack/GoTorrent/actions/workflows/dotnet.yml/badge.svg" alt=".NET Build Status">
    </a>
    <a href="https://goreportcard.com/report/github.com/Oblutack/GoTorrent">
        <img src="https://goreportcard.com/badge/github.com/Oblutack/GoTorrent" alt="Go Report Card">
    </a>
    <a href="https://github.com/Oblutack/GoTorrent/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT">
    </a>
    <img src="https://img.shields.io/badge/Go-1.24%2B-blue.svg" alt="Go Version">
    <img src="https://img.shields.io/badge/.NET-10-blue.svg" alt=".NET Version">
</p>

## About The Project

**GoTorrent** started as a BitTorrent client implemented entirely from scratch in Go — bencode, the peer wire protocol, trackers (HTTP and UDP), mainline DHT, and everything else, hand-rolled with no third-party dependencies — and has grown into a full three-layer ecosystem:

1. **The Go engine and CLI** (`gottrent`) — the original client: magnet links, a modern peer-discovery stack (DHT/PEX/LSD), and the kind of day-to-day features a client like qBittorrent or µTorrent has.
2. **`gottrentd`**, a headless daemon exposing that same engine over a REST + WebSocket control API (bearer-token auth, live events, everything a real GUI needs).
3. **Two .NET front ends on top of that API**: `GoTorrent.Hub`, an ASP.NET Core control plane for the things an engine shouldn't own itself (multi-node aggregation, RSS auto-download rules, history, real user auth), and `GoTorrent.Desktop`, a full Avalonia desktop client — the actual day-to-day GUI, talking straight to `gottrentd`.

It's still, first and foremost, an educational project and a demonstration of skills across two ecosystems (Go network programming and a modern .NET/Avalonia stack) — not an attempt to replace a mature client for daily use — but it's well past "toy" at this point: a magnet link with zero trackers downloads over DHT alone, the CLI runs a full multi-torrent fleet with persistence across restarts, and the desktop app is a real qBittorrent-style GUI with a live piece map (colour-coded by which peer delivered each piece), speed graphs, and OS-level integration (tray, notifications, file associations, drag-and-drop).

### Key Features

**Core protocol**
- Custom bencode codec, `.torrent` parsing, and SHA-1 piece verification, all with no external dependencies.
- Full peer wire protocol: handshake, choke/interest, have/bitfield, request/piece/cancel, and the Fast extension (BEP 6) for downloading during choke.
- Rarest-first (with reservoir-sampled tie-breaking) and sequential piece selection, adaptive per-peer pipelining, and endgame mode.
- Real tit-for-tat choking with an optimistic-unchoke slot.

**Magnet links and peer discovery**
- Magnet URIs, including fetching the info dictionary over BEP 9 metadata exchange straight from peers.
- Mainline DHT (BEP 5), HTTP/HTTPS and UDP (BEP 15) trackers, peer exchange (BEP 11), and local service discovery (BEP 14) — a magnet link with no trackers at all still finds peers and downloads through DHT alone.
- Inbound connections with automatic UPnP/NAT-PMP port mapping, and private-torrent compliance (BEP 27: no DHT/PEX/LSD for a private swarm).

**Client features**
- Multi-torrent fleet management with a persisted manifest — add, list, remove, and pick everything back up automatically after a restart.
- Per-file selection and priority (skip / low / normal / high), with first-and-last-piece-first for previewable partial downloads.
- Queueing: max active downloads/seeds/total, reorderable queue positions, and force-start.
- Bandwidth: global and per-torrent rate limits that compose together, per-torrent upload slots, a LAN-exclusion option, and a weekly alternative-speed schedule.
- Seeding policy: ratio and seed-time limits that pause a torrent automatically.
- Organization: categories with per-category save paths, tags, a watch folder, content-layout options, moving a torrent's data after the fact, and running a command on completion.
- Torrent creation and maintenance: build a new `.torrent` from a file or directory, verify existing data on disk in parallel, add a tracker to a running torrent, and export a `.torrent` file from a magnet once its metadata arrives.

**Networking and privacy**
- SOCKS5 and HTTP CONNECT proxy support for peer connections and HTTP(S) tracker announces, with an option to resolve DNS through the proxy too.
- An IP filter with eMule `ipfilter.dat` and PeerGuardian `.p2p` blocklist support, including auto-update from a URL.
- Anonymous mode: a fingerprint-free peer ID, LSD disabled, and a hard refusal to start without an actual proxy configured.

**`gottrentd` — the headless daemon and control API**
- A long-lived, JSON-configured daemon wrapping the same engine, with a full REST API (list/add/detail/files/peers/trackers/pieces/pause/resume/verify/reannounce/patch/delete/session) plus a hand-rolled WebSocket event stream (no third-party router or WS library — stdlib `net/http` and a hand-rolled RFC 6455 implementation).
- Bearer-token auth, a DNS-rebinding defense (Host-header allowlist), and brute-force lockout on the control API.

**`GoTorrent.Hub` — the .NET control plane**
- ASP.NET Core, built on top of `gottrentd`'s API: multi-node aggregation (register several daemons, one API for all of them), RSS-driven auto-download rules, history/analytics that outlive the engine process, and real user auth (ASP.NET Core Identity + JWT) with a SignalR fan-out of every connected node's live events to a single client connection.
- EF Core/SQLite persistence, Docker support, and an engine bearer token encrypted at rest via ASP.NET Core Data Protection.

**`GoTorrent.Desktop` — the Avalonia desktop client**
- A full qBittorrent-style GUI talking straight to `gottrentd`: sortable/filterable torrent list with categories and tags, a detail pane (files with per-file priority, peers, trackers, a live piece map, live speed graph), preferences, and statistics.
- Live updates over the same WebSocket stream: real-time list updates, a piece map colour-coded by which peer delivered each piece, per-torrent speed/ETA and a rolling sparkline, and toast notifications with optimistic UI (e.g. undo-delete).
- Native OS integration: tray icon with minimize-to-tray, autostart with Windows, `.torrent`/`magnet:` file association, drag-and-drop, native OS notifications on completion, and daemon supervision (attaches to an already-running `gottrentd`, or spawns one).
- A "why is this slow?" diagnostics panel that explains a stalled torrent (no peers, no seeds, everyone choking, every tracker failing, queue-held, rate-limited) using signals already in the API — no guessing required.
- Light/dark theme with a density toggle, window-geometry persistence, and a disk-space guard before adding a torrent that won't fit.

## Built With

**Go engine, CLI, and daemon**
- **Language:** Go 1.24+, no external dependencies anywhere in the module — including the WebSocket implementation (RFC 6455, hand-rolled).
- **Concurrency:** an actor per torrent (one goroutine owning that torrent's state, everything else talking to it over channels) plus goroutines for every peer connection, tracker announce loop, and background service.
- **Networking:** the standard `net` and `net/http` packages only — TCP, UDP, and TLS are all hand-driven, including the DHT and UDP tracker wire formats.
- **Testing:** the standard `testing` package, with real fixtures (real loopback sockets, real fake peers and trackers) rather than mocks wherever the code touches the network or disk.

**.NET Hub and Desktop**
- **Language/runtime:** .NET 10 (C#), one monorepo solution (`GoTorrent.sln`) alongside the Go module.
- **Hub:** ASP.NET Core, EF Core + SQLite, ASP.NET Core Identity + JWT, SignalR, `Microsoft.Extensions.Http.Resilience` (Polly v8) for calls to each `gottrentd` node, Docker.
- **Desktop:** Avalonia UI + `CommunityToolkit.Mvvm`, talking to `gottrentd` over a plain `HttpClient` and a real `ClientWebSocket`; hand-drawn `Control`s (piece map, speed graph, sparkline) rather than a charting dependency.
- **Testing:** xUnit on both, following the same "real fixtures over mocks" convention as the Go side wherever it's cheap (a real in-memory SQLite database, a real loopback WebSocket server) and fakes for pure ViewModel/service logic.

**CI/CD:** GitHub Actions, two independent pipelines — the Go workflow gates on `gofmt`, `go vet`, a build, and `go test -race`; the .NET workflow gates on `dotnet format --verify-no-changes`, a build, and `dotnet test`.

---

## Getting Started

### Prerequisites

- **Go 1.24 or newer** — [https://golang.org/doc/install](https://golang.org/doc/install) (for `gottrent`/`gottrentd`)
- **.NET 10 SDK** — [https://dotnet.microsoft.com/download](https://dotnet.microsoft.com/download) (for the Hub and/or the Desktop app — optional if you only want the CLI)

### Installation & Usage

1. **Clone the repository:**
   ```sh
   git clone https://github.com/Oblutack/GoTorrent.git
   cd GoTorrent
   ```

2. **Build the client:**
   ```sh
   go build ./cmd/gottrent/
   ```
   This produces `gottrent.exe` (Windows) or `gottrent` (Linux/macOS) in the current directory.

### Running the client

`gottrent` is a fleet manager: `-torrent` may be repeated, and torrents added in a previous run are picked back up automatically from the manifest even with no `-torrent` flags at all.

```sh
# Download a torrent into the current directory
./gottrent -torrent "path/to/your.torrent"

# A magnet link, into a specific directory, with verbose logging
./gottrent -torrent "magnet:?xt=urn:btih:..." -dir downloads -verbose

# Several torrents at once, with a global download cap and a queue limit
./gottrent -torrent a.torrent -torrent b.torrent -down-limit 2048 -max-active-downloads 2
```

Run `./gottrent -h` for the full flag list (30+ flags across networking, bandwidth, queueing, organization, and privacy). The most commonly used ones:

| Flag | What it does |
|---|---|
| `-torrent <path\|magnet>` | Add a torrent or magnet link (repeatable) |
| `-dir <path>` | Where to save downloaded files |
| `-port <n>` / `-random-port` | Listen port for inbound connections |
| `-down-limit` / `-up-limit <KiB/s>` | Fleet-wide bandwidth caps |
| `-ratio-limit` / `-seed-time-limit` | Pause a torrent once it's seeded enough |
| `-max-active-downloads` / `-max-active-seeds` | Queue limits |
| `-watch-dir <path>` | Auto-add `.torrent` files dropped into a folder |
| `-proxy-type socks5\|http` | Route peer/tracker traffic through a proxy |
| `-anonymous-mode` | Strip the client fingerprint (requires a proxy) |
| `-verbose` | Detailed logging |

`gottrent` also has two subcommands:

```sh
# Build a new .torrent from a file or directory
./gottrent create -tracker "http://tracker.example/announce" -private ./my-files

# Re-verify existing data on disk against a .torrent, in parallel
./gottrent verify -dir downloads my.torrent
```

### Running the daemon and the desktop app

`gottrentd` is the headless daemon the Hub and the Desktop app both talk to. It generates its own bearer token on first run.

```sh
go build ./cmd/gottrentd/
./gottrentd -api-address 127.0.0.1:6880
```

The Desktop app is the easiest way to actually use the daemon day to day:

```sh
dotnet run --project src/Desktop/GoTorrent.Desktop
```

On first launch it asks for `gottrentd`'s address and token (the token `gottrentd` generated on its own first run, at `<config dir>/GoTorrent/api-token`) — or use its "Start gottrentd" button, which finds and launches (or attaches to an already-running) `gottrentd.exe` next to the app's own binary. See `src/Hub/README.md` for running the optional .NET Hub control plane (multi-node aggregation, RSS rules, history, remote auth) in front of one or more daemons.

---

## Project Structure

```
GoTorrent/
├── cmd/
│   ├── gottrent/          # CLI entry point: the fleet manager plus the create/verify subcommands
│   └── gottrentd/         # Headless daemon (JSON-configured, long-lived)
├── internal/
│   ├── api/               # gottrentd's REST + WebSocket control API, bearer-token auth
│   ├── bencode/           # Bencode encoder/decoder
│   ├── bitfield/          # Shared piece-bitmap type
│   ├── bootstrap/         # Shared engine startup sequence (used by both cmd/gottrent and cmd/gottrentd)
│   ├── choker/            # Tit-for-tat choking algorithm
│   ├── dht/               # Mainline DHT (BEP 5)
│   ├── engine/            # Multi-torrent fleet manager: add/list/remove, manifest, queueing, IP filter, proxy, events, ...
│   ├── ipfilter/          # eMule/PeerGuardian blocklist parsing and lookup
│   ├── logger/            # Verbose/standard logging
│   ├── lsd/               # Local Service Discovery (BEP 14)
│   ├── metainfo/          # .torrent parsing, magnet URIs, torrent creation and export
│   ├── peer/              # Peer wire protocol
│   ├── picker/            # Piece selection strategies, availability tracking, priorities
│   ├── portmap/           # UPnP / NAT-PMP port mapping
│   ├── proxy/             # SOCKS5 / HTTP CONNECT proxy dialer
│   ├── ratelimit/         # Token-bucket rate limiter
│   ├── storage/           # On-disk file layout, allocation, verification
│   ├── torrent/           # The per-torrent actor and its state machine
│   ├── tracker/           # HTTP(S) and UDP tracker clients
│   ├── version/           # Client identity (peer ID / User-Agent)
│   └── ws/                # Hand-rolled RFC 6455 WebSocket server, used by internal/api
├── src/
│   ├── Hub/               # GoTorrent.Hub — the .NET control plane (Api/Core/Infrastructure)
│   └── Desktop/           # GoTorrent.Desktop — the Avalonia desktop client
├── tests/                 # xUnit test projects for the Hub and the Desktop app
├── GoTorrent.sln          # One monorepo solution for both .NET projects, alongside the Go module
├── .github/workflows/     # CI: go.yml (gofmt/vet/build/race tests) and dotnet.yml (format/build/test)
└── ...
```

---

## Demo

**Desktop app** — a live transfer across a real multi-peer swarm: the torrent list, the live piece map, the swarm graph (per-peer contribution, choke state, and progress, colour-coded), and the command palette (Ctrl+K).

![GoTorrent.Desktop in action](assets/gotorrent-desktop-demo.gif)

**CLI**, standard mode:

![GoTorrent in action](assets/gif1.gif)

<p align="center">
  <em>For a more detailed, behind-the-scenes look at the P2P communication, check out the <a href="assets/gif_verbose.gif">verbose mode demo</a>.</em>
</p>

---

## Roadmap

Development follows a phased plan, each phase gated behind the last:

| Phase | Theme | Status |
|---|---|---|
| 0 | Stabilize — critical bug and security fixes | Done |
| 1 | Re-architecture — the torrent-actor engine core | Done |
| 2 | Magnet links + modern peer discovery (DHT, PEX, LSD, NAT traversal) | Done |
| 3 | Client feature parity (queueing, bandwidth, organization, privacy, ...) | Done |
| 4 | Daemon + control API (`gottrentd`, REST/WebSocket) | Done |
| 5 | `GoTorrent.Hub` — an ASP.NET Core control plane | Done |
| 6 | `GoTorrent.Desktop` — an Avalonia desktop client | **In progress** |
| 7 | Advanced protocol: µTP, MSE/PE encryption, BitTorrent v2 | Planned |
| 8 | Differentiators (trace mode, a deterministic swarm simulator, streaming) | Planned |

Phase 6's core GUI (torrent list, detail pane, live piece map/speed graph, native OS integration, theming) is fully functional; a handful of "differentiator" features (a command palette, swarm visualisation, per-torrent notes/history) and installer packaging are still open. A couple of small, deliberately-scoped gaps remain elsewhere too: Transmission RPC compatibility (Phase 4, a stretch goal) and quota/schedule policy for multiple Hub-registered nodes (Phase 5, an optional extra).

---

## License

Distributed under the MIT License. See `LICENSE` for more information.

---

## Contact

[@Oblutack](https://github.com/Oblutack) — kamenjas.evvel@gmail.com

Project Link: [https://github.com/Oblutack/GoTorrent](https://github.com/Oblutack/GoTorrent)
