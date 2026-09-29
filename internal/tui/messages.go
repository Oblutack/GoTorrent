package tui

import "github.com/Oblutack/GoTorrent/internal/tuiclient"

// connectResultMsg is connectCmd's result - success carries a ready
// *tuiclient.Client (already probed reachable via a real GetSession call,
// the same "don't just construct a client, prove it can actually reach
// the daemon" reasoning Desktop's own TryReachAsync documents).
type connectResultMsg struct {
	client *tuiclient.Client
	err    error
}

// torrentsMsg is fetchTorrentsCmd's result, the polling-refresh path.
type torrentsMsg struct {
	list []tuiclient.TorrentSummary
	err  error
}

// sessionMsg is fetchSessionCmd's result.
type sessionMsg struct {
	stats tuiclient.SessionStats
	err   error
}

// detailMsg is fetchDetailCmd's result - one message carrying all four
// detail-screen fetches together (detail/files/peers/trackers), since
// they're always requested as one logical "load this torrent's detail
// view" unit and there's no real value in surfacing four separate partial-
// failure states to the user.
type detailMsg struct {
	detail   tuiclient.TorrentDetail
	files    []tuiclient.FileEntry
	peers    []tuiclient.PeerEntry
	trackers []tuiclient.TrackerEntry
	err      error
}

// subscribedMsg is subscribeCmd's result - the live channel/close pair
// Subscribe returned, or an error if the WS handshake itself failed (the
// program still works via polling alone in that case, see wsConnected).
type subscribedMsg struct {
	events  <-chan tuiclient.WSEvent
	closeFn func() error
	err     error
}

// wsEventMsg is one event off the live stream, delivered by
// waitForEventCmd - Update re-issues waitForEventCmd after handling this
// so the next event is picked up too, the standard Bubble Tea "listen to a
// channel forever" pattern (see commands.go's own doc comment on it).
type wsEventMsg struct {
	event tuiclient.WSEvent
}

// eventsClosedMsg means the WS event channel closed (a real disconnect,
// or the program shutting down) - Update falls back to polling-only, it
// does not attempt to reconnect (a real, deliberately deferred v2 item;
// see ROADMAP.md).
type eventsClosedMsg struct{}

// tickMsg drives the polling-fallback refresh, see pollInterval.
type tickMsg struct{}

// actionResultMsg is the result of a fire-and-forget torrent action
// (pause/resume/verify/reannounce/delete) - action names which one, for
// the status line, since all five share this one message shape rather
// than five near-identical ones.
type actionResultMsg struct {
	action string
	err    error
}

// addResultMsg is addCmd's result.
type addResultMsg struct {
	err error
}
