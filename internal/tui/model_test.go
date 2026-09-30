package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

// Update never performs I/O itself (see its own doc comment), so every test
// here drives it with synthetic messages - no real client, WS connection, or
// terminal needed, the same "test the state machine directly" discipline
// Desktop's own ViewModel tests already established for this project's C# side.

func newTestModel() Model {
	return New(context.Background(), "", "")
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(key(k))
		m = next.(Model)
	}
	return m, cmd
}

var t0 = time.Unix(1_700_000_000, 0)

func sampleList() []tuiclient.TorrentSummary {
	return []tuiclient.TorrentSummary{
		{InfoHash: "aaa", Name: "alpha.iso", State: "Downloading", TotalLength: 1000, Left: 500, Downloaded: 500, PeerCount: 3, Category: "linux", AddedOn: t0.Add(1 * time.Hour)},
		{InfoHash: "bbb", Name: "Bravo.mkv", State: "Seeding", TotalLength: 2000, Left: 0, Downloaded: 2000, Uploaded: 4000, SeedRatio: 2, Tags: []string{"4k"}, AddedOn: t0.Add(2 * time.Hour)},
		{InfoHash: "ccc", Name: "charlie.pdf", State: "Paused", TotalLength: 300, Left: 300, AddedOn: t0.Add(3 * time.Hour)},
		{InfoHash: "ddd", Name: "delta.zip", State: "Error", TotalLength: 50, Left: 50, Category: "linux", AddedOn: t0.Add(4 * time.Hour)},
	}
}

func listModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel()
	m.width, m.height = 120, 36
	m.client = tuiclient.New("http://example.invalid", "")
	m.screen = screenList
	updated, _ := m.Update(torrentsMsg{list: sampleList(), at: t0})
	return updated.(Model)
}

func TestConnectResultSuccessTransitionsToListAndFetches(t *testing.T) {
	m := newTestModel()
	client := tuiclient.New("http://example.invalid", "")

	updated, cmd := m.Update(connectResultMsg{client: client})
	m = updated.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList", m.screen)
	}
	if m.client == nil {
		t.Fatal("client not set")
	}
	if cmd == nil {
		t.Fatal("want a batched fetch/subscribe/tick command, got nil")
	}
}

func TestConnectResultErrorStaysOnConnectScreen(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(connectResultMsg{err: errors.New("connection refused")})
	m = updated.(Model)

	if m.screen != screenConnect {
		t.Fatalf("screen = %v, want screenConnect", m.screen)
	}
	if m.err == "" {
		t.Fatal("want a non-empty error message")
	}
	if m.connecting {
		t.Fatal("connecting should be cleared after the result lands, success or not")
	}
}

func TestTorrentsMsgPopulatesVisibleListAndSidebarCounts(t *testing.T) {
	m := listModel(t)
	if len(m.visible) != 4 {
		t.Fatalf("len(visible) = %d, want 4", len(m.visible))
	}
	counts := map[string]int{}
	for _, e := range m.side {
		counts[e.label] = e.count
	}
	want := map[string]int{"All": 4, "Downloading": 1, "Seeding": 1, "Paused": 1, "Errored": 1, "linux": 2, "4k": 1}
	for label, n := range want {
		if counts[label] != n {
			t.Errorf("sidebar %q count = %d, want %d", label, counts[label], n)
		}
	}
}

func TestDefaultOrderIsNewestFirst(t *testing.T) {
	m := listModel(t)
	if m.visible[0].InfoHash != "ddd" || m.visible[3].InfoHash != "aaa" {
		t.Fatalf("order = %s..%s, want ddd (newest) first, aaa last", m.visible[0].InfoHash, m.visible[3].InfoHash)
	}
}

func TestEnterOnListOpensDetailAndFetchesIt(t *testing.T) {
	m := listModel(t)
	updated, cmd := m.Update(key("enter"))
	m = updated.(Model)

	if m.screen != screenDetail {
		t.Fatalf("screen = %v, want screenDetail", m.screen)
	}
	if m.detailHash != m.visible[m.cursor].InfoHash {
		t.Fatalf("detailHash = %q, want the row under the cursor", m.detailHash)
	}
	if !m.detailLoading {
		t.Fatal("want detailLoading set while the fetch is in flight")
	}
	if cmd == nil {
		t.Fatal("want a fetchDetailCmd, got nil")
	}
}

