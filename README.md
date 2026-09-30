# GoTorrent

<p align="center">
  <img src="https://raw.githubusercontent.com/Oblutack/GoTorrent/main/assets/logo.png" alt="GoTorrent Logo" width="300"/>
</p>
<p align="center">
  <em>A feature-rich BitTorrent ecosystem written from scratch — a dependency-free Go engine, a headless daemon with a REST/WebSocket control API, and three clients on top of it: an Avalonia desktop app, a Bubble Tea terminal UI, and a .NET control-plane Hub.</em>
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
    <img src="https://img.shields.io/badge/Go-1.26%2B-blue.svg" alt="Go Version">
    <img src="https://img.shields.io/badge/.NET-10-blue.svg" alt=".NET Version">
</p>

## About The Project

**GoTorrent** started as a BitTorrent client implemented entirely from scratch in Go — bencode, the peer wire protocol, trackers (HTTP and UDP), mainline DHT, and everything else, hand-rolled with no third-party dependencies — and has grown into a full ecosystem with four clients on one engine:

1. **The Go engine and CLI** (`gottrent`) — the original client: magnet links, a modern peer-discovery stack (DHT/PEX/LSD), BitTorrent v2/hybrid torrents, µTP, MSE/PE encryption, and the kind of day-to-day features a client like qBittorrent or µTorrent has.
2. **`gottrentd`**, a headless daemon exposing that same engine over a REST + WebSocket control API (bearer-token auth, live events, everything a real GUI needs).
3. **Three clients on top of that API**: `GoTorrent.Desktop`, a full Avalonia desktop GUI; `gottrent-tui`, a Bubble Tea terminal UI for a headless box or an SSH session; and `GoTorrent.Hub`, an ASP.NET Core control plane for the things an engine shouldn't own itself (multi-node aggregation, RSS auto-download rules, history, real user auth) that any of the above can sit behind.

It's still, first and foremost, an educational project and a demonstration of skills across three ecosystems (Go network programming, a modern .NET/Avalonia stack, and a terminal-UI framework) — not an attempt to replace a mature client for daily use — but it's well past "toy" at this point: a magnet link with zero trackers downloads over DHT alone, the CLI runs a full multi-torrent fleet with persistence across restarts, a BitTorrent v2 magnet link reconstructs its own SHA-256 merkle piece layers straight from peers, and the desktop app is a real qBittorrent-style GUI with a live piece map (colour-coded by which peer delivered each piece), speed graphs, and OS-level integration (tray, notifications, file associations, drag-and-drop).

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

**Advanced protocol**
- **BitTorrent v2 (BEP 52)**, full scope including magnet support: SHA-256 merkle-tree piece verification, hybrid v1/v2 torrents (interoperable with plain v1 peers), and — the hard part — a v2-only magnet link with zero prior metadata reconstructing its own `piece_layers` from peers over three new wire messages, then downloading and verifying byte-exact.
- **µTP (BEP 29)**, a hand-rolled LEDBAT (RFC 6817) implementation over UDP — the congestion-control scheme that keeps a torrent from saturating the household's upstream, sharing DHT's UDP port for real inbound support.
- **MSE/PE (Message Stream Encryption)** — Diffie-Hellman handshake obfuscation plus optional RC4, to get past DPI-based throttling of "BitTorrent protocol"-shaped traffic, with a forced/preferred/disabled policy.
- **BEP 19 web seeds** — download straight from an HTTP mirror, no peers required.

**Differentiators**
- **Explain/trace mode** (`-trace out.jsonl`): a structured event log of everything the actor did and why — including the picker's own reasoning for each piece it started — replayed by a dependency-free browser viewer with a piece map, swarm graph, and annotated choke timeline.
- **A deterministic swarm simulator** (`gottrent-sim`) driving the real picker/choker strategy code on a virtual clock — hundreds of peers and hours of swarm time in milliseconds of real time, for comparing strategies (e.g. rarest-first vs. sequential) empirically rather than by argument.
- **Streaming mode** (`-stream :PORT`): serves managed torrents over HTTP with byte-range support and deadline-aware piece prioritization — point a media player at it and watch while it's still downloading.
- **Cross-torrent piece dedup**: re-adding an already-seeded release under a second torrent completes instantly by copying already-verified pieces, never re-downloading them.

**`gottrentd` — the headless daemon and control API**
- A long-lived, JSON-configured daemon wrapping the same engine, with a full REST API (list/add/detail/files/peers/trackers/pieces/pause/resume/verify/reannounce/patch/delete/session) plus a hand-rolled WebSocket event stream (no third-party router or WS library — stdlib `net/http` and a hand-rolled RFC 6455 implementation).
- Bearer-token auth, a DNS-rebinding defense (Host-header allowlist), and brute-force lockout on the control API.

**`gottrent-tui` — the terminal UI**
- A Bubble Tea client on `gottrentd`'s own control API for a headless box or an SSH session: a live torrent table, a detail view (files/peers/trackers), an add-torrent prompt, and real-time updates over the same WebSocket stream Desktop uses.
- This project's one deliberate external dependency (`charmbracelet/bubbletea`/`lipgloss`/`bubbles`) — every other package here is hand-rolled specifically because the stdlib had nothing or a dependency would be disproportionate; a real terminal UI's raw-mode/ANSI-rendering problem is genuinely the opposite case.

