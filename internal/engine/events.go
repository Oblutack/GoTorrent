package engine

import (
	"sync"
	"time"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// EventKind identifies what an Event reports.
type EventKind string

const (
	EventTorrentAdded        EventKind = "torrentAdded"
	EventTorrentRemoved      EventKind = "torrentRemoved"
	EventTorrentStateChanged EventKind = "torrentStateChanged"
	EventPeerConnected       EventKind = "peerConnected"
	EventPeerDisconnected    EventKind = "peerDisconnected"
	EventPieceVerified       EventKind = "pieceVerified"
)

// Event is one fleet-wide occurrence, published via Subscribe — 4.2's
// WebSocket event stream is the only consumer today, but nothing here is
// API-package-specific, so a future consumer (a log sink, a metrics
// exporter) can subscribe the same way. Only the fields relevant to Kind
// are meaningful; the rest are zero.
type Event struct {
	Kind     EventKind
	InfoHash metainfo.Hash
	Time     time.Time
	// State is set for EventTorrentStateChanged.
	State torrent.State
	// PeerAddr is set for EventPeerConnected/EventPeerDisconnected.
	PeerAddr string
	// PieceIndex is set for EventPieceVerified.
	PieceIndex int
}

// eventSubscriberBuffer bounds how many not-yet-delivered events one
// subscriber channel holds before broadcast starts dropping for it — the
// same non-blocking, drop-under-backpressure shape peer.Client.Events and
// this package's own reasoning elsewhere already use: one slow WebSocket
// client must never be able to stall delivery to every other connected
// one, let alone the actor goroutines broadcasting into this from their
// own callbacks.
const eventSubscriberBuffer = 64

// Subscribe registers a new listener for every Event this Engine
// publishes from here on (nothing is replayed — a subscriber only sees
// events from after it subscribed) and returns the channel plus a cancel
// function the caller must eventually call to unsubscribe and release it.
// Safe to call from any goroutine, at any time.
func (e *Engine) Subscribe() (<-chan Event, func()) {
	e.eventMu.Lock()
	if e.eventSubs == nil {
		e.eventSubs = make(map[int]chan Event)
	}
	id := e.nextEventSubID
	e.nextEventSubID++
	ch := make(chan Event, eventSubscriberBuffer)
	e.eventSubs[id] = ch
	e.eventMu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			e.eventMu.Lock()
			delete(e.eventSubs, id)
			e.eventMu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}

// broadcast publishes ev to every current subscriber, non-blocking. Safe
// to call from any goroutine — in practice, always from a torrent's own
// actor-callback goroutines (OnStateChange, OnPeerConnected, ...), never
// synchronously from the actor itself.
func (e *Engine) broadcast(ev Event) {
	ev.Time = time.Now()
	e.eventMu.Lock()
	defer e.eventMu.Unlock()
	for _, ch := range e.eventSubs {
		select {
		case ch <- ev:
		default:
		}
	}
}
