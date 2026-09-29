package tuiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/ws"
)

// Subscribe dials gottrentd's GET /api/v1/events WebSocket stream (built
// on internal/ws.DialClient, the client-side counterpart internal/ws
// gained specifically for this) and decodes each frame as a WSEvent onto
// the returned channel. The channel is closed once the read loop exits,
// for whatever reason (a real disconnect, the caller's own close, a
// malformed frame), so a caller ranging over it always terminates rather
// than hanging on a dead connection.
//
// Subscribe derives its own cancellable context from ctx specifically so
// the returned close func can unblock BOTH halves of the read loop at
// once: conn.Close alone only unblocks a goroutine currently inside
// ReadMessage, not one already blocked trying to hand an already-read
// event to an unbuffered channel nobody is draining anymore (a real caller
// that stops ranging over events without also cancelling ctx would
// otherwise leak this goroutine forever) — cancelling the internal context
// unblocks that send via the select below regardless of which of the two
// the goroutine happens to be parked in.
func Subscribe(ctx context.Context, c *Client) (<-chan WSEvent, func() error, error) {
	header := http.Header{}
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}
	conn, err := ws.DialClient(ctx, c.eventsURL(), header)
	if err != nil {
		return nil, nil, fmt.Errorf("tuiclient: connecting to event stream: %w", err)
	}

	innerCtx, cancel := context.WithCancel(ctx)
	events := make(chan WSEvent)
	go func() {
		defer close(events)
		defer conn.Close()
		for {
			op, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if op != ws.OpText {
				continue
			}
			var ev WSEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				continue
			}
			select {
			case events <- ev:
			case <-innerCtx.Done():
				return
			}
		}
	}()

	go func() {
		<-innerCtx.Done()
		conn.Close()
	}()

	closeFn := func() error {
		cancel()
		return conn.Close()
	}
	return events, closeFn, nil
}