**`GoTorrent.Hub` — the .NET control plane**
- ASP.NET Core, built on top of `gottrentd`'s API: multi-node aggregation (register several daemons, one API for all of them), RSS-driven auto-download rules, history/analytics that outlive the engine process, and real user auth (ASP.NET Core Identity + JWT) with a SignalR fan-out of every connected node's live events to a single client connection.
- EF Core/SQLite persistence, Docker support, and an engine bearer token encrypted at rest via ASP.NET Core Data Protection.

**`GoTorrent.Desktop` — the Avalonia desktop client**
- A full qBittorrent-style GUI talking straight to `gottrentd`: sortable/filterable torrent list with categories and tags, a detail pane (files with per-file priority, peers, trackers, a live piece map, live speed graph), preferences, and statistics.
- Live updates over the same WebSocket stream: real-time list updates, a piece map colour-coded by which peer delivered each piece, per-torrent speed/ETA and a rolling sparkline, and toast notifications with optimistic UI (e.g. undo-delete).
- Native OS integration: tray icon with minimize-to-tray, autostart with Windows, `.torrent`/`magnet:` file association, drag-and-drop, native OS notifications on completion, and daemon supervision (attaches to an already-running `gottrentd`, or spawns one).
- A "why is this slow?" diagnostics panel that explains a stalled torrent (no peers, no seeds, everyone choking, every tracker failing, queue-held, rate-limited) using signals already in the API — no guessing required.
- Light/dark theme with a density toggle, window-geometry persistence, and a disk-space guard before adding a torrent that won't fit.
- Per-torrent notes (local, private) and, when connected to a `GoTorrent.Hub` instance, an activity-history view of every torrent that's ever completed across the fleet.
- A packaged Windows installer (Inno Setup) bundling the desktop app plus `gottrentd`/`gottrent`, built and attached to every tagged GitHub release automatically.

## Built With

