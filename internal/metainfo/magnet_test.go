package metainfo

import (
	"encoding/base32"
	"errors"
	"reflect"
	"testing"
)

const sampleHashHex = "0123456789abcdef0123456789abcdef01234567"

func TestParseMagnetHexInfoHash(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + sampleHashHex + "&dn=Some+File&tr=http%3A%2F%2Ftracker.example%2Fannounce"
	m, err := ParseMagnet(uri)
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	want, err := ParseHash(sampleHashHex)
	if err != nil {
		t.Fatalf("ParseHash fixture: %v", err)
	}
	if m.InfoHash != want {
		t.Fatalf("InfoHash = %s, want %s", m.InfoHash, want)
	}
	if m.DisplayName != "Some File" {
		t.Fatalf("DisplayName = %q, want %q", m.DisplayName, "Some File")
	}
	if len(m.Trackers) != 1 || m.Trackers[0] != "http://tracker.example/announce" {
		t.Fatalf("Trackers = %v, want one decoded tracker URL", m.Trackers)
	}
}

func TestParseMagnetBase32InfoHash(t *testing.T) {
	hexHash := sampleHashHex
	hash, err := ParseHash(hexHash)
	if err != nil {
		t.Fatalf("ParseHash fixture: %v", err)
	}

	b32 := hashToBase32(t, hash)
	m, err := ParseMagnet("magnet:?xt=urn:btih:" + b32)
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	if m.InfoHash != hash {
		t.Fatalf("InfoHash = %s, want %s (base32 round trip failed)", m.InfoHash, hash)
	}
}

func TestParseMagnetMultipleTrackers(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + sampleHashHex +
		"&tr=http%3A%2F%2Fa.example%2Fannounce" +
		"&tr=udp%3A%2F%2Fb.example%3A6969"
	m, err := ParseMagnet(uri)
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	want := []string{"http://a.example/announce", "udp://b.example:6969"}
	if !reflect.DeepEqual(m.Trackers, want) {
		t.Fatalf("Trackers = %v, want %v", m.Trackers, want)
	}
}

func TestParseMagnetPeerHintsAndWebSeeds(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + sampleHashHex +
		"&x.pe=203.0.113.5%3A6881" +
		"&ws=https%3A%2F%2Fweb.example%2Ffile.bin"
	m, err := ParseMagnet(uri)
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	if len(m.PeerAddrs) != 1 || m.PeerAddrs[0] != "203.0.113.5:6881" {
		t.Fatalf("PeerAddrs = %v", m.PeerAddrs)
	}
	if len(m.WebSeeds) != 1 || m.WebSeeds[0] != "https://web.example/file.bin" {
		t.Fatalf("WebSeeds = %v", m.WebSeeds)
	}
}

func TestParseMagnetSelectedFiles(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + sampleHashHex + "&so=0,2,4-6"
	m, err := ParseMagnet(uri)
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	want := []int{0, 2, 4, 5, 6}
	if !reflect.DeepEqual(m.SelectedFiles, want) {
		t.Fatalf("SelectedFiles = %v, want %v", m.SelectedFiles, want)
	}
}

func TestParseMagnetHybridV1AndV2(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + sampleHashHex + "&xt=urn:btmh:1220" + sampleHashHex + sampleHashHex[:24]
	m, err := ParseMagnet(uri)
	if err != nil {
		t.Fatalf("ParseMagnet on a hybrid v1+v2 magnet: %v", err)
	}
	want, _ := ParseHash(sampleHashHex)
	if m.InfoHash != want {
		t.Fatalf("InfoHash = %s, want %s", m.InfoHash, want)
	}
	if !m.HasV2 {
		t.Fatal("HasV2 = false, want true for a hybrid magnet")
	}
}

func TestParseMagnetV2OnlyIsRejected(t *testing.T) {
	uri := "magnet:?xt=urn:btmh:1220" + sampleHashHex + sampleHashHex[:24]
	_, err := ParseMagnet(uri)
	if err == nil {
		t.Fatal("ParseMagnet accepted a v2-only magnet, want an error")
	}
}

func TestParseMagnetRejectsNonMagnetURI(t *testing.T) {
	_, err := ParseMagnet("http://example.com/file.torrent")
	if !errors.Is(err, ErrNotAMagnetURI) {
		t.Fatalf("err = %v, want ErrNotAMagnetURI", err)
	}
}

func TestParseMagnetRejectsMissingHash(t *testing.T) {
	_, err := ParseMagnet("magnet:?dn=NoHashHere")
	if err == nil {
		t.Fatal("ParseMagnet accepted a magnet with no xt=urn:btih:, want an error")
	}
}

func TestParseMagnetRejectsBadHashLength(t *testing.T) {
	_, err := ParseMagnet("magnet:?xt=urn:btih:deadbeef")
	if err == nil {
		t.Fatal("ParseMagnet accepted a truncated infohash, want an error")
	}
}

func TestParseMagnetIgnoresUnknownTopics(t *testing.T) {
	uri := "magnet:?xt=urn:sha1:somethingelse&xt=urn:btih:" + sampleHashHex
	m, err := ParseMagnet(uri)
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	want, _ := ParseHash(sampleHashHex)
	if m.InfoHash != want {
		t.Fatalf("InfoHash = %s, want %s", m.InfoHash, want)
	}
}

func TestParseSelectedFilesRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"a", "1-", "-1", "3-1"} {
		if _, err := parseSelectedFiles(bad); err == nil {
			t.Errorf("parseSelectedFiles(%q) accepted malformed input, want an error", bad)
		}
	}
}

func TestParseSelectedFilesSkipsEmptyEntries(t *testing.T) {
	got, err := parseSelectedFiles("1,,2")
	if err != nil {
		t.Fatalf("parseSelectedFiles: %v", err)
	}
	want := []int{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func hashToBase32(t *testing.T, h Hash) string {
	t.Helper()
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:])
}
