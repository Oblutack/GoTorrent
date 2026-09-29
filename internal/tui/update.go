package tui

import (
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

// Update satisfies tea.Model. It never performs I/O itself - every network
// call lives in a Cmd (commands.go) - which is what makes this function
// directly testable with synthetic messages and no real client, WS
// connection, or terminal at all; see model_test.go.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case connectResultMsg:
		m.connecting = false
		if msg.err != nil {
			m.err = "connect: " + msg.err.Error()
			return m, nil
		}
		m.client = msg.client
		m.screen = screenList
		m.err = ""
		return m, tea.Batch(
			fetchTorrentsCmd(m.ctx, m.client),
			fetchSessionCmd(m.ctx, m.client),
			subscribeCmd(m.ctx, m.client),
			tickCmd(),
		)

	case torrentsMsg:
		if msg.err != nil {
			m.err = "list: " + msg.err.Error()
			return m, nil
		}
		m.torrents = msg.list
		m.table.SetRows(torrentRows(msg.list))
		return m, nil

	case sessionMsg:
		if msg.err != nil {
			m.err = "session: " + msg.err.Error()
			return m, nil
		}
		m.session = msg.stats
		return m, nil

	case subscribedMsg:
		if msg.err != nil {
			// Not fatal - the program still works via polling alone.
			m.err = "live updates unavailable: " + msg.err.Error()
			return m, nil
		}
		m.wsEvents = msg.events
		m.closeWS = msg.closeFn
		m.wsConnected = true
		return m, waitForEventCmd(m.wsEvents)

	case wsEventMsg:
		cmd := m.handleWSEvent(msg.event)
		return m, tea.Batch(cmd, waitForEventCmd(m.wsEvents))

	case eventsClosedMsg:
		m.wsConnected = false
		return m, nil

	case tickMsg:
		if m.client == nil {
			return m, nil
		}
		return m, tea.Batch(
			fetchTorrentsCmd(m.ctx, m.client),
			fetchSessionCmd(m.ctx, m.client),
			tickCmd(),
		)

	case detailMsg:
		m.detailLoading = false
		if msg.err != nil {
			m.err = "detail: " + msg.err.Error()
			return m, nil
		}
		m.detail = msg.detail
		m.files = msg.files
		m.peers = msg.peers
		m.trackers = msg.trackers
		return m, nil

	case actionResultMsg:
		if msg.err != nil {
			m.err = msg.action + ": " + msg.err.Error()
			return m, nil
		}
		m.err = ""
		if m.client != nil {
			return m, fetchTorrentsCmd(m.ctx, m.client)
		}
		return m, nil

	case addResultMsg:
		if msg.err != nil {
			m.err = "add: " + msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.screen = screenList
		m.addInput.SetValue("")
		if m.client != nil {
			return m, fetchTorrentsCmd(m.ctx, m.client)
		}
		return m, nil
	}

	return m, nil
}

// handleWSEvent reacts to one live event. Deliberately coarse for v1: any
// torrent-shaped event triggers a full list refresh rather than patching
// one row in place (a real, worthwhile follow-up - see ROADMAP.md), and a
// sessionStats tick updates the status line directly, since that message
// already carries the exact same SessionStats a poll would fetch.
func (m *Model) handleWSEvent(ev tuiclient.WSEvent) tea.Cmd {
	switch ev.Kind {
	case "sessionStats":
		if ev.Session != nil {
			m.session = *ev.Session
		}
		return nil
	case "torrentAdded", "torrentRemoved", "torrentStateChanged", "pieceVerified":
		if m.client == nil {
			return nil
		}
		return fetchTorrentsCmd(m.ctx, m.client)
	default:
		return nil
	}
}

// quit cancels the program's own context and best-effort closes the WS
// connection before returning tea.Quit - shared by both quit key bindings
// (ctrl+c, always; "q" on screenList) rather than duplicating the same
// three-step shutdown sequence in two places.
func (m Model) quit() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	if m.closeWS != nil {
		_ = m.closeWS()
	}
	return tea.Quit
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, m.quit()
	}

	switch m.screen {
	case screenConnect:
		return m.handleConnectKey(msg)
	case screenList:
		return m.handleListKey(msg)
	case screenDetail:
		return m.handleDetailKey(msg)
	case screenAdd:
		return m.handleAddKey(msg)
	}
	return m, nil
}

func (m Model) handleConnectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab":
		if m.addrInput.Focused() {
			m.addrInput.Blur()
			m.tokenInput.Focus()
		} else {
			m.tokenInput.Blur()
			m.addrInput.Focus()
		}
		return m, nil
	case "enter":
		if m.connecting {
			return m, nil
		}
		m.connecting = true
		m.err = ""
		return m, connectCmd(m.ctx, m.addrInput.Value(), m.tokenInput.Value())
	}

	var cmd tea.Cmd
	if m.addrInput.Focused() {
		m.addrInput, cmd = m.addrInput.Update(msg)
	} else {
		m.tokenInput, cmd = m.tokenInput.Update(msg)
	}
	return m, cmd
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, m.quit()
	case "enter":
		hash, ok := m.selectedHash()
		if !ok || m.client == nil {
			return m, nil
		}
		m.screen = screenDetail
		m.detailHash = hash
		m.activeTab = tabFiles
		m.detailLoading = true
		return m, fetchDetailCmd(m.ctx, m.client, hash)
	case "a":
		m.screen = screenAdd
		m.addInput.Focus()
		m.err = ""
		return m, textinput.Blink
	case "p", "r", "v", "n", "d":
		hash, ok := m.selectedHash()
		if !ok || m.client == nil {
			return m, nil
		}
		action := map[string]string{"p": "pause", "r": "resume", "v": "verify", "n": "reannounce", "d": "delete"}[msg.String()]
		return m, actionCmd(m.ctx, m.client, action, hash)
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace", "q":
		m.screen = screenList
		return m, nil
	case "tab":
		m.activeTab = (m.activeTab + 1) % 3
		return m, nil
	}
	return m, nil
}

func (m Model) handleAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenList
		m.addInput.SetValue("")
		m.addInput.Blur()
		return m, nil
	case "enter":
		if m.client == nil || m.addInput.Value() == "" {
			return m, nil
		}
		return m, addCmd(m.ctx, m.client, m.addInput.Value())
	}
	var cmd tea.Cmd
	m.addInput, cmd = m.addInput.Update(msg)
	return m, cmd
}

// torrentRows renders TorrentSummary values into the table's own Row
// shape - kept as a free function (not a Model method) since it's pure
// data transformation with no state of its own, easy to test in isolation.
func torrentRows(list []tuiclient.TorrentSummary) []table.Row {
	rows := make([]table.Row, len(list))
	for i, t := range list {
		rows[i] = table.Row{
			t.Name,
			t.State,
			progressBar(t.Progress()),
			humanBytes(t.Downloaded),
			humanBytes(t.Uploaded),
			itoa(t.PeerCount),
			ratioString(t.SeedRatio),
		}
	}
	return rows
}
