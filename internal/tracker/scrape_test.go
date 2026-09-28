package tracker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/bencode"
)

// TestScrapeURLConversion pins every worked example from the "Tracker
// 'scrape' Convention" (the unofficial but universally implemented rule
// ScrapeURL follows — see its own doc comment), including the ones that
// must NOT convert.
func TestScrapeURLConversion(t *testing.T) {
	cases := []struct {
		announce string
		want     string
		ok       bool
	}{
		{"http://example.com/announce", "http://example.com/scrape", true},
		{"http://example.com/x/announce", "http://example.com/x/scrape", true},
		{"http://example.com/announce.php", "http://example.com/scrape.php", true},
		{"http://example.com/a", "", false},
		{"http://example.com/announce?x2%0644", "http://example.com/scrape?x2%0644", true},
		{"http://example.com/announce?x=2/4", "", false},
		{"http://example.com/x%064announce", "", false},
	}
	for _, c := range cases {
		got, ok := ScrapeURL(c.announce)
		if ok != c.ok || got != c.want {
			t.Errorf("ScrapeURL(%q) = (%q, %v), want (%q, %v)", c.announce, got, ok, c.want, c.ok)
		}
	}
}

// TestHTTPScrapeMatchesTheBEP48PublishedExample builds a "files" response
// carrying the exact numeric values BEP 48's own worked example uses for
// two torrents (complete/downloaded/incomplete of 11/13772/19 and
// 21/206/20) via this package's own marshaler — not the literal example
// bytes the spec page gives, since a web fetch of that page could not be
// reliably reproduced byte-for-byte (a re-fetch garbled the dictionary's
// closing braces); the values and the two-torrents-in-one-response shape
// are what came from the spec, and matter more here than reproducing its
// raw bytes verbatim.
func TestHTTPScrapeMatchesTheBEP48PublishedExample(t *testing.T) {
	var hx, hy [20]byte
	copy(hx[:], "xxxxxxxxxxxxxxxxxxxx")
	copy(hy[:], "yyyyyyyyyyyyyyyyyyyy")

	var gotPath string
	var gotHashes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHashes = r.URL.Query()["info_hash"]
		data, err := bencode.Marshal(scrapeResponseWire{
			Files: map[string]scrapeFileWire{
				string(hx[:]): {Complete: 11, Downloaded: 13772, Incomplete: 19},
				string(hy[:]): {Complete: 21, Downloaded: 206, Incomplete: 20},
			},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(nil)
	results, err := c.Scrape(context.Background(), srv.URL+"/announce", [][20]byte{hx, hy})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}

	if gotPath != "/scrape" {
		t.Fatalf("tracker received path %q, want /scrape (ScrapeURL should have converted /announce)", gotPath)
	}
	if len(gotHashes) != 2 {
		t.Fatalf("tracker received %d info_hash params, want 2", len(gotHashes))
	}

	byHash := make(map[[20]byte]ScrapeResult, len(results))
	for _, r := range results {
		byHash[r.InfoHash] = r
	}
	wantX := ScrapeResult{InfoHash: hx, Complete: 11, Downloaded: 13772, Incomplete: 19}
	wantY := ScrapeResult{InfoHash: hy, Complete: 21, Downloaded: 206, Incomplete: 20}
	if byHash[hx] != wantX {
		t.Fatalf("x result = %+v, want %+v", byHash[hx], wantX)
	}
	if byHash[hy] != wantY {
		t.Fatalf("y result = %+v, want %+v", byHash[hy], wantY)
	}
}

// TestHTTPScrapeSkipsAnInfoHashTheTrackerHasNoDataFor proves the HTTP
// half's real asymmetry against UDP's: a "files" dict that only mentions
// one of the two requested hashes yields exactly one result, not an error
// and not a synthesized zero entry for the missing one.
func TestHTTPScrapeSkipsAnInfoHashTheTrackerHasNoDataFor(t *testing.T) {
	var known [20]byte
	copy(known[:], "xxxxxxxxxxxxxxxxxxxx")
	var unknown [20]byte
	copy(unknown[:], "zzzzzzzzzzzzzzzzzzzz")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := bencode.Marshal(scrapeResponseWire{
			Files: map[string]scrapeFileWire{
				string(known[:]): {Complete: 3, Downloaded: 4, Incomplete: 5},
			},
		})
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(nil)
	results, err := c.Scrape(context.Background(), srv.URL+"/announce", [][20]byte{known, unknown})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(results) != 1 || results[0].InfoHash != known {
		t.Fatalf("results = %+v, want exactly one result for the known hash", results)
	}
}

func TestHTTPScrapeReturnsErrTrackerFailureOnFailureReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := bencode.Marshal(scrapeResponseWire{FailureReason: "unregistered torrent"})
		w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(nil)
	_, err := c.Scrape(context.Background(), srv.URL+"/announce", [][20]byte{{1}})
	var trackerErr *ErrTrackerFailure
	if !errors.As(err, &trackerErr) || trackerErr.Reason != "unregistered torrent" {
		t.Fatalf("Scrape error = %v, want an ErrTrackerFailure with reason %q", err, "unregistered torrent")
	}
}

