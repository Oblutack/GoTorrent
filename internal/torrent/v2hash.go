package torrent

import (
	"errors"
	"fmt"
	"math/bits"
	"time"

	"github.com/Oblutack/GoTorrent/internal/merkle"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
)

// This file is the torrent-actor half of BEP 52's hash-request/hashes/
// hash-reject exchange — internal/peer's own v2hash.go/v2wire.go are the
// wire format and per-connection send/serve mechanics alone; deciding
// what to serve and what to do with a reply is this package's job, the
// same division every other extension in this codebase (ut_metadata,
// ut_pex, ut_holepunch) already follows.
//
// Deliberately scoped to whole-piece-layer requests only, in both
// directions: Index=0, Length covering the file's entire real piece
// count (rounded up to the next power of two per BEP 52's own length
// rule), BaseLayer set to the piece layer's own distance from the leaf
// layer, ProofLayers=0. This is not the full generality BEP 52's wire
// format allows (a requester may ask for a partial range at any layer,
// with ancestor proof layers making the reply verifiable against a root
// without needing the whole base layer) — but it fully solves this
// project's own real use case, reconstructing a magnet-sourced v2/hybrid
// torrent's missing 'piece layers' entry: verifying the WHOLE layer
// against the file's already-known PiecesRoot (via merkle.FileRoot)
// needs no partial proof at all. A real, precisely-scoped gap, the same
// shape as BEP 19's single-file-only scope or BEP 6's receive-only
// AllowedFast — leaf-layer serving and partial/proof-bearing requests are
// not implemented.

// pieceLayerRequestTimeout mirrors holepunchPendingTimeout's own reasoning
// (holepunch.go), a separate constant since the two features' pending-
// request maps are otherwise independent.
const pieceLayerRequestTimeout = 30 * time.Second

// pieceLayerDistance returns how many tree layers separate the leaf
// layer from the piece layer, for a torrent with the given piece length
// — BEP 52's own "if a piece size of 128KiB is used then 3rd layer up
// from the leaf hashes is used" rule, generalized: log2(piece length /
// BlockSize).
func pieceLayerDistance(pieceLength int64) uint32 {
	blocksPerPiece := pieceLength / merkle.BlockSize
	return uint32(bits.Len(uint(blocksPerPiece)) - 1)
}

// serveHashesSafe answers an incoming 'hash request' — called from the
// requesting connection's own read-loop goroutine (peer.Callbacks.
// ServeHashes's own contract), so it reads only the atomically-published
// mi rather than any actor-owned state, the same "peer-goroutine-safe"
// shape hasPieceSafe/readBlockSafe already establish.
func (t *Torrent) serveHashesSafe(req peer.MsgHashRequestPayload) ([][32]byte, bool) {
	mi := t.mi.Load()
	if mi == nil || mi.MetaVersion != 2 {
		return nil, false
	}

	root := metainfo.Hash256(req.PiecesRoot)
	var fileIndex = -1
	for i, f := range mi.V2Files {
		if f.PiecesRoot == root {
			fileIndex = i
			break
		}
	}
	if fileIndex < 0 {
		return nil, false
	}
	first, last, ok := mi.V2FilePieceRange(fileIndex)
	if !ok {
		return nil, false
	}
	realCount := last - first + 1
	wantBaseLayer := pieceLayerDistance(mi.Info.PieceLength)
	wantLength := uint32(merkle.NextPow2(realCount))
	if req.BaseLayer != wantBaseLayer || req.Index != 0 || req.Length != wantLength || req.ProofLayers != 0 {
		return nil, false // outside this implementation's deliberately narrow scope - see doc comment above
	}

	layerBytes, ok := mi.PieceLayers[root]
	if !ok {
		return nil, false // we don't have it to give either
	}
	if len(layerBytes) != realCount*merkle.DigestSize {
		return nil, false // shouldn't happen (setV2 already validated this at parse time), fail closed
	}

	hashes := make([][32]byte, wantLength)
	for i := 0; i < realCount; i++ {
		copy(hashes[i][:], layerBytes[i*merkle.DigestSize:(i+1)*merkle.DigestSize])
	}
	// Slots beyond the real count stay zero - the same raw-zero-hash
	// padding BEP 52's own tree construction uses at the leaf/piece-layer
	// level (see internal/merkle's own doc comment).
	return hashes, true
}

// RequestPieceLayer asks an already-connected peer (peerAddr, exactly as
// it appears in Peers()) for fileIndex's whole piece layer — used to
// reconstruct a v2/hybrid torrent's 'piece layers' entry when metadata
// arrived without one (the magnet case, always, since BEP 9's own
// ut_metadata exchange only ever delivers the info dict, never the
// separate top-level piece_layers dict — see this file's own package doc
// comment). Nothing in this package decides *when* to call this on its
// own — the same "implements the mechanism correctly, a caller decides
// when" shape RequestHolepunch already established for BEP 55.
func (t *Torrent) RequestPieceLayer(peerAddr string, fileIndex int) error {
	resp := make(chan error, 1)
	select {
	case t.control <- controlMsg{
		kind:               ctrlRequestPieceLayer,
		pieceLayerPeerAddr: peerAddr,
		fileIndex:          fileIndex,
		errReply:           resp,
	}:
	case <-t.done:
		return ErrClosed
	}
	select {
	case err := <-resp:
		return err
	case <-t.done:
		return ErrClosed
	}
}

