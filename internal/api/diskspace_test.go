package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestDiskSpaceHandlerReportsRealFreeSpace proves the route really queries
// the given directory, not a fixed/fleet-default one - a real temp
// directory on this test machine's own filesystem, not a fake.
func TestDiskSpaceHandlerReportsRealFreeSpace(t *testing.T) {
	dir := t.TempDir()
	rec := doRequest(t, DiskSpaceHandler(), http.MethodGet, "/api/v1/diskspace?path="+dir)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var got DiskSpaceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Path != dir {
		t.Fatalf("Path = %q, want %q", got.Path, dir)
	}
	if got.FreeBytes <= 0 {
		t.Fatalf("FreeBytes = %d, want a real positive figure for a real temp directory", got.FreeBytes)
	}
}

func TestDiskSpaceHandlerRejectsMissingPath(t *testing.T) {
	rec := doRequest(t, DiskSpaceHandler(), http.MethodGet, "/api/v1/diskspace")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a request with no \"path\", body=%s", rec.Code, rec.Body.String())
	}
}

// TestDiskSpaceHandlerRejectsNonexistentPath proves a bad path is a clean
// 400, not a 500 or a panic - the same "let the underlying OS error
// surface as a normal 400" treatment CreateTorrentHandler already gives
// metainfo.CollectFiles' own errors.
func TestDiskSpaceHandlerRejectsNonexistentPath(t *testing.T) {
	rec := doRequest(t, DiskSpaceHandler(), http.MethodGet, "/api/v1/diskspace?path=/definitely/does/not/exist/anywhere")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a nonexistent path, body=%s", rec.Code, rec.Body.String())
	}
}
