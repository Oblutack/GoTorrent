package torrent

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/peer"
	"github.com/Oblutack/GoTorrent/internal/tracker"
)

// BEP 55 (holepunch) — a peer already connected to two others that cannot
// reach each other directly (both behind a NAT/firewall blocking inbound
// connections) can relay a rendezvous between them, then each attempts a
// connection using the address the relay learned for the other.
//
// Real spec text (bittorrent.org/beps/bep_0055.html) fetched and read
// before writing anything, per this project's own established discipline
// for BEP-numbered work: don't trust memory for protocol details. Unlike
// BEP 40, this one turned out to have no bit-level formula to get subtly
// wrong — the wire format is a small fixed struct (see peer.HolepunchMessage)
// and the normative requirements are all plain message-sequencing rules,
// not arithmetic — so there was no test-vector step the way BEP 40 needed
// one.
//
// **One honest, deliberate deviation from the letter of the spec, worth
// stating plainly rather than burying: the spec's own connection step says
// "each peer initiates a uTP connection to the other peer" — this project
// has no µTP (BEP 29) implementation at all, a separately-scoped and
// substantially larger undertaking (see ROADMAP.md). The signaling half
// (rendezvous/connect/error, the relay's forwarding logic, every error
// code) is implemented exactly per spec below; the actual connection
// attempt a "connect" message triggers uses this client's existing TCP
// dial path (the same Torrent.dial every other peer-discovery mechanism
// already uses) instead of µTP. This means real UDP hole-punching through
// a restrictive NAT — the specific hard case µTP-based holepunching
// exists to solve — will not reliably work here; what does still work is
// the easier, genuinely useful case of a relay telling two peers about
// each other's real reachable address and having them race a normal TCP
// connection, which succeeds whenever at least one side's NAT permits an
// inbound TCP attempt to a freshly-used port (a full-cone or a restricted-
// cone NAT on at least one end) or is not actually NATed at all. A worse
// approximation than the real thing, not a fake one.
//
// pendingHolepunches (torrent.go) is this torrent's outstanding rendezvous
// requests, keyed by holepunchKey(relayAddr, targetIP, targetPort) — a
// connect or error is only ever acted on if it matches an entry here,
// deliberately: an unsolicited connect pointing this client at an
// arbitrary address would otherwise let any connected peer make this
// client attempt outbound connections anywhere it likes, a real abuse
// vector the spec itself does not explicitly warn about but which this
// client is not going to expose regardless.

const (
	// holepunchPendingTimeout bounds how long a sent rendezvous stays in
	// pendingHolepunches waiting for a connect or error that may simply
	// never come (the spec defines no obligation for a relay to answer at
	// all if it silently declines) — see expireHolepunches.
	holepunchPendingTimeout = 30 * time.Second
)

// holepunchKey identifies one outstanding rendezvous request.
func holepunchKey(relayAddr string, targetIP net.IP, targetPort uint16) string {
	return relayAddr + "|" + net.JoinHostPort(targetIP.String(), strconv.Itoa(int(targetPort)))
}

// RequestHolepunch asks an already-connected peer (relayAddr, exactly as
// it appears in Peers()) to relay a BEP 55 rendezvous with target — used
// when a discovered peer's direct dial has failed and some other
// connected peer might already have a working connection to it. This
// client does not decide *when* to do this on its own (a real automatic
// "which of my peers might also know this target" heuristic is a
// deliberately unbuilt gap — see this file's own package doc comment);
// it only implements the wire protocol correctly once asked, by whichever
// caller (or a future heuristic) decides a holepunch is worth trying.
func (t *Torrent) RequestHolepunch(relayAddr string, target net.IP, targetPort uint16) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{
		kind:                ctrlRequestHolepunch,
		holepunchRelayAddr:  relayAddr,
		holepunchTargetIP:   target,
		holepunchTargetPort: targetPort,
		errReply:            resp,
	}:
	case <-t.done:
		return ErrClosed
	}
	select {
	case err := <-resp:
		return err
	case <-t.done:
		return ErrClosed
	}
}