func TestEscOnDetailReturnsToList(t *testing.T) {
	m := newTestModel()
	m.screen = screenDetail
	m, _ = press(m, "esc")
	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList", m.screen)
	}
}

func TestTabOnDetailCyclesThroughAllFiveTabs(t *testing.T) {
	m := newTestModel()
	m.screen = screenDetail
	m.activeTab = tabOverview

	for _, want := range []detailTab{tabFiles, tabPeers, tabTrackers, tabPieces, tabOverview} {
		m, _ = press(m, "tab")
		if m.activeTab != want {
			t.Fatalf("activeTab = %v, want %v", m.activeTab, want)
		}
	}
}

func TestNumberKeysJumpStraightToATab(t *testing.T) {
	m := newTestModel()
	m.screen = screenDetail
	m, _ = press(m, "4")
	if m.activeTab != tabTrackers {
		t.Fatalf("activeTab = %v, want tabTrackers", m.activeTab)
	}
}

func TestDetailReplyForAnotherTorrentIsIgnored(t *testing.T) {
	m := newTestModel()
	m.screen = screenDetail
	m.detailHash = "aaa"
	m.detailLoading = true

	updated, _ := m.Update(detailMsg{hash: "zzz", peers: []tuiclient.PeerEntry{{Addr: "1.2.3.4:5"}}})
	m = updated.(Model)
	if len(m.peers) != 0 || !m.detailLoading {
		t.Fatal("a stale reply for another torrent must not overwrite the detail view")
	}
}

func TestWSEventTorrentStateChangedTriggersARefetchOnlyOnceAtATime(t *testing.T) {
	m := newTestModel()
	m.client = tuiclient.New("http://example.invalid", "")

	if cmd := m.handleWSEvent(tuiclient.WSEvent{Kind: "torrentStateChanged", InfoHash: "aaa"}); cmd == nil {
		t.Fatal("want a refetch command for the first event")
	}
	if cmd := m.handleWSEvent(tuiclient.WSEvent{Kind: "pieceVerified", InfoHash: "aaa"}); cmd != nil {
		t.Fatal("a second event while a refresh is in flight must not queue another")
	}

	updated, _ := m.Update(torrentsMsg{at: t0})
	m = updated.(Model)
	if cmd := m.handleWSEvent(tuiclient.WSEvent{Kind: "pieceVerified"}); cmd == nil {
		t.Fatal("once the refresh lands, the next event should refetch again")
	}
}

func TestWSEventSessionStatsUpdatesSessionAndSpeedHistory(t *testing.T) {
	m := newTestModel()
	m.wsEvents = make(chan tuiclient.WSEvent)

	first := tuiclient.SessionStats{TorrentCount: 5, TotalDownloaded: 1000}
	second := tuiclient.SessionStats{TorrentCount: 5, TotalDownloaded: 3000}
	for i, s := range []tuiclient.SessionStats{first, second} {
		s := s
		updated, _ := m.Update(wsEventMsg{event: tuiclient.WSEvent{Kind: "sessionStats", Session: &s, Time: t0.Add(time.Duration(i) * time.Second)}})
		m = updated.(Model)
	}

	if m.session.TorrentCount != 5 || m.session.TotalDownloaded != 3000 {
		t.Fatalf("session = %+v, want the latest event's stats", m.session)
	}
	if m.speed.down != 2000 {
		t.Fatalf("session download speed = %v B/s, want 2000", m.speed.down)
	}
	if len(m.speed.downHist) != 1 {
		t.Fatalf("history length = %d, want 1 sample", len(m.speed.downHist))
	}
}

func TestCtrlCQuits(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(key("ctrl+c"))
	if cmd == nil {
		t.Fatal("want tea.Quit, got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c did not return tea.Quit")
	}
}

func TestActionFailureRaisesAnErrorToastAndDoesNotRefetch(t *testing.T) {
	m := listModel(t)
	updated, cmd := m.Update(actionResultMsg{action: "pause", count: 1, failed: 1, err: errors.New("not managed by this engine")})
	m = updated.(Model)

	if len(m.toasts) != 1 || m.toasts[0].kind != toastErr || !strings.Contains(m.toasts[0].text, "not managed") {
		t.Fatalf("toasts = %+v, want one error toast carrying the message", m.toasts)
	}
	if cmd == nil {
		t.Fatal("want the toast-expiry command")
	}
}

