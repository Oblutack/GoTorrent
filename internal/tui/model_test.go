package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

// Update never performs I/O itself (see its own doc comment), so every
// test here drives it with synthetic messages - no real client, WS
// connection, or terminal needed, the same "test the state machine
// directly" discipline Desktop's own ViewModel tests already established
// for this project's C# side.

func newTestModel() Model {
	return New(context.Background(), "", "")
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

func TestTorrentsMsgPopulatesTheTable(t *testing.T) {
	m := newTestModel()
	list := []tuiclient.TorrentSummary{
		{InfoHash: "aaa", Name: "one", State: "Downloading", TotalLength: 100, Left: 50},
		{InfoHash: "bbb", Name: "two", State: "Seeding", TotalLength: 100, Left: 0},
	}
	updated, _ := m.Update(torrentsMsg{list: list})
	m = updated.(Model)

	if len(m.torrents) != 2 {
		t.Fatalf("len(torrents) = %d, want 2", len(m.torrents))
	}
	if len(m.table.Rows()) != 2 {
		t.Fatalf("len(table rows) = %d, want 2", len(m.table.Rows()))
	}
}

func TestEnterOnListOpensDetailAndFetchesIt(t *testing.T) {
	m := newTestModel()
	m.client = tuiclient.New("http://example.invalid", "")
	m.torrents = []tuiclient.TorrentSummary{{InfoHash: "aaa", Name: "one"}}
	m.table.SetRows(torrentRows(m.torrents))
	m.screen = screenList

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.screen != screenDetail {
		t.Fatalf("screen = %v, want screenDetail", m.screen)
	}
	if m.detailHash != "aaa" {
		t.Fatalf("detailHash = %q, want aaa", m.detailHash)
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
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList", m.screen)
	}
}

func TestTabOnDetailCyclesThroughAllThreeTabs(t *testing.T) {
	m := newTestModel()
	m.screen = screenDetail
	m.activeTab = tabFiles

	for _, want := range []detailTab{tabPeers, tabTrackers, tabFiles} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(Model)
		if m.activeTab != want {
			t.Fatalf("activeTab = %v, want %v", m.activeTab, want)
		}
	}
}

func TestWSEventTorrentStateChangedTriggersARefetch(t *testing.T) {
	m := newTestModel()
	m.client = tuiclient.New("http://example.invalid", "")
	m.wsEvents = make(chan tuiclient.WSEvent) // never closed/sent to in this test - only handleWSEvent's own return value matters

	_, cmd := m.Update(wsEventMsg{event: tuiclient.WSEvent{Kind: "torrentStateChanged", InfoHash: "aaa"}})
	if cmd == nil {
		t.Fatal("want a batched refetch+waitForEvent command, got nil")
	}
}

func TestWSEventSessionStatsUpdatesSessionDirectly(t *testing.T) {
	m := newTestModel()
	m.wsEvents = make(chan tuiclient.WSEvent)
	stats := tuiclient.SessionStats{TorrentCount: 5, TotalDownloaded: 12345}

	updated, _ := m.Update(wsEventMsg{event: tuiclient.WSEvent{Kind: "sessionStats", Session: &stats}})
	m = updated.(Model)

	if m.session.TorrentCount != 5 || m.session.TotalDownloaded != 12345 {
		t.Fatalf("session = %+v, want the event's own stats applied directly", m.session)
	}
}

func TestCtrlCQuits(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("want tea.Quit, got nil")
	}
	// tea.Quit is a real Cmd that returns a tea.QuitMsg when invoked -
	// confirm this is genuinely it, not just "some non-nil command".
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c did not return tea.Quit")
	}
}

func TestActionResultErrorSetsErrAndDoesNotRefetch(t *testing.T) {
	m := newTestModel()
	m.client = tuiclient.New("http://example.invalid", "")

	updated, cmd := m.Update(actionResultMsg{action: "pause", err: errors.New("not managed by this engine")})
	m = updated.(Model)

	if m.err == "" {
		t.Fatal("want a non-empty error message")
	}
	if cmd != nil {
		t.Fatal("want no refetch command on a failed action")
	}
}

func TestActionResultSuccessClearsErrAndRefetches(t *testing.T) {
	m := newTestModel()
	m.client = tuiclient.New("http://example.invalid", "")
	m.err = "stale error from before"

	updated, cmd := m.Update(actionResultMsg{action: "pause"})
	m = updated.(Model)

	if m.err != "" {
		t.Fatalf("err = %q, want cleared on success", m.err)
	}
	if cmd == nil {
		t.Fatal("want a refetch command after a successful action")
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

func TestProgressBarClampsAndFormats(t *testing.T) {
	cases := []struct {
		frac float64
		want string
	}{
		{0, "......   0%"},
		{1, "###### 100%"},
		{0.5, "###...  50%"},
		{-1, "......   0%"},
		{2, "###### 100%"},
	}
	for _, c := range cases {
		if got := progressBar(c.frac); got != c.want {
			t.Errorf("progressBar(%v) = %q, want %q", c.frac, got, c.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{500, "500 B"},
		{1500, "1.5 KB"},
		{1_500_000, "1.5 MB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
