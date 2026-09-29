// Package tui is the terminal UI: a github.com/charmbracelet/bubbletea
// program driving internal/tuiclient against a real gottrentd, the same
// REST+WS control API Desktop already uses — see this project's own
// ROADMAP.md/CLAUDE.md for the full architecture story. Model, Update, and
// View are kept in separate files (model.go/update.go/view.go) purely for
// readability; Update never performs I/O itself (every network call lives
// in a Cmd, commands.go), which is what makes Update directly testable
// with synthetic messages and no real client or terminal — see
// model_test.go.
package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

// pollInterval is the fallback refresh cadence for the torrent list and
// session stats — Desktop's own MainViewModel uses the same 2s figure for
// its own auto-refresh timer, mirrored here so the two clients feel
// consistent. WS events (subscribeCmd/waitForEventCmd) keep the list
// feeling live between polls; polling is what recovers if a WS connection
// drops without the reconnect logic (a real, deliberately-deferred v2
// item - see ROADMAP.md) ever kicking in.
var pollInterval = 2 * time.Second

// screen is which top-level view is showing.
type screen int

const (
	screenConnect screen = iota
	screenList
	screenDetail
	screenAdd
)

// detailTab is which tab is active within screenDetail.
type detailTab int

const (
	tabFiles detailTab = iota
	tabPeers
	tabTrackers
)

// Model is the whole program's state.
type Model struct {
	ctx    context.Context
	cancel context.CancelFunc

	screen screen
	err    string // last error, shown as a status line - never fatal to the program

	// Connect screen.
	addrInput  textinput.Model
	tokenInput textinput.Model
	connecting bool

	// Connected state.
	client      *tuiclient.Client
	torrents    []tuiclient.TorrentSummary
	table       table.Model
	session     tuiclient.SessionStats
	wsEvents    <-chan tuiclient.WSEvent
	closeWS     func() error
	wsConnected bool

	// Detail screen.
	detailHash    string
	detail        tuiclient.TorrentDetail
	files         []tuiclient.FileEntry
	peers         []tuiclient.PeerEntry
	trackers      []tuiclient.TrackerEntry
	activeTab     detailTab
	detailLoading bool

	// Add screen.
	addInput textinput.Model

	width, height int
}

// New builds the initial Model. addr/token pre-fill the connect screen
// (e.g. from -api-address/-token flags or the default token file) but
// never auto-connect on their own - Init always starts on screenConnect,
// matching Desktop's own "show a connect screen unless settings already
// say otherwise" shape, just without the auto-connect-from-saved-settings
// half yet (a real, deliberately deferred v2 item, same as WS reconnect).
func New(ctx context.Context, addr, token string) Model {
	ctx, cancel := context.WithCancel(ctx)

	addrInput := textinput.New()
	addrInput.Placeholder = "127.0.0.1:6880"
	addrInput.SetValue(addr)
	addrInput.Focus()

	tokenInput := textinput.New()
	tokenInput.Placeholder = "bearer token"
	tokenInput.SetValue(token)
	tokenInput.EchoMode = textinput.EchoPassword
	tokenInput.EchoCharacter = '•'

	addInput := textinput.New()
	addInput.Placeholder = "magnet:?xt=... or a .torrent file path"

	cols := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "State", Width: 12},
		{Title: "Progress", Width: 9},
		{Title: "Down", Width: 10},
		{Title: "Up", Width: 10},
		{Title: "Peers", Width: 6},
		{Title: "Ratio", Width: 6},
	}
	tbl := table.New(table.WithColumns(cols), table.WithFocused(true), table.WithHeight(15))

	return Model{
		ctx:        ctx,
		cancel:     cancel,
		screen:     screenConnect,
		addrInput:  addrInput,
		tokenInput: tokenInput,
		addInput:   addInput,
		table:      tbl,
	}
}

// Init satisfies tea.Model.
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// selectedHash returns the info hash of the table's currently highlighted
// row, and false if the list is empty - the one piece of index<->hash
// bookkeeping every action (pause/resume/verify/delete/view detail) needs,
// kept in one place rather than repeated at each call site.
func (m Model) selectedHash() (string, bool) {
	row := m.table.SelectedRow()
	if len(row) == 0 {
		return "", false
	}
	// Column 0 is Name, not the hash - the hash is threaded through
	// separately via torrentRows/m.torrents by matching the table's
	// cursor index, since displaying a 40-char hex hash in the Name
	// column would crowd out the one field a human actually reads.
	i := m.table.Cursor()
	if i < 0 || i >= len(m.torrents) {
		return "", false
	}
	return m.torrents[i].InfoHash, true
}
