package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

// requestTimeout bounds every single request this program makes -
// distinct from m.ctx, which lives for the whole program's run; a hung
// gottrentd should time out one refresh, not freeze the UI forever.
const requestTimeout = 10 * time.Second

// toastLifetime is how long a toast stays on screen.
const toastLifetime = 4 * time.Second

func connectCmd(ctx context.Context, addr, token string) tea.Cmd {
	return func() tea.Msg {
		addr = strings.TrimSpace(addr)
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		c := tuiclient.New(addr, strings.TrimSpace(token))

		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		if _, err := c.GetSession(reqCtx); err != nil {
			return connectResultMsg{err: err}
		}
		return connectResultMsg{client: c}
	}
}

func fetchTorrentsCmd(ctx context.Context, c *tuiclient.Client) tea.Cmd {
	return func() tea.Msg {
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		list, err := c.ListTorrents(reqCtx)
		return torrentsMsg{list: list, at: time.Now(), err: err}
	}
}

func fetchSessionCmd(ctx context.Context, c *tuiclient.Client) tea.Cmd {
	return func() tea.Msg {
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		stats, err := c.GetSession(reqCtx)
		return sessionMsg{stats: stats, at: time.Now(), err: err}
	}
}

func fetchDetailCmd(ctx context.Context, c *tuiclient.Client, hash string) tea.Cmd {
	return func() tea.Msg {
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()

		fail := func(err error) tea.Msg { return detailMsg{hash: hash, err: err} }
		detail, err := c.GetDetail(reqCtx, hash)
		if err != nil {
			return fail(err)
		}
		files, err := c.GetFiles(reqCtx, hash)
		if err != nil {
			return fail(err)
		}
		peers, err := c.GetPeers(reqCtx, hash)
		if err != nil {
			return fail(err)
		}
		trackers, err := c.GetTrackers(reqCtx, hash)
		if err != nil {
			return fail(err)
		}
		pieces, err := c.GetPieces(reqCtx, hash)
		if err != nil {
			return fail(err)
		}
		return detailMsg{hash: hash, detail: detail, files: files, peers: peers, trackers: trackers, pieces: pieces, at: time.Now()}
	}
}

// subscribeCmd connects the live WS event stream once - see waitForEventCmd
// for how events keep flowing after this.
func subscribeCmd(ctx context.Context, c *tuiclient.Client) tea.Cmd {
	return func() tea.Msg {
		events, closeFn, err := tuiclient.Subscribe(ctx, c)
		return subscribedMsg{events: events, closeFn: closeFn, err: err}
	}
}

// waitForEventCmd blocks on one receive from events and returns it as a
// message - the standard Bubble Tea pattern for turning a long-lived
// channel into a stream of messages: Update must call this again after
// handling each wsEventMsg/eventsClosedMsg to keep listening, since a Cmd
// only ever fires once.
func waitForEventCmd(events <-chan tuiclient.WSEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return eventsClosedMsg{}
		}
		return wsEventMsg{event: ev}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func toastExpireCmd(id int) tea.Cmd {
	return tea.Tick(toastLifetime, func(time.Time) tea.Msg { return toastExpiredMsg{id: id} })
}

// actionsCmd applies one action to every hash in turn and reports a single
// combined result, so acting on a multi-selection raises one toast rather
// than one per torrent.
func actionsCmd(ctx context.Context, c *tuiclient.Client, action string, hashes []string) tea.Cmd {
	return func() tea.Msg {
		res := actionResultMsg{action: action, count: len(hashes)}
		for _, hash := range hashes {
			reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
			var err error
			switch action {
			case "pause":
				err = c.Pause(reqCtx, hash)
			case "resume":
				err = c.Resume(reqCtx, hash)
			case "verify":
				err = c.Verify(reqCtx, hash)
			case "reannounce":
				err = c.Reannounce(reqCtx, hash)
			case "remove":
				err = c.Delete(reqCtx, hash, false)
			case "remove+data":
				err = c.Delete(reqCtx, hash, true)
			}
			cancel()
			if err != nil {
				res.failed++
				if res.err == nil {
					res.err = err
				}
			}
		}
		return res
	}
}

// addCmd dispatches on what looks like a magnet URI, an http(s) URL, or a
// local file path - the same three-way AddRequest dispatch
// internal/api/add.go's own handler makes server-side, just decided here
// since the TUI only has one free-text field to type into rather than
// Desktop's three separate dialog fields.
func addCmd(ctx context.Context, c *tuiclient.Client, input string) tea.Cmd {
	return func() tea.Msg {
		input = strings.TrimSpace(input)
		reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()

		var err error
		switch {
		case strings.HasPrefix(input, "magnet:"):
			_, err = c.AddMagnet(reqCtx, input)
		case strings.HasPrefix(input, "http://"), strings.HasPrefix(input, "https://"):
			_, err = c.AddURL(reqCtx, input)
		default:
			_, err = c.AddTorrentFile(reqCtx, input)
		}
		return addResultMsg{err: err}
	}
}