func TestActionSuccessRaisesAnOKToastAndRefetches(t *testing.T) {
	m := listModel(t)
	updated, cmd := m.Update(actionResultMsg{action: "pause", count: 3})
	m = updated.(Model)

	if len(m.toasts) != 1 || m.toasts[0].kind != toastOK || !strings.Contains(m.toasts[0].text, "3 torrents") {
		t.Fatalf("toasts = %+v, want one OK toast naming 3 torrents", m.toasts)
	}
	if cmd == nil {
		t.Fatal("want toast expiry plus a refetch")
	}
}

func TestToastExpiresByID(t *testing.T) {
	m := newTestModel()
	m.pushToast(toastOK, "one")
	m.pushToast(toastOK, "two")
	updated, _ := m.Update(toastExpiredMsg{id: m.toasts[0].id})
	m = updated.(Model)
	if len(m.toasts) != 1 || m.toasts[0].text != "two" {
		t.Fatalf("toasts = %+v, want only the second to remain", m.toasts)
	}
}

func TestAddResultSuccessReturnsToListAndClearsInput(t *testing.T) {
	m := newTestModel()
	m.client = tuiclient.New("http://example.invalid", "")
	m.screen = screenAdd
	m.addInput.SetValue("magnet:?xt=urn:btih:abc")

	updated, cmd := m.Update(addResultMsg{})
	m = updated.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList", m.screen)
	}
	if m.addInput.Value() != "" {
		t.Fatalf("addInput value = %q, want cleared", m.addInput.Value())
	}
	if cmd == nil {
		t.Fatal("want a refetch command after a successful add")
	}
}

func TestAddResultErrorStaysOnTheAddScreenWithAToast(t *testing.T) {
	m := newTestModel()
	m.screen = screenAdd
	updated, _ := m.Update(addResultMsg{err: errors.New("already added")})
	m = updated.(Model)
	if m.screen != screenAdd || len(m.toasts) != 1 || m.toasts[0].kind != toastErr {
		t.Fatalf("screen=%v toasts=%+v, want to stay on the add screen with an error toast", m.screen, m.toasts)
	}
}

func TestSidebarFilterNarrowsTheList(t *testing.T) {
	m := listModel(t)
	m, _ = press(m, "left") // focus the sidebar
	m, _ = press(m, "down") // Downloading
	if len(m.visible) != 1 || m.visible[0].InfoHash != "aaa" {
		t.Fatalf("visible = %+v, want just the downloading torrent", m.visible)
	}

	// Walk down to the "linux" category.
	for m.filter.kind != filterCategory {
		m, _ = press(m, "down")
	}
	if len(m.visible) != 2 {
		t.Fatalf("category filter shows %d torrents, want 2", len(m.visible))
	}
}

func TestVanishedFilterFallsBackToAll(t *testing.T) {
	m := listModel(t)
	m.filter = filter{filterCategory, "linux"}
	m.applyView()
	updated, _ := m.Update(torrentsMsg{list: []tuiclient.TorrentSummary{{InfoHash: "x", Name: "x", State: "Seeding"}}, at: t0})
	m = updated.(Model)
	if m.filter != (filter{}) || len(m.visible) != 1 {
		t.Fatalf("filter=%+v visible=%d, want a fall back to All", m.filter, len(m.visible))
	}
}

func TestSearchNarrowsLiveAndEscClears(t *testing.T) {
	m := listModel(t)
	m, _ = press(m, "/")
	if !m.searching {
		t.Fatal("/ should start a search")
	}
	m, _ = press(m, "b", "r", "a")
	if len(m.visible) != 1 || m.visible[0].InfoHash != "bbb" {
		t.Fatalf("visible = %+v, want just Bravo (case-insensitive match)", m.visible)
	}
	m, _ = press(m, "enter")
	if m.searching || m.query != "bra" {
		t.Fatalf("after enter: searching=%v query=%q, want the filter kept", m.searching, m.query)
	}
	m, _ = press(m, "esc")
	if m.query != "" || len(m.visible) != 4 {
		t.Fatalf("after esc: query=%q visible=%d, want the filter cleared", m.query, len(m.visible))
	}
}

