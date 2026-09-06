package torrent

import (
	"crypto/sha1"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/peer"
)

// metadataAssembly tracks one in-progress BEP 9 fetch: every piece is
// requested from a single peer up front (metadata is small — a few KB to a
// few hundred KB, so a handful of unpipelined requests is not worth
// optimizing further) rather than doled out incrementally like regular
// pieces are; the picker's adaptive machinery exists for a very different
// scale of transfer.
type metadataAssembly struct {
	peer      *peerConn
	totalSize int
	numPieces int
	pieces    [][]byte
	have      int
}

// maybeStartMetadataFetch looks for any connected peer that has already told
// us (via its extended handshake) how large the metadata is, and starts
// pulling every piece from the first one found. It is a no-op once metadata
// is known or a fetch is already running.
//
// Scanning t.peers here rather than tracking a separate "peers that offered
// metadata" set works because peer.Client itself remembers its own
// handshake result for the life of the connection — nothing here needs to
// remember it a second time.
func (t *Torrent) maybeStartMetadataFetch() {
	if t.mi.Load() != nil || t.metadataFetch != nil {
		return
	}
	for _, pc := range t.peers {
		size := pc.client.PeerMetadataSize()
		if size <= 0 {
			continue
		}
		numPieces := (size + peer.MetadataPieceSize - 1) / peer.MetadataPieceSize
		t.metadataFetch = &metadataAssembly{
			peer:      pc,
			totalSize: size,
			numPieces: numPieces,
			pieces:    make([][]byte, numPieces),
		}
		for i := 0; i < numPieces; i++ {
			if err := pc.client.SendMetadataRequest(i); err != nil {
				logger.Logf("torrent %s: requesting metadata piece %d from %s: %v\n", t.infoHash, i, pc.addr, err)
			}
		}
		return
	}
}

// abandonMetadataFetch drops the current fetch (if pc is the peer it was
// using) and immediately looks for another already-connected peer to try
// instead, so one bad or vanished peer costs at most one attempt rather than
// stalling until some other event happens to retrigger a fetch.
func (t *Torrent) abandonMetadataFetch(pc *peerConn) {
	if t.metadataFetch == nil || t.metadataFetch.peer != pc {
		return
	}
	t.metadataFetch = nil
	t.maybeStartMetadataFetch()
}

// onMetadataPiece folds one arrived piece into the current fetch, and once
// every piece is in, verifies the assembled info dictionary against the
// infohash before trusting a single byte of it (BEP 9 is explicit that a
// peer can lie about metadata_size or the pieces themselves). A peer that
// fails verification is disconnected outright — not just abandoned — since
// serving bad metadata is either a broken implementation or an active
// attack, and either way its bitfield/Have traffic isn't trustworthy either.
func (t *Torrent) onMetadataPiece(pc *peerConn, mp peer.MetadataPiece) {
	mf := t.metadataFetch
	if mf == nil || mf.peer != pc {
		return // stale: from a peer we're no longer fetching from
	}
	if mp.Piece < 0 || mp.Piece >= mf.numPieces || mf.pieces[mp.Piece] != nil {
		return
	}

	mf.pieces[mp.Piece] = mp.Data
	mf.have++
	if mf.have < mf.numPieces {
		return
	}

	assembled := make([]byte, 0, mf.totalSize)
	for _, p := range mf.pieces {
		assembled = append(assembled, p...)
	}
	t.metadataFetch = nil

	if sum := sha1.Sum(assembled); metainfo.Hash(sum) != t.infoHash {
		logger.Warning.Printf("torrent %s: metadata from %s failed infohash verification, dropping peer\n", t.infoHash, pc.addr)
		pc.client.Close()
		t.maybeStartMetadataFetch()
		return
	}

	mi, err := metainfo.ParseInfo(assembled)
	if err != nil {
		// Extremely unlikely once the hash has already matched (sha1 is
		// collision-resistant, so matching bytes are the real info dict),
		// but a defensive fallback costs nothing.
		logger.Warning.Printf("torrent %s: metadata from %s hashed correctly but did not parse: %v\n", t.infoHash, pc.addr, err)
		pc.client.Close()
		t.maybeStartMetadataFetch()
		return
	}

	if err := t.doSetMetadata(mi); err != nil {
		logger.Error.Printf("torrent %s: applying fetched metadata: %v\n", t.infoHash, err)
	}
}