func (t *Torrent) doRequestHolepunch(relayAddr string, target net.IP, targetPort uint16) error {
	pc, ok := t.peers[relayAddr]
	if !ok {
		return fmt.Errorf("torrent: not connected to relay %s", relayAddr)
	}
	if !pc.client.SupportsUtHolepunch() {
		return fmt.Errorf("torrent: relay %s does not support the holepunch extension", relayAddr)
	}
	if target == nil || targetPort == 0 {
		return errors.New("torrent: invalid holepunch target")
	}

	t.pendingHolepunches[holepunchKey(relayAddr, target, targetPort)] = time.Now()
	return pc.client.SendHolepunch(peer.HolepunchRendezvous, target, targetPort, 0)
}

// expireHolepunches drops any pendingHolepunches entry older than
// holepunchPendingTimeout — piggybacked on the same pick-interval ticker
// run() already has, rather than a dedicated one, since this is pure
// housekeeping with no reason to run on its own schedule. Called
// unconditionally (unlike tick, which returns early before metadata is
// known) since a relay request has nothing to do with the picker at all.
func (t *Torrent) expireHolepunches(now time.Time) {
	for key, sentAt := range t.pendingHolepunches {
		if now.Sub(sentAt) > holepunchPendingTimeout {
			delete(t.pendingHolepunches, key)
		}
	}
}

// onHolepunchMessage dispatches an arrived BEP 55 message by type. pc is
// the connection it arrived on — the relay for a connect/error reply we
// requested, or the initiator for a rendezvous we're being asked to relay.
func (t *Torrent) onHolepunchMessage(pc *peerConn, msg peer.HolepunchMessage) {
	switch msg.Type {
	case peer.HolepunchRendezvous:
		t.relayRendezvous(pc, msg)
	case peer.HolepunchConnect:
		t.handleHolepunchConnect(pc, msg)
	case peer.HolepunchError:
		t.handleHolepunchError(pc, msg)
	default:
		logger.Logf("torrent %s: holepunch message from %s has unknown type %d, ignoring\n", t.infoHash, pc.addr, msg.Type)
	}
}

// relayRendezvous is the relay's whole job (BEP 55's central mechanism):
// pc just asked to be introduced to msg.Addr/msg.Port. Every error path
// replies on pc's own connection with the address/port unchanged from the
// request, exactly as the spec requires, so the initiator can match the
// error back to the right outstanding attempt.
func (t *Torrent) relayRendezvous(pc *peerConn, msg peer.HolepunchMessage) {
	reject := func(code uint32) {
		if err := pc.client.SendHolepunch(peer.HolepunchError, msg.Addr, msg.Port, code); err != nil {
			logger.Logf("torrent %s: sending holepunch error to %s: %v\n", t.infoHash, pc.addr, err)
		}
	}

	if msg.Port == 0 || msg.Addr == nil || msg.Addr.IsUnspecified() {
		reject(peer.HolepunchErrNoSuchPeer)
		return
	}
	if t.isOwnAddress(msg.Addr, msg.Port) {
		reject(peer.HolepunchErrNoSelf)
		return
	}
	target := t.findPeerByAddr(msg.Addr, msg.Port)
	if target == nil {
		reject(peer.HolepunchErrNotConnected)
		return
	}
	if !target.client.SupportsUtHolepunch() {
		reject(peer.HolepunchErrNoSupport)
		return
	}
	fromIP, fromPort, ok := connRemoteIPPort(pc.client.Conn)
	if !ok {
		reject(peer.HolepunchErrNoSuchPeer)
		return
	}

	if err := pc.client.SendHolepunch(peer.HolepunchConnect, msg.Addr, msg.Port, 0); err != nil {
		logger.Logf("torrent %s: sending holepunch connect to %s: %v\n", t.infoHash, pc.addr, err)
	}
	if err := target.client.SendHolepunch(peer.HolepunchConnect, fromIP, fromPort, 0); err != nil {
		logger.Logf("torrent %s: sending holepunch connect to %s: %v\n", t.infoHash, target.addr, err)
	}
}

