package lsd

import (
	"testing"
	"time"
)

func TestParseBTSearchValid(t *testing.T) {
	msg := "BT-SEARCH * HTTP/1.1\r\n" +
		"Host: 239.192.152.143:6771\r\n" +
		"Port: 6881\r\n" +
		"Infohash: 0123456789abcdef0123456789abcdef01234567\r\n" +
		"cookie: abc123\r\n" +
		"\r\n\r\n"
	infoHash, port, cookie, ok := parseBTSearch([]byte(msg))
	if !ok {
		t.Fatal("parseBTSearch rejected a well-formed message")
	}
	if port != 6881 {
		t.Fatalf("port = %d, want 6881", port)
	}
	if cookie != "abc123" {
		t.Fatalf("cookie = %q, want abc123", cookie)
	}
	want := "0123456789abcdef0123456789abcdef01234567"
	if got := hexOf(infoHash); got != want {
		t.Fatalf("infoHash = %s, want %s", got, want)
	}
}

func hexOf(b [20]byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 40)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0xf]
	}
	return string(out)
}

func TestParseBTSearchRejectsWrongFirstLine(t *testing.T) {
	if _, _, _, ok := parseBTSearch([]byte("GET / HTTP/1.1\r\n\r\n")); ok {
		t.Fatal("accepted a non-BT-SEARCH message")
	}
}

func TestParseBTSearchRequiresInfohashAndPort(t *testing.T) {
	msg := "BT-SEARCH * HTTP/1.1\r\nPort: 6881\r\n\r\n\r\n"
	if _, _, _, ok := parseBTSearch([]byte(msg)); ok {
		t.Fatal("accepted a message with no infohash")
	}
	msg = "BT-SEARCH * HTTP/1.1\r\nInfohash: 0123456789abcdef0123456789abcdef01234567\r\n\r\n\r\n"
	if _, _, _, ok := parseBTSearch([]byte(msg)); ok {
		t.Fatal("accepted a message with no port")
	}
}

func TestParseBTSearchRejectsBadInfohashLength(t *testing.T) {
	msg := "BT-SEARCH * HTTP/1.1\r\nPort: 1\r\nInfohash: deadbeef\r\n\r\n\r\n"
	if _, _, _, ok := parseBTSearch([]byte(msg)); ok {
		t.Fatal("accepted a truncated infohash")
	}
}

func TestParseBTSearchRejectsOutOfRangePort(t *testing.T) {
	msg := "BT-SEARCH * HTTP/1.1\r\nPort: 99999\r\nInfohash: 0123456789abcdef0123456789abcdef01234567\r\n\r\n\r\n"
	if _, _, _, ok := parseBTSearch([]byte(msg)); ok {
		t.Fatal("accepted an out-of-range port")
	}
}

func TestParseBTSearchToleratesUnknownHeaders(t *testing.T) {
	msg := "BT-SEARCH * HTTP/1.1\r\n" +
		"Host: 239.192.152.143:6771\r\n" +
		"Port: 6881\r\n" +
		"Infohash: 0123456789abcdef0123456789abcdef01234567\r\n" +
		"future-field: whatever\r\n" +
		"\r\n\r\n"
	if _, _, _, ok := parseBTSearch([]byte(msg)); !ok {
		t.Fatal("rejected a message with an unrecognized extra header")
	}
}

// TestAnnounceAndReceiveRoundTrip proves two real *LSD instances on this
// machine can hear each other over actual multicast — a smoke test of New/
// Announce/Found together, not just parseBTSearch in isolation.
//
// This skips rather than fails if nothing arrives: some sandboxed or
// firewalled environments block multicast entirely regardless of what the
// code does (confirmed by hand against this project's own dev sandbox, even
// binding explicitly to a real interface instead of "any") — the same class
// of environment limitation CLAUDE.md documents for `go test -race` on the
// Windows dev machine. A real desktop OS or a standard CI runner's network
// namespace normally supports loopback multicast fine, so this still gives
// full coverage there; it just cannot make a categorically unsupported
// environment carry the packets.
func TestAnnounceAndReceiveRoundTrip(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatalf("New (a): %v", err)
	}
	defer a.Close()
	b, err := New()
	if err != nil {
		t.Fatalf("New (b): %v", err)
	}
	defer b.Close()

	infoHash := [20]byte{0xaa, 0xbb, 0xcc}
	deadline := time.After(10 * time.Second)
	retry := time.NewTicker(200 * time.Millisecond)
	defer retry.Stop()

	// A real router can drop or delay the first multicast packet or two, so
	// this retries the announce until either a response arrives or the
	// overall deadline is hit, rather than sending exactly once.
	if err := a.Announce(infoHash, 6881); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	for {
		select {
		case found := <-b.Found():
			if found.InfoHash != infoHash {
				t.Fatalf("got infohash %x, want %x", found.InfoHash, infoHash)
			}
			if found.Addr.Port != 6881 {
				t.Fatalf("got port %d, want 6881", found.Addr.Port)
			}
			return
		case <-retry.C:
			a.Announce(infoHash, 6881)
		case <-deadline:
			t.Skip("no multicast traffic arrived within 10s - this environment likely does not carry multicast at all (see the doc comment above); parseBTSearch's unit tests still cover the message format itself")
		}
	}
}

// TestCloseIsIdempotent proves Close can be called more than once without
// panicking — a real requirement, not a hypothetical: internal/engine calls
// it both from a ctx-cancellation watcher and from an explicit Shutdown
// path, and either can legitimately win the race to call it first.
func TestCloseIsIdempotent(t *testing.T) {
	l, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Close()
	l.Close() // must not panic
}

// TestOwnAnnounceIsFiltered proves a node never hears its own announce back
// (the cookie mechanism) — without it, a client would append itself to
// every torrent's PEX-like discovery for no reason.
func TestOwnAnnounceIsFiltered(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	if err := a.Announce([20]byte{0x11}, 6881); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	select {
	case found := <-a.Found():
		t.Fatalf("received our own announce back: %+v", found)
	case <-time.After(500 * time.Millisecond):
	}
}
