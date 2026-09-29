package utp

import (
	"context"
	"io"
	"time"
)

func nowMicro() uint32 {
	return uint32(time.Now().UnixMicro())
}

// run is the actor goroutine: the only place any of Conn's "actor state"
// fields are touched, the same "single owner goroutine, external callers
// only touch channels" shape internal/torrent.Torrent's own run loop
// already establishes.
func (c *Conn) run() {
	defer c.teardown()

	if c.initiator {
		if err := c.performHandshake(); err != nil {
			c.connectErr = err
			close(c.connectedCh)
			c.setCloseErrOnce(err)
			return
		}
		close(c.connectedCh)
	} else {
		c.state = stateConnected
		close(c.connectedCh)
		// The initiator is blocked waiting for exactly this: some real
		// reply to its SYN. Nothing else would ever prompt us to send
		// one on our own otherwise.
		c.sendState()
	}

	c.rtoTimer = time.NewTimer(c.rto.current())
	defer c.rtoTimer.Stop()

	lingerTimer := (*time.Timer)(nil)
	var lingerCh <-chan time.Time
	// closeSignal is nil'd out after firing once — a closed channel is
	// always immediately ready, so leaving it in the select forever would
	// have it win disproportionately often (starving recvCh/rtoTimer)
	// for the whole linger period instead of firing exactly once.
	closeSignal := c.closeCh

	for {
		var writeCh chan []byte
		if c.state == stateConnected {
			writeCh = c.writeCh // only accept new writes while fully connected
		}
		select {
		case pkt := <-c.recvCh:
			c.handlePacket(pkt)
			if c.state == stateClosed && lingerTimer == nil {
				lingerTimer = time.NewTimer(closeLinger)
				lingerCh = lingerTimer.C
			}
		case data := <-writeCh:
			c.pendingWrite = append(c.pendingWrite, data...)
			c.trySend()
		case <-c.rtoTimer.C:
			c.handleRTO()
		case <-closeSignal:
			closeSignal = nil
			if c.state == stateConnected {
				c.sendFin()
			}
			if lingerTimer == nil {
				lingerTimer = time.NewTimer(closeLinger)
				lingerCh = lingerTimer.C
			}
		case <-lingerCh:
			return
		}
	}
}

func (c *Conn) teardown() {
	c.setCloseErrOnce(ErrConnClosed)
	c.sock.removeConn(c)
	// Wake any blocked Read/Write not already covered by closeCh (e.g. the
	// connecting-phase failure path, which returns before closeCh is ever
	// closed by Close() itself).
	c.closeOnce.Do(func() { close(c.closeCh) })
	c.closeReadSide()
}

// closeReadSide closes readCh exactly once. Called the moment we know no
// more inbound data will ever arrive — either right away, from a clean
// FIN fully processed in order (so a peer that closes cleanly gets Read
// returning io.EOF immediately, not after riding out the full
// closeLinger delay run()'s own exit is otherwise bound by), or as a
// fallback from teardown for every less clean ending (RESET, our own
// Close(), a lost connection that never gets a reply at all).
func (c *Conn) closeReadSide() {
	if c.readChClosed {
		return
	}
	c.readChClosed = true
	close(c.readCh)
}

// performHandshake is the initiator's side of connection setup: send
// ST_SYN, wait for any real reply (or ST_RESET, or a bounded number of
// timeouts), matching TCP's own SYN-retransmission shape.
func (c *Conn) performHandshake() error {
	for attempt := 0; attempt < SynRetries; attempt++ {
		c.sendRaw(&Packet{
			Type:          STSyn,
			ConnID:        c.recvID,
			SeqNr:         c.seqNr,
			AckNr:         0,
			WndSize:       advertisedWindow,
			Timestamp:     nowMicro(),
			TimestampDiff: c.cachedDiff,
		})
		timer := time.NewTimer(c.rto.current())
		select {
		case in := <-c.recvCh:
			timer.Stop()
			if in.Type == STReset {
				return ErrConnRefused
			}
			c.processTimestamp(in)
			c.ackNr = in.SeqNr - 1
			c.haveAck = true
			c.state = stateConnected
			c.consumeSeq() // the SYN occupied c.seqNr; future sends use the next one
			c.handlePacket(in)
			return nil
		case <-timer.C:
			c.rto.timeout()
			continue
		}
	}
	return ErrConnTimeout
}

// handshakeAccept builds the acceptor's own half of the handshake,
// called by Socket.dispatch before the new *Conn's run() goroutine even
// starts — the SYN's own seq_nr is treated as already "received" (there's
// nothing before it), establishing this side's ackNr baseline directly.
func (c *Conn) handshakeAccept(syn *Packet) {
	c.ackNr = syn.SeqNr
	c.haveAck = true
	c.processTimestamp(syn)
}

