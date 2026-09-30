// Package tui is the terminal UI: a github.com/charmbracelet/bubbletea
// program driving internal/tuiclient against a real gottrentd, the same
// REST+WS control API Desktop already uses — see this project's own
// ROADMAP.md/CLAUDE.md for the full architecture story. Model, Update, and
// View are kept in separate files (model.go/update.go/view*.go) purely for
// readability; Update never performs I/O itself (every network call lives
// in a Cmd, commands.go), which is what makes Update directly testable
// with synthetic messages and no real client or terminal — see
// model_test.go.
package tui

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

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

// modal is an overlay drawn over the list or detail screen.
type modal int

const (
	modalNone modal = iota
	modalHelp
	modalDelete
)

// focus is which pane of the list screen receives the arrow keys.
type focus int

const (
	focusList focus = iota
	focusSidebar
)

// detailTab is which tab is active within screenDetail.
type detailTab int

const (
	tabOverview detailTab = iota
	tabFiles
	tabPeers
	tabTrackers
	tabPieces
	tabCount
)

var tabNames = [tabCount]string{"Overview", "Files", "Peers", "Trackers", "Pieces"}

// sortCol is which column the torrent list is ordered by.
type sortCol int

const (
	sortAdded sortCol = iota
	sortName
	sortState
	sortProgress
	sortDown
	sortUp
	sortPeers
	sortRatio
	sortColCount
)

var sortNames = [sortColCount]string{"Added", "Name", "State", "Progress", "Down", "Up", "Peers", "Ratio"}

type filterKind int

const (
	filterAll filterKind = iota
	filterStatus
	filterCategory
	filterTag
)

// filter is one sidebar selection. It is comparable so a refresh can check
// whether the active one still exists.
type filter struct {
	kind  filterKind
	value string
}

// sideEntry is one selectable sidebar row.
type sideEntry struct {
	f     filter
	label string
	count int
}

type toastKind int

const (
	toastInfo toastKind = iota
	toastOK
	toastErr
)

type toast struct {
	id   int
	kind toastKind
	text string
}

// Model is the whole program's state.
type Model struct {
	ctx    context.Context
	cancel context.CancelFunc

	screen   screen
	modal    modal
	themeIdx int
	err      string // connection-level problem (list/session/live-events failing), cleared by the next success

	// Connect screen.
	addrInput  textinput.Model
	tokenInput textinput.Model
	connecting bool

	// Connected state.
	client      *tuiclient.Client
	torrents    []tuiclient.TorrentSummary
	session     tuiclient.SessionStats
	wsEvents    <-chan tuiclient.WSEvent
	closeWS     func() error
	wsConnected bool
	fetching    bool // a list refresh is in flight; live events don't pile more on

	// List screen: what is shown (visible) is torrents narrowed by filter and
	// search, then ordered by sortCol.
	visible     []tuiclient.TorrentSummary
	cursor      int
	offset      int
	marked      map[string]bool
	filter      filter
	side        []sideEntry
	sideCursor  int
	focus       focus
	searchInput textinput.Model
	searching   bool
	query       string
	sortCol     sortCol
	sortDesc    bool

	// Speeds, derived from successive cumulative readings (see rates.go).
	rates map[string]*rateState
	speed speedHistory

	// Delete confirmation: the hashes the modal will act on.
	confirm []string

	toasts    []toast
	nextToast int

	// Detail screen.
	detailHash    string
	detail        tuiclient.TorrentDetail
	files         []tuiclient.FileEntry
	peers         []tuiclient.PeerEntry
	trackers      []tuiclient.TrackerEntry
	pieces        tuiclient.PiecesResponse
	detailAt      time.Time
	peerRates     map[string]*rateState
	activeTab     detailTab
	detailLoading bool
	detailScroll  int

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
	addrInput.Width = 38 // set before the value: a value longer than the width must scroll, not wrap
	addrInput.Placeholder = "127.0.0.1:6880"
	addrInput.SetValue(addr)
	addrInput.Focus()

	tokenInput := textinput.New()
	tokenInput.Width = 38
	tokenInput.Placeholder = "bearer token"
	tokenInput.SetValue(token)
	tokenInput.EchoMode = textinput.EchoPassword
	tokenInput.EchoCharacter = '•'

	addInput := textinput.New()
	addInput.Placeholder = "magnet:?xt=... or a .torrent file path or URL"

	searchInput := textinput.New()
	searchInput.Placeholder = "filter by name"
	searchInput.Prompt = ""

	for _, in := range []*textinput.Model{&addrInput, &tokenInput, &addInput, &searchInput} {
		styleInput(in)
	}
	addInput.Width = 58
	searchInput.Width = 30

	return Model{
		ctx:         ctx,
		cancel:      cancel,
		screen:      screenConnect,
		addrInput:   addrInput,
		tokenInput:  tokenInput,
		addInput:    addInput,
		searchInput: searchInput,
		marked:      map[string]bool{},
		rates:       map[string]*rateState{},
		peerRates:   map[string]*rateState{},
		sortDesc:    true, // newest first by default
	}
}

