package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Theme is a complete palette. Views never name a colour; they use the
// semantic roles applyTheme fills in, so adding a theme is adding one entry
// to the table below. No theme paints a background: they all draw on the
// terminal's own, which is why a couple of them (the Desktop Light one) are
// only meant for light terminals.
type Theme struct {
	Name string

	Primary, Secondary, Tertiary rgb
	Good, Warn, Bad, Info        rgb

	Text, Dim, Faint, Border, Track, SelBg rgb
	OnPrimary                              rgb

	BrandFrom, BrandTo rgb // logo, titles, piece map
	BarFrom, BarTo     rgb // a downloading progress bar
	SeedFrom, SeedTo   rgb // a seeding progress bar
}

func hx(s string) rgb {
	var r, g, b uint8
	if _, err := fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &r, &g, &b); err != nil {
		panic("tui: bad colour " + s)
	}
	return rgb{r, g, b}
}

var dark = hx("#0b0e14") // text drawn on a bright accent background

var themes = []Theme{
	{
		Name:    "Neon",
		Primary: hx("#00e5ff"), Secondary: hx("#ff2bd6"), Tertiary: hx("#b48eff"),
		Good: hx("#3ddc97"), Warn: hx("#ffb454"), Bad: hx("#ff5370"), Info: hx("#6ea8fe"),
		Text: hx("#e6edf3"), Dim: hx("#8b949e"), Faint: hx("#5a626c"), Border: hx("#30363d"), Track: hx("#2b313a"), SelBg: hx("#1b263b"),
		OnPrimary: dark,
		BrandFrom: hx("#00e5ff"), BrandTo: hx("#ff2bd6"),
		BarFrom: hx("#00e5ff"), BarTo: hx("#ff2bd6"),
		SeedFrom: hx("#3ddc97"), SeedTo: hx("#00e5ff"),
	},
	{
		// Matches the desktop app's dark palette token for token.
		Name:    "Desktop",
		Primary: hx("#00c6ff"), Secondary: hx("#ff7043"), Tertiary: hx("#c792ea"),
		Good: hx("#3dd68c"), Warn: hx("#ffb74d"), Bad: hx("#f44747"), Info: hx("#4ea1f3"),
		Text: hx("#e8e9ed"), Dim: hx("#8b8fa3"), Faint: hx("#5c6070"), Border: hx("#404040"), Track: hx("#2a2e38"), SelBg: hx("#2a2e38"),
		OnPrimary: dark,
		BrandFrom: hx("#00c6ff"), BrandTo: hx("#33d1ff"),
		BarFrom: hx("#00c6ff"), BarTo: hx("#00c6ff"),
		SeedFrom: hx("#3dd68c"), SeedTo: hx("#3dd68c"),
	},
	{
		Name:    "Tokyo Night",
		Primary: hx("#7aa2f7"), Secondary: hx("#bb9af7"), Tertiary: hx("#7dcfff"),
		Good: hx("#9ece6a"), Warn: hx("#e0af68"), Bad: hx("#f7768e"), Info: hx("#2ac3de"),
		Text: hx("#c0caf5"), Dim: hx("#787c99"), Faint: hx("#565f89"), Border: hx("#3b4261"), Track: hx("#292e42"), SelBg: hx("#283457"),
		OnPrimary: hx("#1a1b26"),
		BrandFrom: hx("#7aa2f7"), BrandTo: hx("#bb9af7"),
		BarFrom: hx("#7aa2f7"), BarTo: hx("#bb9af7"),
		SeedFrom: hx("#9ece6a"), SeedTo: hx("#7dcfff"),
	},
	{
		Name:    "Catppuccin",
		Primary: hx("#cba6f7"), Secondary: hx("#f5c2e7"), Tertiary: hx("#89dceb"),
		Good: hx("#a6e3a1"), Warn: hx("#fab387"), Bad: hx("#f38ba8"), Info: hx("#89b4fa"),
		Text: hx("#cdd6f4"), Dim: hx("#9399b2"), Faint: hx("#6c7086"), Border: hx("#45475a"), Track: hx("#313244"), SelBg: hx("#363a4f"),
		OnPrimary: hx("#1e1e2e"),
		BrandFrom: hx("#cba6f7"), BrandTo: hx("#f5c2e7"),
		BarFrom: hx("#89b4fa"), BarTo: hx("#cba6f7"),
		SeedFrom: hx("#a6e3a1"), SeedTo: hx("#94e2d5"),
	},
	{
		Name:    "Dracula",
		Primary: hx("#bd93f9"), Secondary: hx("#ff79c6"), Tertiary: hx("#8be9fd"),
		Good: hx("#50fa7b"), Warn: hx("#ffb86c"), Bad: hx("#ff5555"), Info: hx("#8be9fd"),
		Text: hx("#f8f8f2"), Dim: hx("#9aa0c0"), Faint: hx("#6272a4"), Border: hx("#44475a"), Track: hx("#383a4c"), SelBg: hx("#44475a"),
		OnPrimary: hx("#282a36"),
		BrandFrom: hx("#bd93f9"), BrandTo: hx("#ff79c6"),
		BarFrom: hx("#bd93f9"), BarTo: hx("#ff79c6"),
		SeedFrom: hx("#50fa7b"), SeedTo: hx("#8be9fd"),
	},
	{
		Name:    "Gruvbox",
		Primary: hx("#fabd2f"), Secondary: hx("#fe8019"), Tertiary: hx("#d3869b"),
		Good: hx("#b8bb26"), Warn: hx("#fabd2f"), Bad: hx("#fb4934"), Info: hx("#83a598"),
		Text: hx("#ebdbb2"), Dim: hx("#a89984"), Faint: hx("#7c6f64"), Border: hx("#504945"), Track: hx("#3c3836"), SelBg: hx("#504945"),
		OnPrimary: hx("#282828"),
		BrandFrom: hx("#fabd2f"), BrandTo: hx("#fe8019"),
		BarFrom: hx("#fabd2f"), BarTo: hx("#fe8019"),
		SeedFrom: hx("#b8bb26"), SeedTo: hx("#8ec07c"),
	},
	{
		Name:    "Nord",
		Primary: hx("#88c0d0"), Secondary: hx("#b48ead"), Tertiary: hx("#81a1c1"),
		Good: hx("#a3be8c"), Warn: hx("#ebcb8b"), Bad: hx("#bf616a"), Info: hx("#5e81ac"),
		Text: hx("#eceff4"), Dim: hx("#9aa5b8"), Faint: hx("#616e88"), Border: hx("#434c5e"), Track: hx("#3b4252"), SelBg: hx("#434c5e"),
		OnPrimary: hx("#2e3440"),
		BrandFrom: hx("#88c0d0"), BrandTo: hx("#b48ead"),
		BarFrom: hx("#88c0d0"), BarTo: hx("#81a1c1"),
		SeedFrom: hx("#a3be8c"), SeedTo: hx("#88c0d0"),
	},
	{
		// A warm amber-phosphor terminal: every role is a shade of amber.
		Name:    "Amber",
		Primary: hx("#ffb000"), Secondary: hx("#ff8c00"), Tertiary: hx("#ffd36e"),
		Good: hx("#ffd45e"), Warn: hx("#ff9a1f"), Bad: hx("#ff5a36"), Info: hx("#e8a100"),
		Text: hx("#ffcc66"), Dim: hx("#b37f2a"), Faint: hx("#7a5516"), Border: hx("#4d3410"), Track: hx("#2e1f0a"), SelBg: hx("#3a2607"),
		OnPrimary: hx("#1a1000"),
		BrandFrom: hx("#ffb000"), BrandTo: hx("#ff6a00"),
		BarFrom: hx("#ffb000"), BarTo: hx("#ff6a00"),
		SeedFrom: hx("#ffd45e"), SeedTo: hx("#ffb000"),
	},
	{
		// Green-on-black, like a screen from a certain film.
		Name:    "Matrix",
		Primary: hx("#00ff41"), Secondary: hx("#7dff9f"), Tertiary: hx("#00d68a"),
		Good: hx("#00ff41"), Warn: hx("#d6ff00"), Bad: hx("#ff3131"), Info: hx("#00e0a0"),
		Text: hx("#b8ffc8"), Dim: hx("#3fa860"), Faint: hx("#24703d"), Border: hx("#12401f"), Track: hx("#0c2a15"), SelBg: hx("#0f3a1c"),
		OnPrimary: hx("#00140a"),
		BrandFrom: hx("#00ff41"), BrandTo: hx("#00a82a"),
		BarFrom: hx("#00a82a"), BarTo: hx("#00ff41"),
		SeedFrom: hx("#00ff41"), SeedTo: hx("#7dff9f"),
	},
	{
		// Shades of grey only: state is told apart by glyph and brightness.
		Name:    "Monochrome",
		Primary: hx("#ffffff"), Secondary: hx("#c8c8c8"), Tertiary: hx("#a0a0a0"),
		Good: hx("#e0e0e0"), Warn: hx("#b0b0b0"), Bad: hx("#ffffff"), Info: hx("#b8b8b8"),
		Text: hx("#dcdcdc"), Dim: hx("#8a8a8a"), Faint: hx("#5a5a5a"), Border: hx("#3c3c3c"), Track: hx("#2a2a2a"), SelBg: hx("#333333"),
		OnPrimary: hx("#000000"),
		BrandFrom: hx("#ffffff"), BrandTo: hx("#8a8a8a"),
		BarFrom: hx("#8a8a8a"), BarTo: hx("#ffffff"),
		SeedFrom: hx("#b0b0b0"), SeedTo: hx("#ffffff"),
	},
	{
		// The desktop app's light palette. Only for light terminals, which is
		// why it sits last in the cycle.
		Name:    "Desktop Light",
		Primary: hx("#0090c0"), Secondary: hx("#e64a19"), Tertiary: hx("#7b4dbd"),
		Good: hx("#2e7d32"), Warn: hx("#f57c00"), Bad: hx("#d32f2f"), Info: hx("#1565c0"),
		Text: hx("#1a1a1a"), Dim: hx("#5f6368"), Faint: hx("#9aa0a6"), Border: hx("#d5d8dc"), Track: hx("#dfe2e6"), SelBg: hx("#dcebf3"),
		OnPrimary: hx("#ffffff"),
		BrandFrom: hx("#0090c0"), BrandTo: hx("#1ba9d9"),
		BarFrom: hx("#0090c0"), BarTo: hx("#0090c0"),
		SeedFrom: hx("#2e7d32"), SeedTo: hx("#2e7d32"),
	},
}