func (c *Conn) consumeSeq() { c.seqNr++ }

// processTimestamp caches the one-way-delay value this side must echo
// back in every future outgoing packet, per the mechanism doc.go
// describes — computed fresh on every received packet, overwriting
// whatever was cached before.
func (c *Conn) processTimestamp(pkt *Packet) {
	c.cachedDiff = nowMicro() - pkt.Timestamp
}

// handlePacket dispatches one already-received packet by type. Called
// both from run()'s main loop and once, directly, from performHandshake
// for the very first reply.
func (c *Conn) handlePacket(pkt *Packet) {
	c.processTimestamp(pkt)
	c.handleAck(pkt) // every packet, regardless of type, carries the peer's own ack_nr/SACK of OUR data

	switch pkt.Type {
	case STSyn:
		// A duplicate/retransmitted SYN for an already-established
		// connection - just re-send our own current state as an ack.
		c.sendState()
	case STData:
		c.handleData(pkt)
	case STState:
		// Nothing further: handleAck above already processed it.
	case STFin:
		c.peerFin = true
		c.peerFinSeq = pkt.SeqNr
		c.handleData(pkt) // a FIN's own seq_nr still needs normal in-order accounting
		if c.state == stateConnected {
			c.sendFin()
		}
	case STReset:
		c.setCloseErrOnce(ErrConnRefused)
		c.state = stateClosed
		c.closeReadSide()
	}

	// The FIN itself may have arrived out of order and only just been
	// drained from the reorder buffer by this (possibly unrelated, later)
	// packet's own handleData call - checked unconditionally here, not
	// only in the STFin case above, for exactly that reason.
	if c.peerFin && seqLessEq(c.peerFinSeq, c.ackNr) {
		c.setCloseErrOnce(io.EOF)
		c.closeReadSide()
	}
	if c.haveFinSeq && c.peerFin && c.state != stateClosed {
		c.state = stateClosed
	}

	c.trySend()
}

// handleData processes one packet that carries sequenced content (DATA or
// FIN) on the receive side: in-order delivery, out-of-order buffering
// bounded by maxReorderPackets, and draining the reorder buffer once a
// gap is filled.
func (c *Conn) handleData(pkt *Packet) {
	if !c.haveAck {
		c.ackNr = pkt.SeqNr - 1
		c.haveAck = true
	}

	switch {
	case seqLessEq(pkt.SeqNr, c.ackNr):
		// Already delivered (a retransmitted duplicate) - just re-ack below.
	case pkt.SeqNr == c.ackNr+1:
		c.deliver(pkt)
		c.ackNr = pkt.SeqNr
		// Drain any now-contiguous packets already sitting in the
		// reorder buffer.
		for {
			next, ok := c.reorder[c.ackNr+1]
			if !ok {
				break
			}
			delete(c.reorder, c.ackNr+1)
			c.deliver(next)
			c.ackNr = next.SeqNr
		}
	default:
		if len(c.reorder) < maxReorderPackets {
			c.reorder[pkt.SeqNr] = pkt
		}
	}

	c.sendState()
}

// deliver hands one packet's payload to the reader, dropping it (rather
// than blocking the whole actor forever) only if the reader has stopped
// consuming entirely and the channel's own buffer is full and the
// connection is being torn down — ordinary backpressure is the channel's
// own buffer plus the sender's own cwnd-limited send rate.
func (c *Conn) deliver(pkt *Packet) {
	if len(pkt.Payload) == 0 {
		return
	}
	select {
	case c.readCh <- pkt.Payload:
	case <-c.closeCh:
	}
}

// handleAck processes pkt's AckNr/SACK fields against our own sendBuf —
// the send-side counterpart to handleData's receive-side bookkeeping.
func (c *Conn) handleAck(pkt *Packet) {
	acked := 0
	for seq, entry := range c.sendBuf {
		if seqLessEq(seq, pkt.AckNr) {
			c.ackEntry(seq, entry, pkt)
			acked += len(entry.pkt.Payload)
			continue
		}
		if sackHasBit(pkt.SACK, pkt.AckNr, seq) {
			c.ackEntry(seq, entry, pkt)
			acked += len(entry.pkt.Payload)
		}
	}
	if acked > 0 {
		c.lc.onAck(time.Now(), time.Duration(pkt.TimestampDiff)*time.Microsecond, acked)
		c.rearmRTOTimer()
	}
}

func (c *Conn) ackEntry(seq uint16, entry *sendEntry, pkt *Packet) {
	if !entry.retransmitted {
		c.rto.sample(time.Since(entry.sentAt))
	}
	delete(c.sendBuf, seq)
}

