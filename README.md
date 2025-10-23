---

# GoTorrent

---

## About The Project

**GoTorrent** is a BitTorrent client implementation written entirely in Go. The project was started as a deep dive into network programming, concurrency in Go (goroutines and channels), and understanding the BitTorrent protocol down to the finest details. All core components, including the Bencode (de)coder, Peer Wire Protocol logic, and download strategies, have been implemented from scratch.

This is not intended to be a replacement for mature, feature-rich clients, but rather a demonstration of skills and a journey through the complexities of distributed systems.

### Key Features

-   **`.torrent` File Parsing:** Full support for parsing metadata from `.torrent` files.
-   **Custom Bencode Parser:** A custom-built Bencode decoder and encoder, with no external dependencies for this critical component.
-   **Tracker Communication:** Sends announce requests to HTTP/HTTPS trackers and parses their responses to obtain peer lists.
-   **Concurrent Peer Connections:** The client connects to multiple peers simultaneously using goroutines.
-   **Peer Wire Protocol:**
    -   Proper BitTorrent `handshake`.
    -   Exchange of core messages: `Choke`, `Unchoke`, `Interested`, `Bitfield`, `Have`.
-   **Intelligent Piece Downloading:**
    -   Parallel block downloading from multiple peers.
    -   **"Rarest First"** piece selection strategy.
    -   **Pipelining** of `Request` messages to maximize connection throughput.
    -   **SHA-1 hash verification** for every downloaded piece.
-   **Disk I/O:** Correctly assembles pieces and writes them to the appropriate offsets for both single-file and multi-file torrents.
-   **Robustness:** Built-in timeout mechanism for "stuck" block requests.
-   **Seeding (Basic):** Ability to upload downloaded pieces to other peers.
-   **Download Resumption:** Saves the download state (`bitfield`) on exit and resumes from the last known state on restart.
-   **Modern CLI Display:** A dynamic status line showing progress, speed, and peer count, with an optional `-verbose` mode for detailed logging.

### Built With

-   **Core:** **Go** (v1.20+)
-   **Concurrency:** **Goroutines & Channels** for managing peer connections and download orchestration.
-   **Networking:** Standard `net` and `net/http` packages.
-   **Project Structure:** Clean architecture with separation of concerns (`session`, `peer`, `tracker`, `metainfo`, `bencode`).
-   **Testing:** Standard `testing` package with table-driven tests.
-   **CI/CD:** Configured for **GitHub Actions** for automated builds and tests. (*Note: This is planned and needs to be fully configured.*)

---

## Getting Started

To get a local copy up and running, follow these simple steps.

### Prerequisites

-   **Go** (version 1.20 or newer) must be installed.
    -   [https://golang.org/doc/install](https://golang.org/doc/install)

### Installation & Usage

1.  **Clone the repository:**
    ```sh
    git clone https://github.com/Oblutack/GoTorrent.git
    ```

2.  **Navigate to the project directory:**
    ```sh
    cd GoTorrent
    ```

3.  **Build the project:**
    ```sh
    go build ./cmd/gottrent/
    ```
    This will create an executable file `gottrent.exe` (on Windows) or `gottrent` (on Linux/macOS) in the root directory.

### Running the Client

The client is run from the command line with the following flags:

```
Usage of gottrent:
  -dir string
        Directory to save downloaded files (default ".")
  -port uint
        Port number for incoming peer connections (default 6881)
  -torrent string
        Path to the .torrent file
  -verbose
        Enable verbose logging
```

**Example:**
```sh
# Download a torrent into the current directory
./gottrent -torrent "path/to/your.torrent"

# Download a torrent into a 'downloads' directory with detailed logging
./gottrent -torrent "path/to/your.torrent" -dir "downloads" -verbose
```

---

## Project Structure

The project follows a standard Go layout for better organization and maintainability:

```
gottrent/
├── cmd/gottrent/      # Main application entrypoint (main.go)
├── internal/          # All internal code, not intended for external import
│   ├── bencode/       # Custom Bencode (de)coder
│   ├── logger/        # Controls logging levels (verbose/standard)
│   ├── metainfo/      # .torrent file parser
│   ├── peer/          # P2P communication (Peer Wire Protocol)
│   ├── session/       # Download orchestration and management
│   └── tracker/       # Tracker communication
├── .github/           # CI/CD configuration (GitHub Actions)
└── ...
```

---

## Roadmap

-   [x] Custom Bencode Parser
-   [x] Metainfo `.torrent` File Parsing
-   [x] HTTP/S Tracker Communication
-   [x] Concurrent Peer Connections
-   [x] Peer Wire Protocol (Handshake, core messages)
-   [x] "Rarest First" Piece Selection Strategy
-   [x] Pipelining `Request` Messages
-   [x] Hash Verification & Disk I/O
-   [x] "Stuck" Request Timeout
-   [x] Sending `Have` Messages
-   [x] Clean CLI Display with Progress & Speed
-   [x] Basic Seeding Logic
-   [x] Download Resumption
-   [ ] **Seeding:** Advanced Choking/Unchoking Algorithm (e.g., Tit-for-Tat)
-   [ ] **UI:** Graphical User Interface using **Fyne**
-   [ ] **Advanced Features:**
    -   [ ] UDP Tracker Support (BEP-0015)
    -   [ ] DHT Support (BEP-0005)
    -   [ ] Magnet Link & Metadata Exchange Support (BEP-0009)

---

## License

Distributed under the MIT License. See `LICENSE` for more information.

---

## Contact

Your Name - [@Oblutack](https://github.com/Oblutack) - kamenjas.evvel@gmail.com

Project Link: [https://github.com/Oblutack/GoTorrent](https://github.com/Oblutack/GoTorrent)
