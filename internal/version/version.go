// Package version centralizes this client's identity: the same version
// backs both the BitTorrent peer ID prefix and the HTTP User-Agent header,
// so a tracker or peer sees one consistent client identity everywhere
// instead of two independently-hardcoded strings that could drift apart.
package version

// String is this build's human-readable version. Bumped by hand for now —
// no build-time injection via ldflags yet, that's real release-pipeline
// work (see ROADMAP's goreleaser mention in Phase 6).
const String = "0.1.0"

// PeerIDDigits is String's Azureus-style (BEP 20) 4-digit encoding.
const PeerIDDigits = "0100"

// PeerIDPrefix is the full 8-byte BEP 20 peer ID prefix: "-" + a 2-letter
// client code ("GT" for GoTorrent) + PeerIDDigits + "-".
const PeerIDPrefix = "-GT" + PeerIDDigits + "-"

// UserAgent is sent as the HTTP User-Agent header on tracker announces.
const UserAgent = "GoTorrent/" + String
