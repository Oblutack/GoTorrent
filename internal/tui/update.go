package tui

import (
	"fmt"

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
		m.clampCursor()
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
		m.fetching = false
		if msg.err != nil {
			m.err = "list: " + msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.torrents = msg.list
		m.observeTorrents(msg.list, msg.at)
		m.applyView()
		return m, nil

	case sessionMsg:
		if msg.err != nil {
			m.err = "session: " + msg.err.Error()
			return m, nil
		}
		m.session = msg.stats
		m.speed.observe(msg.stats.TotalDownloaded, msg.stats.TotalUploaded, msg.at)
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
		m.fetching = true
		cmds := []tea.Cmd{
			fetchTorrentsCmd(m.ctx, m.client),
			fetchSessionCmd(m.ctx, m.client),
			tickCmd(),
		}
		if m.screen == screenDetail && m.detailHash != "" {
			cmds = append(cmds, fetchDetailCmd(m.ctx, m.client, m.detailHash))
		}
		return m, tea.Batch(cmds...)

	case detailMsg:
		if msg.hash != m.detailHash {
			return m, nil // a reply for a torrent the user has already left
		}
		m.detailLoading = false
		if msg.err != nil {
			m.err = "detail: " + msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.detail = msg.detail
		m.files = msg.files
		m.peers = msg.peers
		m.trackers = msg.trackers
		m.pieces = msg.pieces
		m.detailAt = msg.at
		m.observePeers(msg.peers, msg.at)
		return m, nil

	case actionResultMsg:
		return m.handleActionResult(msg)

	case addResultMsg:
		if msg.err != nil {
			return m, m.pushToast(toastErr, "add failed: "+msg.err.Error())
		}
		m.screen = screenList
		m.addInput.SetValue("")
		m.addInput.Blur()
		cmds := []tea.Cmd{m.pushToast(toastOK, "Torrent added")}
		if m.client != nil {
			cmds = append(cmds, fetchTorrentsCmd(m.ctx, m.client))
		}
		return m, tea.Batch(cmds...)

	case toastExpiredMsg:
		m.dropToast(msg.id)
		return m, nil
	}

	return m, nil
}

// handleWSEvent reacts to one live event. A sessionStats tick updates the
// header directly (it carries exactly what a poll would fetch); any
// torrent-shaped event triggers a list refresh, but only if one isn't
// already in flight - a busy download emits pieceVerified events by the
// hundred per second, and refetching the whole list for each would swamp
// both ends. A skipped refresh is harmless: the very next poll or event
// catches up.
func (m *Model) handleWSEvent(ev tuiclient.WSEvent) tea.Cmd {
	switch ev.Kind {
	case "sessionStats":
		if ev.Session != nil {
			m.session = *ev.Session
			if !ev.Time.IsZero() {
				m.speed.observe(ev.Session.TotalDownloaded, ev.Session.TotalUploaded, ev.Time)
			}
		}
		return nil
	case "torrentAdded", "torrentRemoved", "torrentStateChanged", "pieceVerified":
		if m.client == nil || m.fetching {
			return nil
		}
		m.fetching = true
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
	if m.modal != modalNone {
		return m.handleModalKey(msg)
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
	case "tab", "shift+tab", "down", "up":
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
	if m.searching {
		return m.handleSearchKey(msg)
	}

	switch msg.String() {
	case "q":
		return m, m.quit()
	case "?":
		m.modal = modalHelp
		return m, nil
	case "/":
		m.searching = true
		m.searchInput.SetValue(m.query)
		m.searchInput.CursorEnd()
		m.searchInput.Focus()
		return m, textinput.Blink
	case "esc":
		switch {
		case len(m.marked) > 0:
			m.marked = map[string]bool{}
		case m.query != "":
			m.query = ""
			m.applyView()
		}
		return m, nil
	case "tab":
		if m.focus == focusList {
			m.focus = focusSidebar
		} else {
			m.focus = focusList
		}
		return m, nil
	case "left", "h":
		m.focus = focusSidebar
		return m, nil
	case "right", "l":
		m.focus = focusList
		return m, nil
	case "up", "k":
		m.move(-1)
		return m, nil
	case "down", "j":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-m.listRows())
		return m, nil
	case "pgdown":
		m.move(m.listRows())
		return m, nil
	case "home", "g":
		m.move(-1 << 30)
		return m, nil
	case "end", "G":
		m.move(1 << 30)
		return m, nil
	case "enter":
		if m.focus == focusSidebar {
			m.focus = focusList
			return m, nil
		}
		t, ok := m.current()
		if !ok || m.client == nil {
			return m, nil
		}
		return m.openDetail(t.InfoHash)
	case " ":
		if t, ok := m.current(); ok {
			if m.marked[t.InfoHash] {
				delete(m.marked, t.InfoHash)
			} else {
				m.marked[t.InfoHash] = true
			}
			m.move(1)
		}
		return m, nil
	case "ctrl+a":
		for _, t := range m.visible {
			m.marked[t.InfoHash] = true
		}
		return m, nil
	case "s":
		m.sortCol = (m.sortCol + 1) % sortColCount
		m.sortDesc = m.sortCol != sortName && m.sortCol != sortState
		m.applyView()
		return m, m.pushToast(toastInfo, fmt.Sprintf("Sorted by %s %s", sortNames[m.sortCol], arrow(m.sortDesc)))
	case "S":
		m.sortDesc = !m.sortDesc
		m.applyView()
		return m, m.pushToast(toastInfo, fmt.Sprintf("Sorted by %s %s", sortNames[m.sortCol], arrow(m.sortDesc)))
	case "a":
		m.screen = screenAdd
		m.addInput.Focus()
		return m, textinput.Blink
	case "p":
		return m.runAction("pause", m.actionTargets())
	case "r":
		return m.runAction("resume", m.actionTargets())
	case "v":
		return m.runAction("verify", m.actionTargets())
	case "n":
		return m.runAction("reannounce", m.actionTargets())
	case "d", "delete":
		return m.askDelete(m.actionTargets())
	}
	return m, nil
}

func arrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
}

// move moves whichever pane has focus: the list cursor, or the sidebar
// selection (which applies its filter immediately).
func (m *Model) move(delta int) {
	if m.focus == focusSidebar {
		m.sideCursor += delta
		if m.sideCursor >= len(m.side) {
			m.sideCursor = len(m.side) - 1
		}
		if m.sideCursor < 0 {
			m.sideCursor = 0
		}
		if m.sideCursor < len(m.side) && m.side[m.sideCursor].f != m.filter {
			m.filter = m.side[m.sideCursor].f
			m.cursor, m.offset = 0, 0
			m.applyView()
		}
		return
	}
	m.cursor += delta
	m.clampCursor()
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searching = false
		m.searchInput.Blur()
		return m, nil
	case "esc":
		m.searching = false
		m.searchInput.Blur()
		m.query = ""
		m.applyView()
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	if m.query != m.searchInput.Value() {
		m.query = m.searchInput.Value()
		m.cursor, m.offset = 0, 0
		m.applyView()
	}
	return m, cmd
}

func (m Model) openDetail(hash string) (tea.Model, tea.Cmd) {
	m.screen = screenDetail
	m.detailHash = hash
	m.activeTab = tabOverview
	m.detailLoading = true
	m.detailScroll = 0
	m.detail = tuiclient.TorrentDetail{}
	m.files, m.peers, m.trackers = nil, nil, nil
	m.pieces = tuiclient.PiecesResponse{}
	m.peerRates = map[string]*rateState{}
	return m, fetchDetailCmd(m.ctx, m.client, hash)
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace", "q":
		m.screen = screenList
		return m, nil
	case "?":
		m.modal = modalHelp
		return m, nil
	case "tab", "right", "l":
		m.activeTab = (m.activeTab + 1) % tabCount
		m.detailScroll = 0
		return m, nil
	case "shift+tab", "left", "h":
		m.activeTab = (m.activeTab + tabCount - 1) % tabCount
		m.detailScroll = 0
		return m, nil
	case "1", "2", "3", "4", "5":
		m.activeTab = detailTab(msg.String()[0] - '1')
		m.detailScroll = 0
		return m, nil
	case "up", "k":
		if m.detailScroll > 0 {
			m.detailScroll--
		}
		return m, nil
	case "down", "j":
		m.detailScroll++
		return m, nil
	case "pgup":
		m.detailScroll = max(0, m.detailScroll-10)
		return m, nil
	case "pgdown":
		m.detailScroll += 10
		return m, nil
	case "p":
		return m.runAction("pause", []string{m.detailHash})
	case "r":
		return m.runAction("resume", []string{m.detailHash})
	case "v":
		return m.runAction("verify", []string{m.detailHash})
	case "n":
		return m.runAction("reannounce", []string{m.detailHash})
	case "d", "delete":
		return m.askDelete([]string{m.detailHash})
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

// askDelete opens the confirmation modal for the given torrents.
func (m Model) askDelete(hashes []string) (tea.Model, tea.Cmd) {
	if len(hashes) == 0 {
		return m, nil
	}
	m.confirm = hashes
	m.modal = modalDelete
	return m, nil
}

func (m Model) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.modal {
	case modalHelp:
		switch msg.String() {
		case "?", "esc", "q", "enter":
			m.modal = modalNone
		}
		return m, nil

	case modalDelete:
		switch msg.String() {
		case "y", "enter":
			return m.confirmDelete("remove")
		case "D":
			return m.confirmDelete("remove+data")
		case "n", "esc", "q":
			m.modal = modalNone
			m.confirm = nil
		}
		return m, nil
	}
	return m, nil
}

func (m Model) confirmDelete(action string) (tea.Model, tea.Cmd) {
	hashes := m.confirm
	m.modal = modalNone
	m.confirm = nil
	if m.screen == screenDetail {
		m.screen = screenList
	}
	return m.runAction(action, hashes)
}

// runAction dispatches an action against the given torrents.
func (m Model) runAction(action string, hashes []string) (tea.Model, tea.Cmd) {
	if m.client == nil || len(hashes) == 0 {
		return m, nil
	}
	return m, actionsCmd(m.ctx, m.client, action, hashes)
}

var actionVerbs = map[string]string{
	"pause":       "Paused",
	"resume":      "Resumed",
	"verify":      "Verifying",
	"reannounce":  "Reannounced",
	"remove":      "Removed",
	"remove+data": "Removed, with data,",
}

func (m Model) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	noun := "torrent"
	if msg.count != 1 {
		noun = fmt.Sprintf("%d torrents", msg.count)
	}

	var toastCmd tea.Cmd
	switch {
	case msg.err != nil && msg.failed >= msg.count:
		toastCmd = m.pushToast(toastErr, fmt.Sprintf("%s failed: %v", msg.action, msg.err))
	case msg.err != nil:
		toastCmd = m.pushToast(toastErr, fmt.Sprintf("%s: %d of %d failed: %v", msg.action, msg.failed, msg.count, msg.err))
	default:
		toastCmd = m.pushToast(toastOK, fmt.Sprintf("%s %s", actionVerbs[msg.action], noun))
	}

	if msg.failed >= msg.count || m.client == nil {
		return m, toastCmd
	}
	if msg.action == "remove" || msg.action == "remove+data" {
		m.marked = map[string]bool{}
	}
	cmds := []tea.Cmd{toastCmd, fetchTorrentsCmd(m.ctx, m.client)}
	if m.screen == screenDetail && m.detailHash != "" {
		cmds = append(cmds, fetchDetailCmd(m.ctx, m.client, m.detailHash))
	}
	return m, tea.Batch(cmds...)
}
