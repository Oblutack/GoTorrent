package metainfo

import "testing"

func TestExportOfAnOrdinarilyLoadedTorrentRoundTrips(t *testing.T) {
	mi := buildTestMetaInfo(t, "http://tracker.example/announce", [][]string{{"http://tracker.example/announce"}})

	raw, err := Export(mi, nil)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	reparsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(Export's output): %v", err)
	}
	if reparsed.InfoHash != mi.InfoHash {
		t.Fatal("Export changed the infohash")
	}
	if reparsed.Announce != mi.Announce {
		t.Fatalf("Announce = %q, want %q", reparsed.Announce, mi.Announce)
	}
}

// TestExportOfAMagnetOnlyMetaInfoAddsTheGivenTrackers proves the case that
// matters most: a magnet's MetaInfo (built via ParseInfo, so it has no
// top-level Announce/AnnounceList at all — only Info/InfoBytes) still
// exports to a real, useful .torrent once given the trackers the magnet
// (or AddTracker) knew about.
func TestExportOfAMagnetOnlyMetaInfoAddsTheGivenTrackers(t *testing.T) {
	full := buildTestMetaInfo(t, "", nil)
	magnetShaped, err := ParseInfo(full.InfoBytes)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if magnetShaped.Announce != "" || len(magnetShaped.AnnounceList) != 0 {
		t.Fatal("test setup: expected a magnet-shaped MetaInfo with no trackers of its own")
	}

	raw, err := Export(magnetShaped, []string{"udp://tr1.example:80", "udp://tr2.example:80"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	reparsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(Export's output): %v", err)
	}
	if reparsed.InfoHash != full.InfoHash {
		t.Fatal("Export changed the infohash")
	}
	urls := reparsed.AnnounceURLs()
	if len(urls) != 2 {
		t.Fatalf("AnnounceURLs() = %v, want the 2 given trackers", urls)
	}
}

func TestExportDeduplicatesExtraTrackers(t *testing.T) {
	mi := buildTestMetaInfo(t, "http://tracker.example/announce", nil)

	raw, err := Export(mi, []string{"http://tracker.example/announce", "http://tracker.example/announce"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	reparsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := reparsed.AnnounceURLs(); len(got) != 1 {
		t.Fatalf("AnnounceURLs() = %v, want exactly one deduplicated entry", got)
	}
}

func TestExportRejectsNilMetaInfo(t *testing.T) {
	if _, err := Export(nil, nil); err == nil {
		t.Fatal("Export(nil, ...) accepted, want an error")
	}
}

// buildTestMetaInfo is a minimal real torrent for export_test.go's own use
// — create_test.go's Build is the natural way to get one without hand
// -rolling bencode here too.
func buildTestMetaInfo(t *testing.T, announce string, announceList [][]string) *MetaInfo {
	t.Helper()
	dir := t.TempDir()
	src := writeTempFile(t, dir, "e.bin", []byte("hello world"))
	_, mi, err := Build(CreateOptions{
		Name:         "e.bin",
		PieceLength:  MinPieceLength,
		Files:        []CreateFile{{SourcePath: src, Length: 11}},
		Announce:     announce,
		AnnounceList: announceList,
	})
	if err != nil {
		t.Fatalf("Build (test fixture): %v", err)
	}
	return mi
}
