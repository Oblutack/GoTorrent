// Package portmap automatically opens a port through a home router's NAT
// for inbound BitTorrent connections, using whichever of UPnP IGD or
// NAT-PMP (RFC 6886) the gateway supports. Without this, inbound
// connections (internal/engine's TCP listener, internal/peer's
// AcceptClient) only work if the port happens to already be forwarded
// manually or the client is not behind a NAT at all — which is why the
// roadmap called this "the biggest cheap win" of 2.6.
package portmap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrNoGateway is returned when neither a UPnP IGD nor a NAT-PMP gateway
// could be found (or neither would grant a mapping) on the local network.
var ErrNoGateway = errors.New("portmap: no UPnP or NAT-PMP gateway available")

// Mapping is one successful port mapping, as granted by the gateway. A
// gateway is free to grant a different external port than the one
// requested (a collision with an existing mapping, for instance) — callers
// must use ExternalPort, not the port they asked for.
type Mapping struct {
	ExternalIP   net.IP
	ExternalPort uint16
	Protocol     string // "TCP" or "UDP"
}

// leaseDuration is what every mapping requests and what drives the renewal
// timer. Real gateways commonly do not honor it exactly — some UPnP
// implementations grant it verbatim, some cap it far lower, some grant a
// permanent lease and ignore it entirely, and NAT-PMP gateways often cap it
// at a few minutes regardless of what was asked for. None of that matters
// much: what actually matters is renewing well before whatever lease was
// actually granted would lapse, which is what renewBefore (in client.go) is
// for — the requested value is a starting point, not a promise.
const leaseDuration = 1 * time.Hour

// mapper is the protocol-specific half a *Client tries in turn. Unexported:
// callers only ever need Client, never a specific protocol implementation.
type mapper interface {
	// name identifies the protocol, for logging (e.g. "UPnP", "NAT-PMP").
	name() string
	// addMapping requests a mapping from internalPort to an external port
	// for lease, for both the initial mapping and every renewal.
	addMapping(ctx context.Context, protocol string, internalPort uint16, lease time.Duration) (Mapping, error)
	// deleteMapping withdraws a previously added mapping. internalPort and
	// externalPort are both passed through because the two protocols key a
	// mapping differently: NAT-PMP deletes by internal port, UPnP's
	// DeletePortMapping by external port — each implementation uses
	// whichever one it actually needs.
	deleteMapping(ctx context.Context, protocol string, internalPort, externalPort uint16) error
}

// outboundIP returns the local IP address this machine would use to reach
// the public internet — found by "connecting" a UDP socket to an address
// outside any private range (UDP connect never actually sends a packet; it
// only consults the routing table to pick a source address) and reading
// back what was chosen. This is the standard portable trick for the
// question "what is my LAN-facing IP", used here for two purposes: as the
// NewInternalClient UPnP asks for, and as the basis of guessGateway's
// heuristic.
func outboundIP() (net.IP, error) {
	conn, err := net.Dial("udp4", "203.0.113.1:80") // TEST-NET-3 (RFC 5737): never actually reachable, never actually sent to
	if err != nil {
		return nil, fmt.Errorf("portmap: determining the local outbound address: %w", err)
	}
	defer conn.Close()
	ip := conn.LocalAddr().(*net.UDPAddr).IP.To4()
	if ip == nil {
		return nil, errors.New("portmap: local outbound address is not IPv4")
	}
	return ip, nil
}
