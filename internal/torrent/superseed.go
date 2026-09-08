package torrent

import "github.com/Oblutack/GoTorrent/internal/logger"

// superSeedState is BEP 16's per-torrent state, actor-owned like pick/peers/
// dialing — touched only from run(). Created in openMetadata when
// Config.SuperSeeding is set, and cleared (set back to nil) the moment every
// piece has been released to the swarm — see checkSuperSeedGraduation —
// after which this torrent behaves like an ordinary seed for good.
type superSeedState struct {
	numPieces int
	// released tracks which pieces have been assigned to at least one peer
	// who then showed (via its own Have/Bitfield) that it obtained it —
	// releasedN is its count, checked against numPieces for graduation.
	released  []bool
	releasedN int
	// nextIndex round-robins across every piece as new assignments are
	// handed out, so the first copies of a piece spread across the swarm
	// instead of clustering on whichever piece happened to be picked first.
	nextIndex int
	// assigned is addr -> the one piece index we've most recently told that
	// peer we have. A peer's entry only ever advances forward (see
	// maybeSuperSeedAdvance); removePeer deletes it on disconnect.
	assigned map[string]int
}

func newSuperSeedState(numPieces int) *superSeedState {
	return &superSeedState{
		numPieces: numPieces,
		released:  make([]bool, numPieces),
		assigned:  make(map[string]int),
	}
}

// superSeeding reports whether new connections should currently be handed a
// single assigned piece instead of the normal HaveAll — only true once this
// torrent has actually reached Seeding (a torrent configured for
// super-seeding that is still downloading behaves normally until then) and
// before graduation has cleared t.superSeed entirely.
func (t *Torrent) superSeeding() bool {
	return t.superSeed != nil && t.State() == StateSeeding
}

// superSeedAssign hands addr the next piece to advertise as our only one.
// Called from dial/acceptIncoming, on the actor goroutine, before the
// connection's peer.Callbacks are built — the assignment has to be baked
// into the InitialHaves closure at construction time, since peer.Client.Run
// calls sendInitialState immediately after the handshake, racing this
// torrent's own asynchronous eventPeerConnected handling if the decision
// were made any later.
func (t *Torrent) superSeedAssign(addr string) (int, bool) {
	if !t.superSeeding() {
		return 0, false
	}
	ss := t.superSeed
	if ss.numPieces == 0 {
		return 0, false
	}
	piece := ss.nextIndex
	ss.nextIndex = (ss.nextIndex + 1) % ss.numPieces
	ss.assigned[addr] = piece
	return piece, true
}

// maybeSuperSeedAdvance checks whether pc has just shown it obtained the one
// piece it was assigned — the "it propagated" signal that earns it a new
// piece to spread next, rather than ever being offered everything at once.
func (t *Torrent) maybeSuperSeedAdvance(pc *peerConn, piece int) {
	ss := t.superSeed
	if ss == nil {
		return
	}
	assigned, ok := ss.assigned[pc.addr]
	if !ok || assigned != piece {
		return
	}
	if !ss.released[piece] {
		ss.released[piece] = true
		ss.releasedN++
	}
	if t.checkSuperSeedGraduation() {
		return
	}
	next := ss.nextIndex
	ss.nextIndex = (ss.nextIndex + 1) % ss.numPieces
	ss.assigned[pc.addr] = next
	if err := pc.client.SendHave(uint32(next)); err != nil {
		logger.Logf("torrent %s: super-seed Have to %s: %v\n", t.infoHash, pc.addr, err)
	}
}

// checkSuperSeedGraduation ends super-seeding once every piece has been
// released to the swarm at least once: every already-connected peer is
// swept with a Have for every piece (redundant ones are harmless — BEP 3
// tolerates re-announcing a piece already known), matching what an ordinary
// seed would have told them from the start, and t.superSeed is cleared so
// any connection from here on gets the normal HaveAll-based initial state.
// Returns whether graduation happened.
func (t *Torrent) checkSuperSeedGraduation() bool {
	ss := t.superSeed
	if ss == nil || ss.releasedN < ss.numPieces {
		return false
	}
	for _, pc := range t.peers {
		for i := 0; i < ss.numPieces; i++ {
			if err := pc.client.SendHave(uint32(i)); err != nil {
				logger.Logf("torrent %s: super-seed graduation Have to %s: %v\n", t.infoHash, pc.addr, err)
			}
		}
	}
	t.superSeed = nil
	return true
}