// applyTheme loads a theme into the live palette. The palette is package
// state because every view reads it; a terminal program only ever has one
// theme at a time, so threading it through every render call would add
// noise without adding any real safety.
func applyTheme(t Theme) {
	cPrimary, cSecondary, cTertiary = t.Primary, t.Secondary, t.Tertiary
	cGood, cWarn, cBad, cInfo = t.Good, t.Warn, t.Bad, t.Info
	cText, cDim, cFaint, cBorder, cTrack, cSelBg = t.Text, t.Dim, t.Faint, t.Border, t.Track, t.SelBg
	cOnPrimary = t.OnPrimary
	brandFrom, brandTo = t.BrandFrom, t.BrandTo
	barFrom, barTo = t.BarFrom, t.BarTo
	seedFrom, seedTo = t.SeedFrom, t.SeedTo
	rebuildStyles()
}

func init() { applyTheme(themes[0]) }

// ThemeNames lists the available theme names in cycling order.
func ThemeNames() []string {
	out := make([]string, len(themes))
	for i, t := range themes {
		out[i] = t.Name
	}
	return out
}

func slug(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s))
}

// themeIndex finds a theme by name, ignoring case, spaces and dashes
// ("tokyo-night", "Tokyo Night" and "tokyonight" all match).
func themeIndex(name string) (int, bool) {
	want := slug(name)
	for i, t := range themes {
		if slug(t.Name) == want {
			return i, true
		}
	}
	return 0, false
}

