package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

// The palette is package state, so every test that changes it puts the
// default back.
func restoreTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { applyTheme(themes[0]) })
}

func TestThemesAreWellFormed(t *testing.T) {
	if len(themes) < 6 {
		t.Fatalf("only %d themes, want at least 6", len(themes))
	}
	seen := map[string]bool{}
	for _, th := range themes {
		if th.Name == "" || seen[slug(th.Name)] {
			t.Errorf("theme name %q is empty or duplicated", th.Name)
		}
		seen[slug(th.Name)] = true

		// Text, dim and faint must be distinguishable from one another and
		// the faintest must still differ from the bar track, or hierarchy
		// and empty bars vanish.
		if th.Text == th.Dim || th.Dim == th.Faint || th.Faint == th.Track {
			t.Errorf("%s: text/dim/faint/track collapse into each other", th.Name)
		}
		if th.Primary == th.Secondary && th.Name != "Monochrome" && th.BarFrom == th.BarTo && th.Name != "Desktop" && th.Name != "Desktop Light" {
			t.Errorf("%s: primary and secondary accents are identical", th.Name)
		}
		if th.OnPrimary == th.Primary {
			t.Errorf("%s: text on the primary colour is the primary colour", th.Name)
		}
	}
}

func TestThemeLookupIgnoresCaseSpacesAndDashes(t *testing.T) {
	for _, name := range []string{"Tokyo Night", "tokyo-night", "TOKYONIGHT", "tokyo_night"} {
		i, ok := themeIndex(name)
		if !ok || themes[i].Name != "Tokyo Night" {
			t.Errorf("themeIndex(%q) = %d,%v, want Tokyo Night", name, i, ok)
		}
	}
	if KnownTheme("no such theme") {
		t.Error("an unknown name must not match")
	}
}

func TestThemeKeysCycleForwardBackAndWrap(t *testing.T) {
	restoreTheme(t)
	m := listModel(t)
	if m.themeIdx != 0 {
		t.Fatalf("starts on theme %d, want 0", m.themeIdx)
	}

	m, _ = press(m, "t")
	if m.themeIdx != 1 || cPrimary != themes[1].Primary {
		t.Fatalf("after t: idx=%d, palette primary %v, want theme 1 applied", m.themeIdx, cPrimary)
	}
	if len(m.toasts) == 0 || !strings.Contains(m.toasts[len(m.toasts)-1].text, themes[1].Name) {
		t.Fatalf("toasts = %+v, want one naming %s", m.toasts, themes[1].Name)
	}

	m, _ = press(m, "T", "T")
	if m.themeIdx != len(themes)-1 {
		t.Fatalf("T from the first theme should wrap to the last, got %d", m.themeIdx)
	}
	m, _ = press(m, "t")
	if m.themeIdx != 0 {
		t.Fatalf("t from the last theme should wrap to the first, got %d", m.themeIdx)
	}
}

func TestCtrlTCyclesThemesOnTheConnectScreenEvenWhileTyping(t *testing.T) {
	restoreTheme(t)
	m := newTestModel()
	m, _ = press(m, "ctrl+t")
	if m.themeIdx != 1 {
		t.Fatalf("ctrl+t on the connect screen: idx=%d, want 1", m.themeIdx)
	}
	if m.addrInput.Value() != "" {
		t.Fatal("ctrl+t must not type into the focused field")
	}
}

func TestChangingThemeRestylesTheTextInputs(t *testing.T) {
	restoreTheme(t)
	m := newTestModel()
	before := m.addrInput.Cursor.Style.GetForeground()
	m, _ = press(m, "ctrl+t")
	if m.addrInput.Cursor.Style.GetForeground() == before {
		t.Fatal("the input cursor keeps the old theme's colour after a theme change")
	}
}

func TestEveryThemeRendersEveryScreenWithinTheTerminal(t *testing.T) {
	restoreTheme(t)
	const w, h = 100, 30
	for i, th := range themes {
		applyTheme(th)
		m := listModel(t)
		m.themeIdx = i
		m.width, m.height = w, h
		m.session = tuiclient.SessionStats{TorrentCount: 4, DHTRunning: true, DHTNodeCount: 10, ListenPort: 6881}
		m.speed.downHist = []float64{1, 4, 2, 8}
		m.wsConnected = true
		m.detailHash = "aaa"
		m.detail = tuiclient.TorrentDetail{TorrentSummary: sampleList()[0]}
		m.pieces = tuiclient.PiecesResponse{NumPieces: 40, HaveCount: 20, Bitfield: []byte{0xFF, 0xFF, 0x00, 0x00, 0x00}}
		m.pushToast(toastErr, "something failed")

		screens := map[string]func(*Model){
			"connect": func(m *Model) { m.screen = screenConnect },
			"list":    func(m *Model) {},
			"help":    func(m *Model) { m.modal = modalHelp },
			"delete":  func(m *Model) { m.modal = modalDelete; m.confirm = []string{"aaa"} },
		}
		for tab := tabOverview; tab < tabCount; tab++ {
			tab := tab
			screens["detail/"+tabNames[tab]] = func(m *Model) { m.screen = screenDetail; m.activeTab = tab }
		}
		for name, configure := range screens {
			mm := m
			configure(&mm)
			lines := strings.Split(mm.View(), "\n")
			if len(lines) > h {
				t.Errorf("%s/%s: %d lines, terminal has %d", th.Name, name, len(lines), h)
			}
			for n, l := range lines {
				if lw := lipgloss.Width(l); lw > w {
					t.Errorf("%s/%s: line %d is %d cells wide, terminal has %d", th.Name, name, n, lw, w)
					break
				}
			}
		}
	}
}

func TestSavedThemeRoundTripsAndIgnoresGarbage(t *testing.T) {
	old := themeFile
	path := filepath.Join(t.TempDir(), "sub", "tui-theme")
	themeFile = func() string { return path }
	t.Cleanup(func() { themeFile = old })

	if got := SavedTheme(); got != "" {
		t.Fatalf("SavedTheme() = %q before anything is saved, want empty", got)
	}
	saveThemeCmd("Nord")()
	if got := SavedTheme(); got != "Nord" {
		t.Fatalf("SavedTheme() = %q, want Nord", got)
	}
	if err := os.WriteFile(path, []byte("not-a-theme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := SavedTheme(); got != "" {
		t.Fatalf("SavedTheme() = %q for a garbage file, want empty", got)
	}
}
