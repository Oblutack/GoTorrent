package api

import (
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/engine"
)

// PiecesResponse is GET /api/v1/torrents/{hash}/pieces's body. Bitfield is
// the raw packed bytes (BEP 3's own bit-per-piece, most-significant-bit
// first, layout) - encoding/json base64-encodes a []byte field
// automatically, which is far more compact over the wire than a JSON array
// of NumPieces booleans for any torrent with a real piece count.
type PiecesResponse struct {
	NumPieces int    `json:"numPieces"`
	HaveCount int    `json:"haveCount"`
	Bitfield  []byte `json:"bitfield"`
}

// PiecesHandler serves GET /api/v1/torrents/{hash}/pieces.
func PiecesHandler(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash, ok := parseHashParam(w, r)
		if !ok {
			return
		}
		tr, ok := e.Get(hash)
		if !ok {
			writeError(w, http.StatusNotFound, "torrent not managed by this engine")
			return
		}

		bf := tr.HaveBitfield()
		resp := PiecesResponse{Bitfield: []byte{}}
		if bf != nil {
			resp.NumPieces = bf.Len()
			resp.HaveCount = bf.Count()
			resp.Bitfield = bf.Bytes()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