// trySend packetizes as much of pendingWrite as the congestion window
// currently allows.
func (c *Conn) trySend() {
	for len(c.pendingWrite) > 0 {
		avail := c.lc.availableWindow()
		if avail < 1 {
			return
		}
		n := len(c.pendingWrite)
		if n > mss {
			n = mss
		}
		if n > avail {
			n = avail
		}
		if n == 0 {
			return
		}
		chunk := c.pendingWrite[:n]
		c.pendingWrite = c.pendingWrite[n:]

		seq := c.seqNr
		c.consumeSeq()
		pkt := &Packet{
			Type:          STData,
			ConnID:        c.sendID,
			SeqNr:         seq,
			AckNr:         c.ackNr,
			WndSize:       advertisedWindow,
			Timestamp:     nowMicro(),
			TimestampDiff: c.cachedDiff,
			Payload:       chunk,
		}
		c.sendBuf[seq] = &sendEntry{pkt: pkt, sentAt: time.Now()}
		c.lc.send(len(chunk))
		c.sendRaw(pkt)
	}
	c.rearmRTOTimer()
}

// sendState sends a pure ACK — the SACK bitmask, if any gap exists,
// reports every out-of-order packet currently buffered.
func (c *Conn) sendState() {
	pkt := &Packet{
		Type:          STState,
		ConnID:        c.sendID,
		SeqNr:         c.seqNr,
		AckNr:         c.ackNr,
		WndSize:       advertisedWindow,
		Timestamp:     nowMicro(),
		TimestampDiff: c.cachedDiff,
		SACK:          c.buildSACK(),
	}
	c.sendRaw(pkt)
}

func (c *Conn) buildSACK() []byte {
	if len(c.reorder) == 0 {
		return nil
	}
	maxSeq := c.ackNr
	for seq := range c.reorder {
		if seqLess(maxSeq, seq) {
			maxSeq = seq
		}
	}
	span := seqDiff(c.ackNr+sackBaseOffset, maxSeq) + 1
	if span < 1 {
		return nil
	}
	nbytes := (int(span) + 7) / 8
	nbytes = ((nbytes + sackLenMultiple - 1) / sackLenMultiple) * sackLenMultiple
	if nbytes < minSackLen {
		nbytes = minSackLen
	}
	sack := make([]byte, nbytes)
	for seq := range c.reorder {
		setSackBit(sack, c.ackNr, seq)
	}
	return sack
}

func (c *Conn) sendFin() {
	if c.haveFinSeq {
		return
	}
	seq := c.seqNr
	c.consumeSeq()
	c.finSeq = seq
	c.haveFinSeq = true
	pkt := &Packet{
		Type:          STFin,
		ConnID:        c.sendID,
		SeqNr:         seq,
		AckNr:         c.ackNr,
		WndSize:       advertisedWindow,
		Timestamp:     nowMicro(),
		TimestampDiff: c.cachedDiff,
	}
	c.sendBuf[seq] = &sendEntry{pkt: pkt, sentAt: time.Now()}
	c.sendRaw(pkt)
	c.state = stateClosing
}

// handleRTO fires when the oldest outstanding packet's retransmit timer
// expires with nothing acked in the meantime — RFC 6817's own congestion
// timeout backstop as well as this connection's ordinary retransmission
// trigger (see the package's own "go back 1" simplification note).
func (c *Conn) handleRTO() {
	oldest, ok := c.oldestUnacked()
	if !ok {
		c.rearmRTOTimer()
		return
	}
	c.rto.timeout()
	c.lc.onTimeout()
	oldest.retransmitted = true
	oldest.sentAt = time.Now()
	oldest.pkt.AckNr = c.ackNr
	oldest.pkt.TimestampDiff = c.cachedDiff
	oldest.pkt.Timestamp = nowMicro()
	c.sendRaw(oldest.pkt)
	c.rearmRTOTimer()
}

func (c *Conn) oldestUnacked() (*sendEntry, bool) {
	var best *sendEntry
	var bestSeq uint16
	found := false
	for seq, e := range c.sendBuf {
		if !found || seqLess(seq, bestSeq) {
			best, bestSeq, found = e, seq, true
		}
	}
	return best, found
}

func (c *Conn) rearmRTOTimer() {
	if c.rtoTimer == nil {
		return
	}
	if !c.rtoTimer.Stop() {
		select {
		case <-c.rtoTimer.C:
		default:
		}
	}
	c.rtoTimer.Reset(c.rto.current())
}

// sendRaw writes pkt to the wire unconditionally — every send path
// (handshake, data, state, fin, retransmit) funnels through here.
func (c *Conn) sendRaw(pkt *Packet) {
	_, _ = c.sock.pc.WriteTo(pkt.Marshal(), c.remoteAddr)
}

// waitConnected blocks until the handshake resolves or ctx is done.
func (c *Conn) waitConnected(ctx context.Context) error {
	select {
	case <-c.connectedCh:
		return c.connectErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