func styleInput(in *textinput.Model) {
	in.TextStyle = styleText
	in.PlaceholderStyle = styleFaint
	in.Cursor.Style = styleKey
	if in.Prompt != "" {
		in.Prompt = "❯ "
	}
	in.PromptStyle = styleKey
}

// restyleInputs re-applies the current palette to the text inputs, which
// capture their colours when styled rather than reading the palette live.
func (m *Model) restyleInputs() {
	for _, in := range []*textinput.Model{&m.addrInput, &m.tokenInput, &m.addInput, &m.searchInput} {
		styleInput(in)
	}
}

// Init satisfies tea.Model.
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// size is the terminal size, with a sane default until the first
// WindowSizeMsg arrives.
func (m Model) size() (int, int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 30
	}
	return w, h
}

// Layout constants shared by Update (for scrolling) and View.
const (
	headerH  = 4
	footerH  = 2
	sidebarW = 26
)

func (m Model) bodyH() int {
	_, h := m.size()
	return h - headerH - footerH
}

// listRows is how many torrent rows fit in the list panel: the body minus
// the panel border (2), the column header (1) and the selection strip (2).
func (m Model) listRows() int {
	r := m.bodyH() - 5
	if r < 1 {
		r = 1
	}
	return r
}

// statusGroup maps an engine state to the sidebar status bucket it counts in.
func statusGroup(state string) string {
	switch state {
	case "Seeding":
		return "seeding"
	case "Paused":
		return "paused"
	case "Error":
		return "error"
	default:
		return "downloading"
	}
}

func (f filter) matches(t tuiclient.TorrentSummary) bool {
	switch f.kind {
	case filterStatus:
		return statusGroup(t.State) == f.value
	case filterCategory:
		return t.Category == f.value
	case filterTag:
		return slices.Contains(t.Tags, f.value)
	default:
		return true
	}
}

// rateFor returns a torrent's smoothed down/up speed, zero if not yet known.
func (m Model) rateFor(hash string) (down, up float64) {
	if r, ok := m.rates[hash]; ok {
		return r.down, r.up
	}
	return 0, 0
}

// observeTorrents folds a fresh list reading into the per-torrent rates and
// forgets torrents that are gone.
func (m *Model) observeTorrents(list []tuiclient.TorrentSummary, at time.Time) {
	seen := make(map[string]bool, len(list))
	for _, t := range list {
		seen[t.InfoHash] = true
		r, ok := m.rates[t.InfoHash]
		if !ok {
			r = &rateState{}
			m.rates[t.InfoHash] = r
		}
		r.observe(t.Downloaded, t.Uploaded, at)
	}
	for hash := range m.rates {
		if !seen[hash] {
			delete(m.rates, hash)
		}
	}
}

// observePeers folds a detail reading into per-peer speeds and forgets peers
// that have disconnected.
func (m *Model) observePeers(peers []tuiclient.PeerEntry, at time.Time) {
	seen := make(map[string]bool, len(peers))
	for _, p := range peers {
		seen[p.Addr] = true
		r, ok := m.peerRates[p.Addr]
		if !ok {
			r = &rateState{}
			m.peerRates[p.Addr] = r
		}
		r.observe(p.Downloaded, p.Uploaded, at)
	}
	for addr := range m.peerRates {
		if !seen[addr] {
			delete(m.peerRates, addr)
		}
	}
}