// KnownTheme reports whether name matches an available theme.
func KnownTheme(name string) bool {
	_, ok := themeIndex(name)
	return ok
}

// themeFile is where the chosen theme is remembered between runs; a var so
// tests can point it at a temporary directory.
var themeFile = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "GoTorrent", "tui-theme")
}

// SavedTheme returns the theme name remembered from a previous run, if any.
func SavedTheme() string {
	path := themeFile()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if _, ok := themeIndex(name); !ok {
		return ""
	}
	return name
}

// saveThemeCmd remembers the chosen theme. Best effort: failing to write a
// preference file is never worth interrupting the program for.
func saveThemeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if path := themeFile(); path != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				_ = os.WriteFile(path, []byte(name+"\n"), 0o600)
			}
		}
		return nil
	}
}

// cycleTheme moves delta steps through the themes (wrapping), applies the
// result, and returns the toast and save commands.
func (m *Model) cycleTheme(delta int) tea.Cmd {
	m.themeIdx = ((m.themeIdx+delta)%len(themes) + len(themes)) % len(themes)
	t := themes[m.themeIdx]
	applyTheme(t)
	m.restyleInputs()
	return tea.Batch(
		m.pushToast(toastInfo, fmt.Sprintf("Theme: %s (%d/%d)", t.Name, m.themeIdx+1, len(themes))),
		saveThemeCmd(t.Name),
	)
}