func TestSortCyclesColumnsAndKeepsTheCursorOnTheSameTorrent(t *testing.T) {
	m := listModel(t)
	m, _ = press(m, "down") // cursor on the second row
	hash := m.visible[m.cursor].InfoHash

	m, _ = press(m, "s") // -> Name, ascending
	if m.sortCol != sortName || m.sortDesc {
		t.Fatalf("sortCol=%v desc=%v, want name ascending", m.sortCol, m.sortDesc)
	}
	if m.visible[0].Name != "alpha.iso" || m.visible[1].Name != "Bravo.mkv" {
		t.Fatalf("name order = %v, want case-insensitive alphabetical", []string{m.visible[0].Name, m.visible[1].Name})
	}
	if m.visible[m.cursor].InfoHash != hash {
		t.Fatal("the cursor must follow its torrent when the list is re-sorted")
	}

	m, _ = press(m, "S")
	if !m.sortDesc || m.visible[0].Name != "delta.zip" {
		t.Fatalf("after S: desc=%v first=%s, want reversed", m.sortDesc, m.visible[0].Name)
	}
}

func TestMarkingSelectsSeveralAndActionsTargetThem(t *testing.T) {
	m := listModel(t)
	if got := m.actionTargets(); len(got) != 1 {
		t.Fatalf("with nothing marked, targets = %v, want just the cursor row", got)
	}
	m, _ = press(m, " ", " ") // mark two rows, moving down each time
	if len(m.marked) != 2 {
		t.Fatalf("marked = %v, want 2", m.marked)
	}
	if got := m.actionTargets(); len(got) != 2 {
		t.Fatalf("targets = %v, want both marked torrents", got)
	}
	m, _ = press(m, "ctrl+a")
	if len(m.marked) != 4 {
		t.Fatalf("ctrl+a marked %d, want all 4", len(m.marked))
	}
	m, _ = press(m, "esc")
	if len(m.marked) != 0 {
		t.Fatal("esc should clear the marks")
	}
}

func TestDeleteAsksForConfirmationFirst(t *testing.T) {
	m := listModel(t)
	var cmd tea.Cmd
	m, cmd = press(m, "d")
	if m.modal != modalDelete || len(m.confirm) != 1 {
		t.Fatalf("modal=%v confirm=%v, want the delete confirmation open", m.modal, m.confirm)
	}
	if cmd != nil {
		t.Fatal("nothing may be sent before the user confirms")
	}

	m, cmd = press(m, "n")
	if m.modal != modalNone || cmd != nil {
		t.Fatal("n must cancel without sending anything")
	}

	m, _ = press(m, "d")
	m, cmd = press(m, "y")
	if m.modal != modalNone || cmd == nil {
		t.Fatal("y must close the modal and send the delete")
	}
}

func TestHelpOverlayToggles(t *testing.T) {
	m := listModel(t)
	m, _ = press(m, "?")
	if m.modal != modalHelp {
		t.Fatal("? should open the help overlay")
	}
	m, _ = press(m, "esc")
	if m.modal != modalNone {
		t.Fatal("esc should close it")
	}
}

func TestRateStateComputesSmoothedSpeedAndSkipsNoisyReadings(t *testing.T) {
	var r rateState
	if r.observe(0, 0, t0) {
		t.Fatal("the first reading only sets a baseline")
	}
	if r.observe(1000, 0, t0.Add(100*time.Millisecond)) {
		t.Fatal("a reading only 100ms later is noise and must be skipped")
	}
	if !r.observe(2000, 500, t0.Add(time.Second)) || r.down != 2000 || r.up != 500 {
		t.Fatalf("first real sample: down=%v up=%v, want 2000 / 500", r.down, r.up)
	}
	r.observe(2000, 500, t0.Add(2*time.Second)) // an idle second
	if r.down != 1000 {
		t.Fatalf("smoothed down = %v, want 1000 (half of the previous 2000, new sample 0)", r.down)
	}
	r.observe(10, 10, t0.Add(3*time.Second)) // counters went backwards: engine restarted
	if r.hasRate {
		t.Fatal("a counter reset must start over, not report a negative rate")
	}
}

func TestSpeedHistoryIsCapped(t *testing.T) {
	var s speedHistory
	s.observe(0, 0, t0)
	for i := 1; i <= histLen+20; i++ {
		s.observe(int64(i)*1000, 0, t0.Add(time.Duration(i)*time.Second))
	}
	if len(s.downHist) != histLen {
		t.Fatalf("history length = %d, want %d", len(s.downHist), histLen)
	}
}

