package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPiecesHandlerReportsBitfield(t *testing.T) {
	e := newTestEngine(t)
	hash := addTestTorrent(t, e, "piecesme")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}/pieces", PiecesHandler(e))

	rec := routedRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+hash.String()+"/pieces")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got PiecesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// addTestTorrent's fixture (writeTorrentFile: pieceLength*2+100 bytes)
	// is a real 3-piece torrent verified against no real content on disk,
	// so every piece is missing.
	if got.NumPieces != 3 {
		t.Fatalf("NumPieces = %d, want 3", got.NumPieces)
	}
	if got.HaveCount != 0 {
		t.Fatalf("HaveCount = %d, want 0", got.HaveCount)
	}
	if len(got.Bitfield) == 0 {
		t.Fatal("Bitfield is empty for a torrent with known metadata")
	}
}

func TestPiecesHandlerUnknownHashReturns404(t *testing.T) {
	e := newTestEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/torrents/{hash}/pieces", PiecesHandler(e))

	unmanaged := "0000000000000000000000000000000000000000"
	rec := routedRequest(t, mux, http.MethodGet, "/api/v1/torrents/"+unmanaged+"/pieces")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