// buildSidebar recomputes the sidebar entries and their counts from the full
// torrent list, and falls back to "All" if the active filter has vanished
// (its last category was removed, say).
func (m *Model) buildSidebar() {
	status := map[string]int{}
	cats := map[string]int{}
	tags := map[string]int{}
	for _, t := range m.torrents {
		status[statusGroup(t.State)]++
		if t.Category != "" {
			cats[t.Category]++
		}
		for _, tg := range t.Tags {
			tags[tg]++
		}
	}
	entries := []sideEntry{
		{filter{filterAll, ""}, "All", len(m.torrents)},
		{filter{filterStatus, "downloading"}, "Downloading", status["downloading"]},
		{filter{filterStatus, "seeding"}, "Seeding", status["seeding"]},
		{filter{filterStatus, "paused"}, "Paused", status["paused"]},
		{filter{filterStatus, "error"}, "Errored", status["error"]},
	}
	for _, name := range sortedKeys(cats) {
		entries = append(entries, sideEntry{filter{filterCategory, name}, name, cats[name]})
	}
	for _, name := range sortedKeys(tags) {
		entries = append(entries, sideEntry{filter{filterTag, name}, name, tags[name]})
	}
	m.side = entries

	m.sideCursor = 0
	found := false
	for i, e := range entries {
		if e.f == m.filter {
			m.sideCursor, found = i, true
			break
		}
	}
	if !found {
		m.filter = filter{}
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applyView rebuilds everything derived from the raw torrent list: sidebar,
// the filtered and ordered visible list, marks, and a cursor that stays on
// the same torrent when rows move underneath it.
func (m *Model) applyView() {
	var cursorHash string
	if m.cursor >= 0 && m.cursor < len(m.visible) {
		cursorHash = m.visible[m.cursor].InfoHash
	}

	m.buildSidebar()

	q := strings.ToLower(strings.TrimSpace(m.query))
	vis := make([]tuiclient.TorrentSummary, 0, len(m.torrents))
	for _, t := range m.torrents {
		if !m.filter.matches(t) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(t.Name), q) {
			continue
		}
		vis = append(vis, t)
	}
	m.sortTorrents(vis)
	m.visible = vis

	// Drop marks for torrents that no longer exist.
	present := make(map[string]bool, len(m.torrents))
	for _, t := range m.torrents {
		present[t.InfoHash] = true
	}
	for hash := range m.marked {
		if !present[hash] {
			delete(m.marked, hash)
		}
	}

	if cursorHash != "" {
		for i, t := range vis {
			if t.InfoHash == cursorHash {
				m.cursor = i
				break
			}
		}
	}
	m.clampCursor()
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	rows := m.listRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if maxOff := len(m.visible) - rows; m.offset > maxOff {
		m.offset = maxOff
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m Model) sortTorrents(list []tuiclient.TorrentSummary) {
	less := func(a, b tuiclient.TorrentSummary) int {
		switch m.sortCol {
		case sortName:
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case sortState:
			return strings.Compare(a.State, b.State)
		case sortProgress:
			return cmpFloat(a.Progress(), b.Progress())
		case sortDown:
			ad, _ := m.rateFor(a.InfoHash)
			bd, _ := m.rateFor(b.InfoHash)
			return cmpFloat(ad, bd)
		case sortUp:
			_, au := m.rateFor(a.InfoHash)
			_, bu := m.rateFor(b.InfoHash)
			return cmpFloat(au, bu)
		case sortPeers:
			return a.PeerCount - b.PeerCount
		case sortRatio:
			return cmpFloat(a.SeedRatio, b.SeedRatio)
		default:
			return a.AddedOn.Compare(b.AddedOn)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		c := less(list[i], list[j])
		if c == 0 {
			// Keep ties in a fixed order so rows don't shuffle between refreshes.
			return list[i].InfoHash < list[j].InfoHash
		}
		if m.sortDesc {
			return c > 0
		}
		return c < 0
	})
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// current is the torrent under the list cursor.
func (m Model) current() (tuiclient.TorrentSummary, bool) {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return tuiclient.TorrentSummary{}, false
	}
	return m.visible[m.cursor], true
}

// actionTargets is what an action key acts on: every marked torrent (in
// list order) if there is a multi-selection, else the one under the cursor.
func (m Model) actionTargets() []string {
	if len(m.marked) > 0 {
		var out []string
		for _, t := range m.torrents {
			if m.marked[t.InfoHash] {
				out = append(out, t.InfoHash)
			}
		}
		return out
	}
	if t, ok := m.current(); ok {
		return []string{t.InfoHash}
	}
	return nil
}

// pushToast adds a toast and returns the command that will retire it.
func (m *Model) pushToast(kind toastKind, text string) tea.Cmd {
	m.nextToast++
	id := m.nextToast
	m.toasts = append(m.toasts, toast{id: id, kind: kind, text: text})
	if len(m.toasts) > 4 {
		m.toasts = m.toasts[len(m.toasts)-4:]
	}
	return toastExpireCmd(id)
}

func (m *Model) dropToast(id int) {
	for i, t := range m.toasts {
		if t.id == id {
			m.toasts = append(m.toasts[:i], m.toasts[i+1:]...)
			return
		}
	}
}