// TestHTTPScrapeReturnsErrScrapeNotSupportedForANonAnnounceURL confirms
// Scrape refuses before ever making a network call — the target host is
// unresolvable, so a network attempt would fail differently (and slowly).
func TestHTTPScrapeReturnsErrScrapeNotSupportedForANonAnnounceURL(t *testing.T) {
	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.Scrape(ctx, "http://tracker.invalid.example/x/y", [][20]byte{{1}})
	if !errors.Is(err, ErrScrapeNotSupported) {
		t.Fatalf("Scrape error = %v, want ErrScrapeNotSupported", err)
	}
}

func TestScrapeRejectsEmptyInfoHashList(t *testing.T) {
	c := NewClient(nil)
	if _, err := c.Scrape(context.Background(), "http://example.com/announce", nil); err == nil {
		t.Fatal("Scrape accepted an empty info hash list")
	}
}

// TestParseUDPScrapeResponseDecodesCorrectly pins BEP 15's scrape response
// byte layout (action, transaction_id, then one seeders/completed/leechers
// triple per torrent) against hand-computed bytes, the same discipline
// internal/peer's holepunch wire test uses — not just a round trip through
// this package's own encoder.
func TestParseUDPScrapeResponseDecodesCorrectly(t *testing.T) {
	h1 := [20]byte{1}
	h2 := [20]byte{2}
	body := make([]byte, 8+12*2)
	binary.BigEndian.PutUint32(body[0:4], udpActionScrape)
	binary.BigEndian.PutUint32(body[4:8], 0xDEADBEEF)
	// torrent 1: seeders=5, completed=100, leechers=3
	binary.BigEndian.PutUint32(body[8:12], 5)
	binary.BigEndian.PutUint32(body[12:16], 100)
	binary.BigEndian.PutUint32(body[16:20], 3)
	// torrent 2: seeders=0, completed=9999, leechers=1
	binary.BigEndian.PutUint32(body[20:24], 0)
	binary.BigEndian.PutUint32(body[24:28], 9999)
	binary.BigEndian.PutUint32(body[28:32], 1)

	got, err := parseUDPScrapeResponse(body, [][20]byte{h1, h2})
	if err != nil {
		t.Fatalf("parseUDPScrapeResponse: %v", err)
	}
	want := []ScrapeResult{
		{InfoHash: h1, Complete: 5, Downloaded: 100, Incomplete: 3},
		{InfoHash: h2, Complete: 0, Downloaded: 9999, Incomplete: 1},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parseUDPScrapeResponse = %+v, want %+v", got, want)
	}
}

func TestParseUDPScrapeResponseRejectsWrongAction(t *testing.T) {
	body := make([]byte, 8+12)
	binary.BigEndian.PutUint32(body[0:4], udpActionAnnounce) // wrong action
	if _, err := parseUDPScrapeResponse(body, [][20]byte{{1}}); err == nil {
		t.Fatal("parseUDPScrapeResponse accepted a response with the wrong action code")
	}
}

func TestParseUDPScrapeResponseRejectsTooShort(t *testing.T) {
	body := make([]byte, 8+12) // only enough for one torrent
	binary.BigEndian.PutUint32(body[0:4], udpActionScrape)
	if _, err := parseUDPScrapeResponse(body, [][20]byte{{1}, {2}}); err == nil {
		t.Fatal("parseUDPScrapeResponse accepted a response too short for the number of torrents requested")
	}
}

// TestUDPScrapeRoundTrip drives a real UDP round trip (connect, then
// scrape) against a fixture that answers scrape requests from a small
// per-infohash stats table — proving scrapeUDP's request encoding and
// parseUDPScrapeResponse's decoding agree with each other over a real
// socket, not just in isolation.
func TestUDPScrapeRoundTrip(t *testing.T) {
	h1 := [20]byte{1, 2, 3}
	h2 := [20]byte{4, 5, 6}
	fake := newFakeUDPScrapeTracker(t, map[[20]byte][3]uint32{
		h1: {7, 42, 2}, // seeders, completed, leechers
		h2: {0, 0, 0},  // known to the tracker, but nobody's ever touched it
	})

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := c.Scrape(ctx, "udp://"+fake.addr()+"/announce", [][20]byte{h1, h2})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	want := []ScrapeResult{
		{InfoHash: h1, Complete: 7, Downloaded: 42, Incomplete: 2},
		{InfoHash: h2, Complete: 0, Downloaded: 0, Incomplete: 0},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Scrape = %+v, want %+v", got, want)
	}
}

