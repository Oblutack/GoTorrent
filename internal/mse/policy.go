package mse

import "fmt"

// Policy selects how a peer connection negotiates MSE/PE, per torrent
// (outbound) or per engine (inbound, since which torrent an encrypted
// inbound connection is for isn't known until negotiation completes).
type Policy int

const (
	// PolicyDisabled never attempts MSE, outbound or inbound — today's
	// behavior, byte-for-byte, and the default. Every caller of this
	// package must treat PolicyDisabled as a complete no-op: no
	// bufio.Reader constructed, no extra read, nothing.
	PolicyDisabled Policy = iota
	// PolicyPrefer attempts MSE first. Outbound, a failed negotiation
	// (the peer doesn't speak it, or the handshake errors/times out)
	// falls back to a fresh, plain classic handshake on a new connection
	// rather than giving up on that peer entirely. Inbound, it accepts
	// either a classic or an MSE-negotiated connection.
	PolicyPrefer
	// PolicyRequired refuses to fall back: an outbound dial that fails to
	// negotiate MSE is treated as an ordinary dial failure with no retry,
	// and an inbound connection that turns out to be a classic
	// (unencrypted) handshake is refused outright.
	PolicyRequired
)

// String renders p as the CLI/JSON string ParsePolicy accepts back.
func (p Policy) String() string {
	switch p {
	case PolicyDisabled:
		return "disabled"
	case PolicyPrefer:
		return "prefer"
	case PolicyRequired:
		return "required"
	default:
		return fmt.Sprintf("Policy(%d)", int(p))
	}
}

// ParsePolicy parses the CLI/JSON-config string form of Policy. An empty
// string is accepted as PolicyDisabled so a zero-value config field (JSON
// omits it, or a flag was never passed) behaves exactly like an explicit
// "disabled" rather than needing special-casing at every call site.
func ParsePolicy(s string) (Policy, error) {
	switch s {
	case "", "disabled":
		return PolicyDisabled, nil
	case "prefer":
		return PolicyPrefer, nil
	case "required":
		return PolicyRequired, nil
	default:
		return PolicyDisabled, fmt.Errorf("mse: unknown encryption policy %q (want \"disabled\", \"prefer\", or \"required\")", s)
	}
}
