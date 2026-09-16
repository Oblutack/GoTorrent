package engine

import (
	"net"

	"github.com/Oblutack/GoTorrent/internal/storage"
)

// DaemonInfo is a status-bar-oriented snapshot of this process's
// daemon-level state — the pieces that live on the Engine itself, not on
// any one managed torrent: which port peers actually connect to, whether
// that port is reachable from outside via NAT traversal, DHT/LSD/PEX
// status, and how much disk space is left where downloads land. Stage 5's
// answer to "nothing exposes enough for a real status bar."
type DaemonInfo struct {
	// ListenPort is the port actually bound for inbound peer connections —
	// read from the real listener, not Defaults.ListenPort, since
	// StartPortMapping overwrites that field with the external port on
	// success (see its own doc comment), which would otherwise make the
	// internal port unrecoverable once mapping succeeds. 0 if Listen was
	// never called (or Defaults.ListenPort was 0, meaning "don't listen").
	ListenPort uint16
	// ExternalPort is the port a NAT gateway granted via StartPortMapping,
	// or 0 if port mapping never succeeded (no gateway found, mapping
	// failed, or StartPortMapping was never called).
	ExternalPort uint16
	// PortMapped is ExternalPort != 0, spelled out so a caller doesn't have
	// to remember 0 means "not mapped" rather than "mapped to port 0."
	PortMapped bool
	// DHTRunning is whether StartDHT has bound a node for this fleet.
	DHTRunning bool
	// DHTNodeCount is the DHT routing table's current size — 0 whenever
	// DHTRunning is false, meaningless otherwise.
	DHTNodeCount int
	// LSDRunning is whether StartLSD has joined the local multicast group.
	LSDRunning bool
	// PEXEnabled is always true — BEP 11 peer exchange has no engine-level
	// switch at all in this codebase, unlike DHT/LSD: it's compiled in and
	// always on per-torrent, never something a StartXxx call turns on for
	// the whole fleet. Reported anyway so a status bar doesn't have to
	// hardcode the same fact a second time on its own side.
	PEXEnabled bool
	// FreeDiskBytes is the free space on the volume holding
	// Defaults.DownloadDir, or -1 if it could not be determined — the same
	// advisory, best-effort nature storage.checkFreeSpace already has, for
	// the identical reason (e.g. a network filesystem that can't answer).
	FreeDiskBytes int64
}

// Info reports this Engine's current daemon-level state — see DaemonInfo.
func (e *Engine) Info() DaemonInfo {
	e.mu.Lock()
	listener := e.listener
	portmapClient := e.portmapClient
	dhtNode := e.dhtNode
	lsdNode := e.lsdNode
	downloadDir := e.defaults.DownloadDir
	e.mu.Unlock()

	info := DaemonInfo{PEXEnabled: true, FreeDiskBytes: -1}
	if listener != nil {
		if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
			info.ListenPort = uint16(tcpAddr.Port)
		}
	}
	if portmapClient != nil {
		info.ExternalPort = portmapClient.ExternalPort()
		info.PortMapped = info.ExternalPort != 0
	}
	if dhtNode != nil {
		info.DHTRunning = true
		info.DHTNodeCount = dhtNode.NodeCount()
	}
	info.LSDRunning = lsdNode != nil
	if downloadDir != "" {
		if free, err := storage.AvailableSpace(downloadDir); err == nil {
			info.FreeDiskBytes = free
		}
	}
	return info
}
