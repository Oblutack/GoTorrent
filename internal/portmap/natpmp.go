package portmap

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// NAT-PMP (RFC 6886): a small binary protocol over UDP port 5351, spoken
// directly to the default gateway.
const (
	natPMPPort = 5351

	natPMPOpExternalAddress = 0
	natPMPOpMapUDP          = 1
	natPMPOpMapTCP          = 2

	// natPMPMaxRetries bounds how long this client waits before concluding
	// the gateway does not speak NAT-PMP at all and letting Client fall
	// back to UPnP. RFC 6886 §3.1 recommends retrying for up to ~64 seconds
	// (9 attempts, doubling from 250ms) before giving up entirely; that is
	// far too slow for "detect NAT-PMP isn't supported, move on to UPnP"; a
	// gateway that speaks NAT-PMP at all replies almost immediately, so a
	// handful of quick attempts is enough to tell the difference between
	// "slow reply" and "no NAT-PMP here" without making every unsupported
	// gateway cost a minute.
	natPMPMaxRetries = 4
)

func natPMPOpcode(protocol string) (byte, error) {
	switch protocol {
	case "TCP":
		return natPMPOpMapTCP, nil
	case "UDP":
		return natPMPOpMapUDP, nil
	default:
		return 0, fmt.Errorf("portmap: unknown protocol %q", protocol)
	}
}

// natPMPMapper speaks NAT-PMP to a single gateway address. port is always
// natPMPPort in production; tests override it to talk to a fake gateway on
// loopback instead of needing to bind the real well-known port.
type natPMPMapper struct {
	gateway net.IP
	port    int
}

// newNATPMPMapper guesses the gateway address — see guessGateway — since
// there is no portable, dependency-free way to read the OS routing table
// across Windows/Linux/macOS from pure Go. It does not contact the gateway
// yet; that happens on the first addMapping/externalAddress call, so
// constructing one is cheap and never itself the reason NAT-PMP is
// considered unavailable.
func newNATPMPMapper() (*natPMPMapper, error) {
	gw, err := guessGateway()
	if err != nil {
		return nil, err
	}
	return &natPMPMapper{gateway: gw, port: natPMPPort}, nil
}

// guessGateway assumes the default gateway is this machine's own
// outbound-facing IP with the last octet replaced by 1 — the overwhelming
// convention for a home router's LAN address (192.168.1.1, 192.168.0.1,
// 10.0.0.1, and so on). This is a heuristic, not a lookup: it is wrong on a
// network whose gateway uses a different final octet (some routers default
// to .254), and the only consequence of that is NAT-PMP mapping failing
// cleanly, letting Client fall back to UPnP — whose SSDP discovery needs no
// such guess, since it multicasts to find the gateway instead of assuming
// where it is.
func guessGateway() (net.IP, error) {
	local, err := outboundIP()
	if err != nil {
		return nil, err
	}
	gw := make(net.IP, len(local))
	copy(gw, local)
	gw[len(gw)-1] = 1
	return gw, nil
}

func (m *natPMPMapper) name() string { return "NAT-PMP" }

func (m *natPMPMapper) addMapping(ctx context.Context, protocol string, internalPort uint16, lease time.Duration) (Mapping, error) {
	opcode, err := natPMPOpcode(protocol)
	if err != nil {
		return Mapping{}, err
	}

	req := make([]byte, 12)
	req[1] = opcode
	binary.BigEndian.PutUint16(req[4:6], internalPort)
	binary.BigEndian.PutUint16(req[6:8], internalPort) // requested external port: ask for the same number first
	binary.BigEndian.PutUint32(req[8:12], uint32(lease.Seconds()))

	resp, err := m.roundTrip(ctx, req)
	if err != nil {
		return Mapping{}, err
	}
	if len(resp) < 16 {
		return Mapping{}, fmt.Errorf("portmap: NAT-PMP mapping response is %d bytes, too short", len(resp))
	}
	if resp[1] != opcode|0x80 {
		return Mapping{}, fmt.Errorf("portmap: NAT-PMP mapping response has opcode %d, want %d", resp[1], opcode|0x80)
	}
	if result := binary.BigEndian.Uint16(resp[2:4]); result != 0 {
		return Mapping{}, fmt.Errorf("portmap: NAT-PMP gateway refused the mapping (result code %d)", result)
	}
	externalPort := binary.BigEndian.Uint16(resp[10:12])

	extIP, err := m.externalAddress(ctx)
	if err != nil {
		return Mapping{}, err
	}
	return Mapping{ExternalIP: extIP, ExternalPort: externalPort, Protocol: protocol}, nil
}

func (m *natPMPMapper) deleteMapping(ctx context.Context, protocol string, internalPort, _ uint16) error {
	// RFC 6886 §3.4: a mapping is deleted by requesting one with the same
	// internal port, external port 0, and lifetime 0 — keyed by internal
	// port, not external, which is why deleteMapping's externalPort
	// parameter is unused here (see the mapper interface's doc comment).
	opcode, err := natPMPOpcode(protocol)
	if err != nil {
		return err
	}
	req := make([]byte, 12)
	req[1] = opcode
	binary.BigEndian.PutUint16(req[4:6], internalPort)
	// req[6:8] external port and req[8:12] lifetime are already zero.

	_, err = m.roundTrip(ctx, req)
	return err
}

func (m *natPMPMapper) externalAddress(ctx context.Context) (net.IP, error) {
	resp, err := m.roundTrip(ctx, []byte{0, natPMPOpExternalAddress})
	if err != nil {
		return nil, err
	}
	if len(resp) < 12 {
		return nil, fmt.Errorf("portmap: NAT-PMP external-address response is %d bytes, too short", len(resp))
	}
	if resp[1] != 0x80 {
		return nil, fmt.Errorf("portmap: NAT-PMP external-address response has opcode %d, want 128", resp[1])
	}
	if result := binary.BigEndian.Uint16(resp[2:4]); result != 0 {
		return nil, fmt.Errorf("portmap: NAT-PMP gateway refused the external-address request (result code %d)", result)
	}
	ip := make(net.IP, 4)
	copy(ip, resp[8:12])
	return ip, nil
}

// roundTrip sends req to the gateway and waits for any reply, retrying with
// a doubling timeout (mirroring internal/tracker/udp.go's udpRoundTrip,
// which faces the same "UDP has no delivery guarantee" problem) until
// natPMPMaxRetries is exhausted or ctx is cancelled.
func (m *natPMPMapper) roundTrip(ctx context.Context, req []byte) ([]byte, error) {
	addr := &net.UDPAddr{IP: m.gateway, Port: m.port}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("portmap: dialing NAT-PMP gateway %s: %w", addr, err)
	}
	defer conn.Close()

	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.SetDeadline(time.Now())
		case <-watcherDone:
		}
	}()

	buf := make([]byte, 16)
	timeout := 250 * time.Millisecond
	for attempt := 0; attempt < natPMPMaxRetries; attempt++ {
		if _, err := conn.Write(req); err != nil {
			return nil, fmt.Errorf("portmap: writing to NAT-PMP gateway %s: %w", addr, err)
		}
		conn.SetReadDeadline(time.Now().Add(timeout))
		n, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				timeout *= 2
				continue
			}
			return nil, fmt.Errorf("portmap: reading from NAT-PMP gateway %s: %w", addr, err)
		}
		out := make([]byte, n)
		copy(out, buf[:n])
		return out, nil
	}
	return nil, fmt.Errorf("portmap: NAT-PMP gateway %s did not respond after %d attempts", addr, natPMPMaxRetries)
}