func TestFormatHelpers(t *testing.T) {
	bytes := []struct {
		n    int64
		want string
	}{{500, "500 B"}, {1500, "1.5 KB"}, {1_500_000, "1.5 MB"}}
	for _, c := range bytes {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	if got := humanRate(0); got != "-" {
		t.Errorf("humanRate(0) = %q, want a dash", got)
	}
	if got := humanRate(2_500_000); got != "2.5 MB/s" {
		t.Errorf("humanRate = %q", got)
	}
	etas := []struct {
		left int64
		bps  float64
		want string
	}{{0, 1000, "-"}, {1000, 0, "∞"}, {90_000, 1500, "1m 00s"}, {30_000, 10_000, "3s"}, {36_000_000, 10_000, "1h 00m"}}
	for _, c := range etas {
		if got := humanETA(c.left, c.bps); got != c.want {
			t.Errorf("humanETA(%d, %v) = %q, want %q", c.left, c.bps, got, c.want)
		}
	}
	if got := ago(t0.Add(-90*time.Second), t0); got != "1m ago" {
		t.Errorf("ago = %q", got)
	}
	if got := truncate("abcdefghij", 6); got != "abcde…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
}

func TestGradientBarAndSparklineHaveExactlyTheRequestedWidth(t *testing.T) {
	for _, frac := range []float64{-1, 0, 0.01, 0.37, 0.5, 0.999, 1, 2} {
		if w := lipgloss.Width(gradientBar(frac, 12, colCyan, colMagenta, nil)); w != 12 {
			t.Errorf("gradientBar(%v) width = %d, want 12", frac, w)
		}
	}
	for _, n := range []int{0, 3, 12, 40} {
		vals := make([]float64, n)
		for i := range vals {
			vals[i] = float64(i)
		}
		if w := lipgloss.Width(sparkline(vals, 12, colCyan, colMagenta)); w != 12 {
			t.Errorf("sparkline(%d values) width = %d, want 12", n, w)
		}
	}
}

func TestPieceMapIsExactlyWxHAndShadesPartialCells(t *testing.T) {
	// 100 pieces, the first 50 have, squeezed into a 5x2 map (20 half-cells).
	bits := make([]byte, 13)
	for i := 0; i < 50; i++ {
		bits[i/8] |= 0x80 >> (uint(i) % 8)
	}
	p := tuiclient.PiecesResponse{NumPieces: 100, HaveCount: 50, Bitfield: bits}
	lines, per := pieceMap(p, 5, 2)
	if len(lines) != 2 {
		t.Fatalf("rows = %d, want 2", len(lines))
	}
	if per != 5 {
		t.Fatalf("pieces per cell = %d, want 5 (100 pieces into 20 half-cells)", per)
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w != 5 {
			t.Fatalf("row width = %d, want 5", w)
		}
	}

	// Few pieces are drawn as big blocks, one per piece, filling the area
	// rather than huddling in a corner - and never overflowing it.
	small := tuiclient.PiecesResponse{NumPieces: 12, Bitfield: []byte{0xFF, 0x00}}
	lines, per = pieceMap(small, 40, 8)
	if per != 1 {
		t.Fatalf("small torrent: pieces per cell = %d, want 1 (a block per piece)", per)
	}
	if len(lines) < 4 || len(lines) > 8 {
		t.Fatalf("small torrent: %d rows, want the blocks scaled up to use the area (4-8)", len(lines))
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w != 40 {
			t.Fatalf("small map row width = %d, want 40", w)
		}
	}
}

func TestListColumnsKeepTheNameReadableByDroppingOptionalOnes(t *testing.T) {
	wide := listColumns(140)
	narrow := listColumns(70)
	if len(narrow) >= len(wide) {
		t.Fatalf("narrow layout kept %d columns vs %d wide, want fewer", len(narrow), len(wide))
	}
	for _, cols := range [][]column{wide, narrow} {
		for _, c := range cols {
			if c.id == "name" && c.w < 6 {
				t.Fatalf("name column collapsed to %d cells", c.w)
			}
		}
	}
}

// Every screen, at several terminal sizes, must render without panicking and
// without ever exceeding the terminal: a line wider than the window wraps and
// wrecks the whole layout, and a frame taller than the window scrolls the
// header off the top.
func TestEveryScreenFitsTheTerminal(t *testing.T) {
	sizes := [][2]int{{minWidth, minHeight}, {100, 30}, {120, 40}, {200, 60}}

	build := func(w, h int, configure func(*Model)) Model {
		m := listModel(t)
		m.width, m.height = w, h
		m.session = tuiclient.SessionStats{TorrentCount: 4, TotalPeerCount: 9, DHTRunning: true, DHTNodeCount: 312, ListenPort: 6881, FreeDiskBytes: 1e11}
		m.speed.downHist = []float64{1, 5, 9, 3}
		m.speed.upHist = []float64{0, 2, 1, 4}
		m.wsConnected = true
		m.pushToast(toastOK, "Paused 2 torrents")
		m.detailHash = "aaa"
		m.detail = tuiclient.TorrentDetail{TorrentSummary: sampleList()[0], DownloadDir: "/downloads", Source: "file", PieceLength: 262144}
		m.files = []tuiclient.FileEntry{{Path: []string{"dir", "a.bin"}, Length: 500, Priority: "normal"}, {Path: []string{"b.bin"}, Length: 100, Priority: "skip"}}
		m.peers = []tuiclient.PeerEntry{{Addr: "127.0.0.1:6881", Outbound: true, Progress: 0.5, PeerID: "2d475430313030" + "2d" + "6161616161616161616161616161"}}
		m.trackers = []tuiclient.TrackerEntry{{URL: "http://tracker.example/announce", Seeders: 3, Leechers: 4, LastAnnounce: t0}, {URL: "udp://x", LastError: "timeout"}}
		m.pieces = tuiclient.PiecesResponse{NumPieces: 64, HaveCount: 32, Bitfield: []byte{0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0}}
		m.detailAt = t0
		if configure != nil {
			configure(&m)
		}
		return m
	}

	screens := map[string]func(*Model){
		"connect": func(m *Model) { m.screen = screenConnect; m.err = "connect: connection refused" },
		"list":    func(m *Model) {},
		"list+marks+search": func(m *Model) {
			m.marked["aaa"] = true
			m.query = "a"
			m.applyView()
		},
		"list sidebar focus": func(m *Model) { m.focus = focusSidebar },
		"list empty":         func(m *Model) { m.torrents = nil; m.applyView() },
		"add":                func(m *Model) { m.screen = screenAdd },
		"help":               func(m *Model) { m.modal = modalHelp },
		"delete":             func(m *Model) { m.modal = modalDelete; m.confirm = []string{"aaa", "bbb"} },
		"error banner":       func(m *Model) { m.err = "list: connection refused" },
	}
	for tab := tabOverview; tab < tabCount; tab++ {
		tab := tab
		screens["detail/"+tabNames[tab]] = func(m *Model) { m.screen = screenDetail; m.activeTab = tab }
	}
	screens["detail loading"] = func(m *Model) { m.screen = screenDetail; m.detailLoading = true }

	for name, configure := range screens {
		for _, sz := range sizes {
			w, h := sz[0], sz[1]
			out := build(w, h, configure).View()
			lines := strings.Split(out, "\n")
			if len(lines) > h {
				t.Errorf("%s at %dx%d: frame is %d lines tall, terminal has %d", name, w, h, len(lines), h)
			}
			for i, l := range lines {
				if lw := lipgloss.Width(l); lw > w {
					t.Errorf("%s at %dx%d: line %d is %d cells wide, terminal has %d", name, w, h, i, lw, w)
					break
				}
			}
		}
	}
}

func TestViewBeforeTheFirstWindowSizeDoesNotPanic(t *testing.T) {
	m := newTestModel() // width/height still zero
	if out := m.View(); out == "" {
		t.Fatal("want the connect screen, got nothing")
	}
}

func TestClientNameDecodesAzureusStylePeerIDs(t *testing.T) {
	// "-GT0100-" followed by 12 arbitrary bytes.
	if got := clientName("2d47543031303" + "02d" + "000000000000000000000000"); got != "GoTorrent" {
		t.Errorf("clientName = %q, want GoTorrent", got)
	}
	if clientName("zz") != "" || clientName("") != "" {
		t.Error("garbage must decode to no name")
	}
}
