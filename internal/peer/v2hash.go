package peer

// HashMessageKind distinguishes a 'hashes' reply from a 'hash reject' —
// both correlate with a 'hash request' we sent, delivered on the same
// channel since a caller waiting for one needs to know about the other
// too (a reject means stop waiting, not keep waiting forever).
type HashMessageKind int

const (
	HashMessageHashes HashMessageKind = iota
	HashMessageReject
)

// HashMessage carries an incoming BEP 52 'hashes' or 'hash reject'
// message — the fields it correlates with (pieces_root/base_layer/index/
// length/proof_layers) always echo the 'hash request' this answers;
// Hashes is only meaningful for HashMessageHashes.
type HashMessage struct {
	Kind        HashMessageKind
	PiecesRoot  [32]byte
	BaseLayer   uint32
	Index       uint32
	Length      uint32
	ProofLayers uint32
	Hashes      [][32]byte
}

// SendHashRequest sends a BEP 52 'hash request' — internal/torrent is
// what decides which file/layer/range to ask for and what to do with the
// reply (arriving later on HashMessages); this is the wire send alone.
func (c *Client) SendHashRequest(req MsgHashRequestPayload) error {
	return c.SendMessage(MsgHashRequest, req.Serialize())
}

// serveHashRequest answers an incoming 'hash request' — called from this
// connection's own read-loop goroutine, the same "peer-goroutine-safe,
// no actor round trip" shape serveRequest already uses for ordinary piece
// requests, via the Callbacks.ServeHashes hook (internal/torrent walks
// whatever merkle data it already has locally). A nil ServeHashes (the
// zero-value Callbacks — no torrent-side hash-serving wired up at all)
// answers every request with a reject, never silence — hash requests
// "MUST be answered with either a 'hashes' or 'hash reject' message," per
// BEP 52's own wording, unlike an ordinary Request, which a peer that
// doesn't understand Fast can legitimately just ignore.
func (c *Client) serveHashRequest(req MsgHashRequestPayload) {
	var hashes [][32]byte
	var ok bool
	if c.serveHashes != nil {
		hashes, ok = c.serveHashes(req)
	}
	if !ok {
		_ = c.SendMessage(MsgHashReject, req.Serialize())
		return
	}
	reply := MsgHashesPayload{
		PiecesRoot: req.PiecesRoot, BaseLayer: req.BaseLayer, Index: req.Index,
		Length: req.Length, ProofLayers: req.ProofLayers, Hashes: hashes,
	}
	_ = c.SendMessage(MsgHashes, reply.Serialize())
}

// handleHashesOrReject delivers an incoming 'hashes'/'hash reject' message
// on HashMessages for the owner (internal/torrent) to correlate with
// whatever 'hash request' it's waiting on — this package's job ends at
// "parse it correctly and hand it off," the same division holepunch.go's
// own handleHolepunchMessage already establishes.
func (c *Client) handleHashesOrReject(kind HashMessageKind, p MsgHashRequestPayload, hashes [][32]byte) {
	msg := HashMessage{
		Kind: kind, PiecesRoot: p.PiecesRoot, BaseLayer: p.BaseLayer,
		Index: p.Index, Length: p.Length, ProofLayers: p.ProofLayers, Hashes: hashes,
	}
	select {
	case c.HashMessages <- msg:
	case <-c.done:
	}
}
