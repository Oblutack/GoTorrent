package torrent

import "net"

// isLANAddr reports whether addr (a "host:port" or bare host) resolves to a
// private or loopback IP — 3.3's Config.ExcludeLANFromLimits uses this to
// skip DownLimit/UpLimit for a same-network peer entirely, so a LAN
// transfer always runs at full local speed regardless of an internet-facing
// cap. An address that fails to parse as an IP (should not happen for
// anything peer.NewClient/AcceptClient has already connected to) is
// conservatively treated as not LAN, i.e. still subject to the limit.
func isLANAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback()
}
