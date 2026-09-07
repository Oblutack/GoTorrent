package peer

import (
	"github.com/Oblutack/GoTorrent/internal/bitfield"
)

// fastReservedByte/fastReservedBit mark support for the Fast extension
// (BEP 6) in the handshake's reserved field: the last of the 8 reserved
// bytes, bit 0x04.
const (
	fastReservedByte = 7
	fastReservedBit  = 0x04
)

// SupportsFast reports whether the reserved bits in a handshake advertise
// Fast extension (BEP 6) support.
func (h *Handshake) SupportsFast() bool {
	return h.Reserved[fastReservedByte]&fastReservedBit != 0
}

// sendInitialState sends this connection's one-time initial piece-state
// message, called once from Run right after the handshake — the thing this
// client never used to send at all before BEP 6 gave it a cheap way to.
// Exactly one of HaveAll, HaveNone, or a plain Bitfield goes out, chosen
// from what hasPiece reports:
//   - nothing yet known (NumPieces == 0, the magnet-fetching path, or no
//     HasPiece callback at all): HaveNone if the peer supports Fast, nothing
//     otherwise — BEP 3 already tolerates skipping Bitfield when there is
//     nothing to report.
//   - everything: HaveAll if Fast is supported, else a full Bitfield (BEP 6
//     exists specifically so a seed doesn't have to build and send that
//     bitfield at all).
//   - some pieces: a plain Bitfield either way — BEP 6 only replaces the
//     two extreme cases, not the general one.
func (c *Client) sendInitialState() error {
	info := c.info()
	if info.NumPieces == 0 || c.hasPiece == nil {
		if c.peerSupportsFast {
			return c.SendMessage(MsgHaveNone, nil)
		}
		return nil
	}

	have := bitfield.New(info.NumPieces)
	for i := 0; i < info.NumPieces; i++ {
		if c.hasPiece(uint32(i)) {
			have.Set(i)
		}
	}

	switch have.Count() {
	case info.NumPieces:
		if c.peerSupportsFast {
			return c.SendMessage(MsgHaveAll, nil)
		}
		return c.SendMessage(MsgBitfield, have.Bytes())
	case 0:
		if c.peerSupportsFast {
			return c.SendMessage(MsgHaveNone, nil)
		}
		return nil
	default:
		return c.SendMessage(MsgBitfield, have.Bytes())
	}
}

// applyHaveAll handles an incoming HaveAll: same caching problem as an
// early Bitfield (see pendingBitfield/pendingHaveAll's field comments) when
// NumPieces isn't known yet, otherwise sets every bit immediately.
func (c *Client) applyHaveAll() {
	info := c.info()
	c.bitfieldMu.Lock()
	if info.NumPieces == 0 {
		c.pendingHaveAll = true
		c.pendingBitfield = nil
	} else {
		c.bitfield = bitfield.Full(info.NumPieces)
	}
	c.bitfieldMu.Unlock()
	c.notify(Event{Kind: EventBitfield})
}

// markAllowedFast records that the peer has told us (BEP 6's AllowedFast)
// we may request this piece even while choked.
func (c *Client) markAllowedFast(index uint32) {
	c.allowedFastMu.Lock()
	if c.allowedFast == nil {
		c.allowedFast = make(map[uint32]bool)
	}
	c.allowedFast[index] = true
	c.allowedFastMu.Unlock()
}

// IsAllowedFast reports whether the peer has granted this piece via BEP 6's
// AllowedFast — the owner's picking logic consults this to keep requesting
// from an otherwise-choked peer instead of treating choke as an absolute
// stop, which is the whole point of the Fast extension for a downloading
// client: a faster, less idle cold start with a new peer.
//
// Sending our own AllowedFast grants to peers (the seeding-side half of
// this extension) is not implemented — choosing which pieces to offer for
// that is a policy decision with no correctness stakes, unlike honoring a
// grant we're given, so it is left as a deliberate gap rather than guessed
// at without a real heuristic behind it.
func (c *Client) IsAllowedFast(index uint32) bool {
	c.allowedFastMu.RLock()
	defer c.allowedFastMu.RUnlock()
	return c.allowedFast[index]
}