// TestUDPScrapeRequestWireFormatMatchesSpecByteLayout captures the raw
// scrape request packet a real Scrape call puts on the wire (after the
// preceding connect handshake every UDP tracker interaction needs) and
// checks it against BEP 15's exact layout, the same "don't just trust a
// round trip" discipline the HTTP and parse-side tests above already
// apply.
func TestUDPScrapeRequestWireFormatMatchesSpecByteLayout(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer conn.Close()

	const fakeConnID = 0x1122334455667788
	captured := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, raddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 16 {
				continue
			}
			action := binary.BigEndian.Uint32(buf[8:12])
			txID := binary.BigEndian.Uint32(buf[12:16])
			if action == udpActionConnect {
				resp := make([]byte, 16)
				binary.BigEndian.PutUint32(resp[0:4], udpActionConnect)
				binary.BigEndian.PutUint32(resp[4:8], txID)
				binary.BigEndian.PutUint64(resp[8:16], fakeConnID)
				conn.WriteToUDP(resp, raddr)
				continue
			}
			out := make([]byte, n)
			copy(out, buf[:n])
			captured <- out
			return
		}
	}()

	c := NewClient(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	h1 := [20]byte{0xAA}
	h2 := [20]byte{0xBB}
	// The tracker never answers the scrape itself, so this call times out —
	// only the captured request bytes matter here.
	_, _ = c.Scrape(ctx, "udp://"+conn.LocalAddr().String()+"/announce", [][20]byte{h1, h2})

	select {
	case raw := <-captured:
		if len(raw) != 16+20*2 {
			t.Fatalf("scrape request is %d bytes, want %d (16 header + 2*20 info_hash)", len(raw), 16+40)
		}
		if got := binary.BigEndian.Uint64(raw[0:8]); got != fakeConnID {
			t.Fatalf("connection_id = %#x, want %#x", got, uint64(fakeConnID))
		}
		if action := binary.BigEndian.Uint32(raw[8:12]); action != udpActionScrape {
			t.Fatalf("action = %d, want %d (scrape)", action, udpActionScrape)
		}
		if !bytes.Equal(raw[16:36], h1[:]) {
			t.Fatalf("first info_hash = %x, want %x", raw[16:36], h1)
		}
		if !bytes.Equal(raw[36:56], h2[:]) {
			t.Fatalf("second info_hash = %x, want %x", raw[36:56], h2)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scrape request never arrived at the fake tracker")
	}
}

// fakeUDPScrapeTracker plays a connect + scrape round trip: unlike
// fakeUDPTracker (udp_test.go, announce-only), it answers per-infohash
// stats from a fixed lookup table, falling back to all-zero counts for a
// requested hash it wasn't told about — mirroring a real tracker that
// knows about a torrent but has never seen a peer for it.
type fakeUDPScrapeTracker struct {
	conn  *net.UDPConn
	stats map[[20]byte][3]uint32
}

func newFakeUDPScrapeTracker(t *testing.T, stats map[[20]byte][3]uint32) *fakeUDPScrapeTracker {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	f := &fakeUDPScrapeTracker{conn: conn, stats: stats}
	t.Cleanup(func() { conn.Close() })
	go f.serve(t)
	return f
}

func (f *fakeUDPScrapeTracker) addr() string { return f.conn.LocalAddr().String() }

func (f *fakeUDPScrapeTracker) serve(t *testing.T) {
	buf := make([]byte, 2048)
	const fakeConnID = 0x1122334455667788
	for {
		n, raddr, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 16 {
			continue
		}
		action := binary.BigEndian.Uint32(buf[8:12])
		txID := binary.BigEndian.Uint32(buf[12:16])

		switch action {
		case udpActionConnect:
			resp := make([]byte, 16)
			binary.BigEndian.PutUint32(resp[0:4], udpActionConnect)
			binary.BigEndian.PutUint32(resp[4:8], txID)
			binary.BigEndian.PutUint64(resp[8:16], fakeConnID)
			f.conn.WriteToUDP(resp, raddr)

		case udpActionScrape:
			if binary.BigEndian.Uint64(buf[0:8]) != fakeConnID {
				t.Errorf("fake scrape tracker: scrape used the wrong connection id")
				continue
			}
			hashBytes := buf[16:n]
			count := len(hashBytes) / 20
			resp := make([]byte, 8+12*count)
			binary.BigEndian.PutUint32(resp[0:4], udpActionScrape)
			binary.BigEndian.PutUint32(resp[4:8], txID)
			for i := 0; i < count; i++ {
				var h [20]byte
				copy(h[:], hashBytes[i*20:(i+1)*20])
				s := f.stats[h] // zero value if unknown, matching a real tracker's "nothing to report"
				off := 8 + 12*i
				binary.BigEndian.PutUint32(resp[off:off+4], s[0])
				binary.BigEndian.PutUint32(resp[off+4:off+8], s[1])
				binary.BigEndian.PutUint32(resp[off+8:off+12], s[2])
			}
			f.conn.WriteToUDP(resp, raddr)
		}
	}
}