func (t *Torrent) doRequestPieceLayer(peerAddr string, fileIndex int) error {
	pc, ok := t.peers[peerAddr]
	if !ok {
		return fmt.Errorf("torrent: not connected to peer %s", peerAddr)
	}
	mi := t.mi.Load()
	if mi == nil {
		return errors.New("torrent: no metadata yet")
	}
	if mi.MetaVersion != 2 {
		return errors.New("torrent: not a v2/hybrid torrent")
	}
	if fileIndex < 0 || fileIndex >= len(mi.V2Files) {
		return fmt.Errorf("torrent: file index %d out of range", fileIndex)
	}
	return t.requestPieceLayer(pc, mi, fileIndex)
}

// requestPieceLayer sends pc a whole-layer 'hash request' for fileIndex,
// recording it in pendingPieceLayerRequests so a later reply (or reject,
// or simply nothing before it's swept — see expirePieceLayerRequests) is
// recognized.
func (t *Torrent) requestPieceLayer(pc *peerConn, mi *metainfo.MetaInfo, fileIndex int) error {
	if !pc.client.SupportsV2Hashes() {
		return errors.New("torrent: peer does not support BEP 52 hash exchange")
	}
	f := mi.V2Files[fileIndex]
	first, last, ok := mi.V2FilePieceRange(fileIndex)
	if !ok {
		return errors.New("torrent: file has no pieces to request a layer for")
	}
	realCount := last - first + 1
	if realCount <= 1 {
		return errors.New("torrent: file is a single piece - its PiecesRoot needs no separate layer request")
	}

	req := peer.MsgHashRequestPayload{
		PiecesRoot:  f.PiecesRoot,
		BaseLayer:   pieceLayerDistance(mi.Info.PieceLength),
		Index:       0,
		Length:      uint32(merkle.NextPow2(realCount)),
		ProofLayers: 0,
	}
	t.pendingPieceLayerRequests[f.PiecesRoot] = time.Now()
	return pc.client.SendHashRequest(req)
}

// onHashMessage processes an incoming 'hashes'/'hash reject' reply. An
// unsolicited one (no matching pendingPieceLayerRequests entry) is
// ignored outright — the same anti-abuse discipline BEP 55's own
// pendingHolepunches check already established, here mattering less for
// abuse (a peer can't make us do anything by sending an unsolicited
// reply) than for simply not acting on stale or duplicate data.
//
// A verified reply (its hashes really do combine, via merkle.FileRoot, to
// the file's own already-known PiecesRoot) is published as a genuinely
// new *MetaInfo with PieceLayers extended — an atomic whole-value
// replace, the same publish-a-new-instance discipline SetMetadata already
// uses, never an in-place mutation of the currently-loaded one (which
// readers on other goroutines, like serveHashesSafe above, may be
// reading concurrently).
func (t *Torrent) onHashMessage(pc *peerConn, msg peer.HashMessage) {
	root := metainfo.Hash256(msg.PiecesRoot)
	if _, pending := t.pendingPieceLayerRequests[root]; !pending {
		return
	}
	delete(t.pendingPieceLayerRequests, root)

	if msg.Kind == peer.HashMessageReject {
		return
	}

	mi := t.mi.Load()
	if mi == nil || mi.MetaVersion != 2 {
		return
	}
	fileIndex := -1
	for i, f := range mi.V2Files {
		if f.PiecesRoot == root {
			fileIndex = i
			break
		}
	}
	if fileIndex < 0 {
		return
	}
	first, last, ok := mi.V2FilePieceRange(fileIndex)
	if !ok {
		return
	}
	realCount := last - first + 1
	if len(msg.Hashes) < realCount {
		return
	}

	blocksPerPiece := int(mi.Info.PieceLength / merkle.BlockSize)
	pieceRoots := make([]merkle.Hash, len(msg.Hashes))
	for i, h := range msg.Hashes {
		pieceRoots[i] = merkle.Hash(h)
	}
	if got := merkle.FileRoot(pieceRoots, blocksPerPiece); got != merkle.Hash(root) {
		return // does not verify - a lying or confused peer, discard
	}

	layerBytes := make([]byte, realCount*merkle.DigestSize)
	for i := 0; i < realCount; i++ {
		copy(layerBytes[i*merkle.DigestSize:(i+1)*merkle.DigestSize], msg.Hashes[i][:])
	}

	next := *mi // shallow copy - PieceLayers is the only field this replaces
	next.PieceLayers = make(map[metainfo.Hash256][]byte, len(mi.PieceLayers)+1)
	for k, v := range mi.PieceLayers {
		next.PieceLayers[k] = v
	}
	next.PieceLayers[root] = layerBytes
	t.mi.Store(&next)
}

// expirePieceLayerRequests drops any pendingPieceLayerRequests entry
// older than pendingRequestTimeout, mirroring expireHolepunches — a peer
// that never answers a hash request (no reply, no reject) must not pin
// this map's memory forever, and a request another connected peer could
// still usefully answer should not stay blocked on the one that went
// silent.
func (t *Torrent) expirePieceLayerRequests(now time.Time) {
	for key, sentAt := range t.pendingPieceLayerRequests {
		if now.Sub(sentAt) > pieceLayerRequestTimeout {
			delete(t.pendingPieceLayerRequests, key)
		}
	}
}
