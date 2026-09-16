package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
	"github.com/Oblutack/GoTorrent/internal/ws"
)

// defaultLogTail is how many recent history entries LogsHandler sends
// immediately on connect, before switching to live streaming — enough
// recent context to be useful without dumping the entire logger history
// buffer on every connect. Overridable via "?tail=" (0 sends nothing from
// history at all, matching logger.Tail's own "n == 0 means none" contract
// — a caller that only wants to watch what happens from here on).
const defaultLogTail = 200

// historyBurstDeadline bounds how long writeWSJSONReliably will keep
// retrying a single history-burst frame against a full outbound queue
// before giving up on the whole connection — generous, since the only
// thing it's waiting on is sendLoop draining a bounded, in-memory queue
// over a presumably-healthy connection, not anything that could hang.
const historyBurstDeadline = 5 * time.Second

// WSLogEntry is one message on the wire, WS /api/v1/logs — logger.Entry
// itself is not marshaled directly, same "an API response is a public
// contract" reasoning as WSEvent not being engine.Event reused.
type WSLogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

func wsLogEntryDTO(e logger.Entry) WSLogEntry {
	return WSLogEntry{Time: e.Time, Level: e.Level, Message: e.Message}
}

// writeWSJSONReliably is writeWSJSON's counterpart for delivering a known
// batch (the history burst) to one client rather than broadcasting a
// live event to many — it retries on ws.ErrQueueFull (the outbound queue
// being momentarily full, not the connection being gone) with a short
// pause, up to historyBurstDeadline, instead of giving up on the first
// transient full queue the way writeWSJSON's normal contract does.
func writeWSJSONReliably(conn *ws.Conn, v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return true // a bad DTO is a bug worth not crashing over, not a reason to drop the connection
	}
	deadline := time.Now().Add(historyBurstDeadline)
	for {
		err := conn.WriteJSON(data)
		if err == nil {
			return true
		}
		if !errors.Is(err, ws.ErrQueueFull) || time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// LogsHandler serves WS /api/v1/logs — Stage 5's "so the Desktop can show
// what the daemon is actually doing," previously not possible at all
// short of running gottrentd under -verbose and reading its raw stderr.
// Sends up to "?tail=" (default defaultLogTail) recent entries immediately
// on connect, then every new entry as it's recorded, live, regardless of
// whether this process was started with -verbose — see logger.Init's own
// doc comment for why that flag no longer gates what this route can see.
// The connection's read side is only ever expected to see ping/pong and
// the close handshake, the same as EventsHandler.
func LogsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Upgrade(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer conn.Close()

		n := defaultLogTail
		if q := r.URL.Query().Get("tail"); q != "" {
			if parsed, err := strconv.Atoi(q); err == nil && parsed >= 0 {
				n = parsed
			}
		}
		// writeWSJSONReliably, not writeWSJSON: a history dump can easily
		// exceed ws.outboundQueueSize (32) in one tight loop, well before
		// sendLoop's single goroutine has drained any of it over the real
		// socket — writeWSJSON's normal "drop it and give up" contract
		// (right for EventsHandler's per-event broadcast, see its own doc
		// comment) would silently truncate a large tail and hang up the
		// connection right in the middle of its very first delivery. Found
		// exactly this way — a real, live test using a real ws.Conn caught
		// a burst of ~70 entries cutting off partway through — not by
		// reasoning about it in advance.
		for _, e := range logger.Tail(n) {
			if !writeWSJSONReliably(conn, wsLogEntryDTO(e)) {
				return
			}
		}

		sub, cancel := logger.Subscribe()
		defer cancel()

		closed := make(chan struct{})
		go func() {
			defer close(closed)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()

		for {
			select {
			case <-closed:
				return
			case e, ok := <-sub:
				if !ok {
					return
				}
				if !writeWSJSON(conn, wsLogEntryDTO(e)) {
					return
				}
			}
		}
	}
}