// handleHolepunchConnect is the initiator's side: the relay has told us
// how to reach the target. Only acted on if it matches a rendezvous we
// actually sent — see this file's own package doc comment for why. BEP
// 55's own "MUST ignore connect messages if already connected to the
// other" requirement falls out of dial's existing dedup check for free,
// with no extra code needed here.
func (t *Torrent) handleHolepunchConnect(relayPC *peerConn, msg peer.HolepunchMessage) {
	key := holepunchKey(relayPC.addr, msg.Addr, msg.Port)
	if _, pending := t.pendingHolepunches[key]; !pending {
		return
	}
	delete(t.pendingHolepunches, key)
	t.dial(tracker.PeerInfo{IP: msg.Addr, Port: msg.Port})
}

// handleHolepunchError is the initiator's side of a rejected rendezvous —
// logged, not retried automatically (same "caller decides when to try
// again" posture RequestHolepunch itself already takes).
func (t *Torrent) handleHolepunchError(relayPC *peerConn, msg peer.HolepunchMessage) {
	key := holepunchKey(relayPC.addr, msg.Addr, msg.Port)
	if _, pending := t.pendingHolepunches[key]; !pending {
		return
	}
	delete(t.pendingHolepunches, key)
	logger.Logf("torrent %s: holepunch via %s for %s:%d failed: %s\n",
		t.infoHash, relayPC.addr, msg.Addr, msg.Port, peer.HolepunchErrorString(msg.ErrCode))
}

// findPeerByAddr looks up a connected peer by its observed connection
// address — net.Conn.RemoteAddr(), not peerConn.peerInfo. This matters:
// peerInfo is only ever populated for a peer this client dialed itself
// (see its own doc comment in torrent.go), but the whole point of BEP 55
// is helping a peer that *cannot* be dialed — one that reached us
// inbound. For an inbound connection from behind a NAT, RemoteAddr() is
// the NAT's own public mapped address, which is exactly the address a
// third party's own rendezvous request would name (since it's the same
// address a tracker or another peer would have observed that peer
// connecting *from*) and exactly the address hole-punching depends on
// being reachable.
func (t *Torrent) findPeerByAddr(ip net.IP, port uint16) *peerConn {
	for _, pc := range t.peers {
		pcIP, pcPort, ok := connRemoteIPPort(pc.client.Conn)
		if ok && pcPort == port && pcIP.Equal(ip) {
			return pc
		}
	}
	return nil
}

// isOwnAddress reports whether ip/port name this torrent's own known
// external listening endpoint (Config.LocalIP/ListenPort — see BEP 40's
// canonicalpriority.go for where LocalIP comes from). Nil-safe: without a
// known LocalIP (no UPnP/NAT-PMP gateway, the common case this whole
// project's live-testing sandbox has hit repeatedly) this can never
// return true, which is the correct conservative default — treating an
// unknown address as "not us" rather than risking a false NoSelf
// rejection.
func (t *Torrent) isOwnAddress(ip net.IP, port uint16) bool {
	return t.cfg.LocalIP != nil && port == t.cfg.ListenPort && t.cfg.LocalIP.Equal(ip)
}

// connRemoteIPPort splits a connection's remote address into an IP and
// port, the form every holepunch address comparison in this file needs.
func connRemoteIPPort(conn net.Conn) (net.IP, uint16, bool) {
	if conn == nil {
		return nil, 0, false
	}
	host, portStr, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return nil, 0, false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, 0, false
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, 0, false
	}
	return ip, uint16(port), true
}