**Go engine, CLI, and daemon**
- **Language:** Go 1.26+ (see `go.mod` for the exact pinned patch — bumped regularly to stay clean under `govulncheck`). The engine itself has no external dependencies at all — bencode, the peer wire protocol, DHT, µTP, MSE/PE, and a hand-rolled RFC 6455 WebSocket implementation are all from scratch. The one deliberate exception is `gottrent-tui`'s Bubble Tea stack (`charmbracelet/bubbletea`/`lipgloss`/`bubbles`) — a real terminal UI's raw-mode/rendering problem is judged genuinely disproportionate to hand-roll, unlike everything else here.
- **Concurrency:** an actor per torrent (one goroutine owning that torrent's state, everything else talking to it over channels) plus goroutines for every peer connection, tracker announce loop, and background service.
- **Networking:** the standard `net` and `net/http` packages only — TCP, UDP, and TLS are all hand-driven, including the DHT, UDP tracker, and µTP wire formats.
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

- **Go 1.24 or newer** — [https://golang.org/doc/install](https://golang.org/doc/install) (for `gottrent`/`gottrentd`/`gottrent-tui`/`gottrent-sim`; `go.mod` pins an exact patch version and `GOTOOLCHAIN=auto` fetches it automatically if your installed Go is older)
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

### Running the terminal UI

`gottrent-tui` talks to the exact same `gottrentd` control API as the desktop app — the natural choice for a headless box or an SSH session:

```sh
go build ./cmd/gottrent-tui/
./gottrent-tui -api-address 127.0.0.1:6880
```

It prompts for the bearer token on first connect (pre-filled from `-token` or the default token file if either is given/found) and never auto-connects. Once in: a live torrent table refreshed both on a poll and by real WebSocket events, `Tab` to cycle a selected torrent's Files/Peers/Trackers, and `a` to add a magnet/URL/local path.

---

## Project Structure

```
GoTorrent/
├── cmd/
│   ├── gottrent/          # CLI entry point: the fleet manager plus the create/verify subcommands
│   ├── gottrentd/         # Headless daemon (JSON-configured, long-lived)
│   ├── gottrent-tui/      # Bubble Tea terminal UI, talking to gottrentd's own control API
│   └── gottrent-sim/      # Deterministic swarm simulator CLI (real picker/choker on a virtual clock)
├── internal/
│   ├── api/               # gottrentd's REST + WebSocket control API, bearer-token auth
│   ├── bencode/           # Bencode encoder/decoder
│   ├── bitfield/          # Shared piece-bitmap type
│   ├── bootstrap/         # Shared engine startup sequence (used by both cmd/gottrent and cmd/gottrentd)
│   ├── choker/            # Tit-for-tat choking algorithm
│   ├── debugserver/       # Optional pprof/expvar profiling endpoint
│   ├── dht/               # Mainline DHT (BEP 5), plus BEP 42 (security extension)
│   ├── engine/            # Multi-torrent fleet manager: add/list/remove, manifest, queueing, IP filter, proxy, events, ...
│   ├── ipfilter/          # eMule/PeerGuardian blocklist parsing and lookup
│   ├── logger/            # Verbose/standard logging
│   ├── lsd/               # Local Service Discovery (BEP 14)
│   ├── merkle/            # BEP 52's SHA-256 merkle tree (piece/file roots, inclusion proofs)
│   ├── metainfo/          # .torrent parsing (v1/v2/hybrid), magnet URIs, torrent creation and export
│   ├── mse/               # Message Stream Encryption (MSE/PE) — DH handshake + optional RC4
│   ├── peer/              # Peer wire protocol
│   ├── picker/            # Piece selection strategies, availability tracking, priorities
│   ├── portmap/           # UPnP / NAT-PMP port mapping
│   ├── proxy/             # SOCKS5 / HTTP CONNECT proxy dialer
│   ├── ratelimit/         # Token-bucket rate limiter
│   ├── simulator/         # The deterministic swarm simulator (internal/torrent's real picker/choker, virtual time)
│   ├── storage/           # On-disk file layout, allocation, verification (v1, v2, and mmap-backed)
│   ├── stream/            # HTTP byte-range streaming with deadline-aware piece prioritization
│   ├── torrent/           # The per-torrent actor and its state machine
│   ├── trace/             # Explain/trace mode — structured JSONL event log
│   ├── tracker/           # HTTP(S) and UDP tracker clients (including BEP 48 scrape)
│   ├── tui/               # The Bubble Tea program (Model/Update/View) behind cmd/gottrent-tui
│   ├── tuiclient/         # A typed client for gottrentd's control API, used by internal/tui
│   ├── udpmux/            # Lets DHT and inbound µTP share one UDP port
│   ├── utp/               # µTP (BEP 29): LEDBAT congestion control over UDP
│   ├── version/           # Client identity (peer ID / User-Agent)
│   ├── webseed/           # BEP 19 web seed downloading
│   └── ws/                # Hand-rolled RFC 6455 WebSocket server, used by internal/api
├── web/trace-viewer/      # Dependency-free static page that replays a -trace JSONL file
├── src/
│   ├── Hub/               # GoTorrent.Hub — the .NET control plane (Api/Core/Infrastructure)
│   └── Desktop/           # GoTorrent.Desktop — the Avalonia desktop client
├── tests/                 # xUnit test projects for the Hub and the Desktop app
├── installer/             # Windows installer (Inno Setup), built and attached to tagged releases by CI
├── GoTorrent.sln          # One monorepo solution for both .NET projects, alongside the Go module
├── .goreleaser.yml        # Cross-platform Go binary releases, triggered by a version tag
├── .github/workflows/     # CI: go.yml, dotnet.yml (every push), release.yml (tag push only)
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
| 6 | `GoTorrent.Desktop` — an Avalonia desktop client | Done |
| 7 | Advanced protocol: µTP, MSE/PE encryption, BitTorrent v2 | Done |
| 8 | Differentiators (trace mode, a deterministic swarm simulator, streaming) | Done |

Every phase is done. What's left is a short, deliberately-scoped list of gaps that were never meant to be built out fully, each a conscious call rather than something silently dropped: Transmission RPC/Web UI compatibility (Phase 4, a stretch goal), quota/schedule policy for multiple Hub-registered nodes (Phase 5, an optional extra), IPv6 for the UDP tracker and DHT (BEP 32), per-tracker state exposure (nothing consumes it yet), and signed installer builds (blocked on owning a real code-signing certificate, not something a self-signed one should stand in for).

---

## Making a release

Pushing a version tag is the entire release process — `.github/workflows/release.yml` does the rest, with no manual build/upload steps:

```sh
git tag v0.3.0
git push origin v0.3.0
```

That triggers two jobs:

1. **`goreleaser`** (Ubuntu) cross-compiles `gottrent`/`gottrentd`/`gottrent-tui`/`gottrent-sim` for Windows/Linux/macOS × amd64/arm64 (4 binaries × 3 OSes × 2 arches = 24 build targets), zips each OS/arch pair, generates a checksums file and a changelog from the commits since the last tag, and creates the GitHub release itself (config: `.goreleaser.yml`).
2. **`windows-installer`** (Windows, depends on the first job so the release already exists) runs `installer\build.ps1` — a real Windows build, `dotnet publish` for a self-contained Desktop executable plus native `go build` for the CLI/daemon — compiles the Inno Setup script, and uploads `GoTorrentSetup.exe` onto that same release.

Both jobs use the repo's own `GITHUB_TOKEN` — no secrets to configure. A tag that doesn't match `v*` never triggers this workflow at all, so it's fully separate from the ordinary `go.yml`/`dotnet.yml` CI that gates every push. To test the Go side locally without actually tagging anything, `goreleaser build --snapshot --clean` runs every real cross-compile target and reports failures the same way CI would.

---

## License

Distributed under the MIT License. See `LICENSE` for more information.

---

## Contact

[@Oblutack](https://github.com/Oblutack) — kamenjas.evvel@gmail.com

Project Link: [https://github.com/Oblutack/GoTorrent](https://github.com/Oblutack/GoTorrent)
