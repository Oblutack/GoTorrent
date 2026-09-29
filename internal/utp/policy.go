package utp

import "fmt"

// Policy selects how a peer connection negotiates µTP as its transport —
// the same three-value shape internal/mse.Policy already established for
// encryption, reused deliberately for consistency rather than inventing
// a second vocabulary for what is structurally the same kind of choice:
// "prefer the special thing, fall back to plain TCP on failure" vs.
// "require it outright."
type Policy int

const (
	// PolicyDisabled never attempts µTP, outbound or inbound — today's
	// TCP-only behavior, byte-for-byte, and the default.
	PolicyDisabled Policy = iota
	// PolicyPrefer attempts a µTP dial first; on failure, falls back to a
	// fresh plain TCP dial rather than giving up on that peer entirely.
	// Inbound, it accepts connections over either transport.
	PolicyPrefer
	// PolicyRequired refuses to fall back: a failed µTP dial is treated
	// as an ordinary dial failure with no TCP retry, and (inbound, once
	// wired into internal/engine) a plain TCP connection is refused
	// outright.
	PolicyRequired
)

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
// string is accepted as PolicyDisabled, matching internal/mse.ParsePolicy's
// own reasoning: a zero-value config field should behave exactly like an
// explicit "disabled" with no special-casing needed at any call site.
func ParsePolicy(s string) (Policy, error) {
	switch s {
	case "", "disabled":
		return PolicyDisabled, nil
	case "prefer":
		return PolicyPrefer, nil
	case "required":
		return PolicyRequired, nil
	default:
		return PolicyDisabled, fmt.Errorf("utp: unknown policy %q (want \"disabled\", \"prefer\", or \"required\")", s)
	}
}
