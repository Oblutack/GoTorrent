package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The smallest terminal every screen is laid out for.
const (
	minWidth  = 80
	minHeight = 24
)

// View satisfies tea.Model.
func (m Model) View() string {
	w, h := m.size()
	if w < minWidth || h < minHeight {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center,
			styleDim.Render(fmt.Sprintf("terminal too small (%dx%d)\nneeds at least %dx%d", w, h, minWidth, minHeight)))
	}

	switch m.screen {
	case screenConnect:
		return m.viewConnect(w, h)
	case screenAdd:
		return m.viewAdd(w, h)
	}

	header := m.viewHeader(w)
	footer := m.viewFooter(w)
	bodyH := h - lipgloss.Height(header) - lipgloss.Height(footer)

	var body string
	switch {
	case m.modal == modalHelp:
		body = lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center, m.viewHelp())
	case m.modal == modalDelete:
		body = lipgloss.Place(w, bodyH, lipgloss.Center, lipgloss.Center, m.viewDeleteConfirm())
	case m.screen == screenDetail:
		body = m.viewDetail(w, bodyH)
	default:
		body = m.viewList(w, bodyH)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// logoRows is "GoTorrent" in a small box-drawing face, three rows tall.
var logoRows = [3]string{
	"╔═╗┌─┐╔╦╗┌─┐┬─┐┬─┐┌─┐┌┐┌┌┬┐",
	"║ ╦│ │ ║ │ │├┬┘├┬┘├┤ │││ │ ",
	"╚═╝└─┘ ╩ └─┘┴└─┴└─└─┘┘└┘ ┴ ",
}

func (m Model) viewConnect(w, h int) string {
	var b strings.Builder
	for _, row := range logoRows {
		b.WriteString(gradientText(row, colCyan, colMagenta, true) + "\n")
	}
	b.WriteString("\n" + styleDim.Render("a terminal client for gottrentd") + "\n\n")

	field := func(label string, focused bool, view string) string {
		border := colBorder
		if focused {
			border = colCyan
		}
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border.color()).Padding(0, 1).Width(44).Render(view)
		return styleLabel.Render(label) + "\n" + box + "\n"
	}
	b.WriteString(field("daemon address", m.addrInput.Focused(), m.addrInput.View()))
	b.WriteString(field("bearer token", m.tokenInput.Focused(), m.tokenInput.View()))

	switch {
	case m.connecting:
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(colAmber.color()).Render("◐ connecting…"))
	case m.err != "":
		b.WriteString("\n" + styleErr.Render("✗ "+m.err))
	default:
		b.WriteString("\n" + styleFaint.Render(" "))
	}
	b.WriteString("\n\n" + hintLine([]hint{{"tab", "switch field"}, {"enter", "connect"}, {"ctrl+c", "quit"}}, 60))

	card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder.color()).Padding(1, 3).Render(b.String())
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
}

func (m Model) viewAdd(w, h int) string {
	var b strings.Builder
	b.WriteString(gradientText("Add torrent", colCyan, colMagenta, true) + "\n\n")
	b.WriteString(styleLabel.Render("magnet link, .torrent URL, or a local file path") + "\n")
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colCyan.color()).Padding(0, 1).Width(64).Render(m.addInput.View())
	b.WriteString(box + "\n")
	b.WriteString("\n" + hintLine([]hint{{"enter", "add"}, {"esc", "cancel"}}, 60))
	card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder.color()).Padding(1, 3).Render(b.String())

	body := lipgloss.Place(w, h-footerH, lipgloss.Center, lipgloss.Center, card)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.viewFooter(w))
}

// viewHeader is the bordered top bar: logo and connection pills on the first
// line, live speeds with sparklines and totals on the second.
func (m Model) viewHeader(w int) string {
	inner := w - 4

	logo := gradientText("◆ GoTorrent", colCyan, colMagenta, true)

	var pills []string
	if m.wsConnected {
		pills = append(pills, pill("● live", colGreen))
	} else {
		pills = append(pills, pill("○ polling", colAmber))
	}
	if m.session.DHTRunning {
		pills = append(pills, pill(fmt.Sprintf("DHT %d", m.session.DHTNodeCount), colPurple))
	}
	if m.session.PortMapped {
		pills = append(pills, pill(fmt.Sprintf("port %d", m.session.ExternalPort), colBlue))
	} else if m.session.ListenPort != 0 {
		pills = append(pills, pill(fmt.Sprintf("port %d", m.session.ListenPort), colDim))
	}
	if m.session.FreeDiskBytes > 0 {
		pills = append(pills, pill("disk "+humanBytes(m.session.FreeDiskBytes), colDim))
	}
	right := strings.Join(pills, " ")
	for lipgloss.Width(logo)+2+lipgloss.Width(right) > inner && len(pills) > 1 {
		pills = pills[:len(pills)-1]
		right = strings.Join(pills, " ")
	}
	line1 := logo + strings.Repeat(" ", max(1, inner-lipgloss.Width(logo)-lipgloss.Width(right))) + right

	sparkW := 20
	if w < 100 {
		sparkW = 10
	}
	down := fmt.Sprintf("%s %s %s",
		fg(colCyan, false).Bold(true).Render("↓ "+padRight(humanRateOrZero(m.speed.down), 10)),
		sparkline(m.speed.downHist, sparkW, colCyan, colBlue), "")
	up := fmt.Sprintf("%s %s",
		fg(colMagenta, false).Bold(true).Render("↑ "+padRight(humanRateOrZero(m.speed.up), 10)),
		sparkline(m.speed.upHist, sparkW, colMagenta, colPurple))
	totals := styleDim.Render(fmt.Sprintf("%d torrents · %d peers · ↓ %s ↑ %s",
		m.session.TorrentCount, m.session.TotalPeerCount,
		humanBytes(m.session.TotalDownloaded), humanBytes(m.session.TotalUploaded)))
	line2 := strings.TrimRight(down, " ") + "   " + up + "   " + totals
	line2 = truncateANSI(line2, inner)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder.color()).
		Padding(0, 1).
		Width(w - 2).
		Render(line1 + "\n" + line2)
}

func humanRateOrZero(bps float64) string {
	if bps < 1 {
		return "0 B/s"
	}
	return humanRate(bps)
}

// truncateANSI cuts a styled string to w display cells.
func truncateANSI(s string, w int) string {
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// hint is one "key  description" entry of the footer key legend.
type hint struct{ key, desc string }

// hintLine renders as many hints as fit in w cells.
func hintLine(hints []hint, w int) string {
	var b strings.Builder
	used := 0
	for i, h := range hints {
		part := styleKey.Render(h.key) + " " + styleDim.Render(h.desc)
		sep := "  "
		if i == 0 {
			sep = ""
		}
		if used+lipgloss.Width(sep)+lipgloss.Width(part) > w {
			break
		}
		b.WriteString(sep + part)
		used += lipgloss.Width(sep) + lipgloss.Width(part)
	}
	return b.String()
}

func (m Model) listHints() []hint {
	return []hint{
		{"↑↓", "move"}, {"←→", "pane"}, {"enter", "open"}, {"space", "mark"}, {"/", "search"}, {"s", "sort"},
		{"a", "add"}, {"p", "pause"}, {"r", "resume"}, {"d", "remove"}, {"?", "help"}, {"q", "quit"},
	}
}

func (m Model) detailHints() []hint {
	return []hint{
		{"tab", "next tab"}, {"1-5", "jump"}, {"↑↓", "scroll"}, {"p", "pause"}, {"r", "resume"},
		{"v", "verify"}, {"n", "reannounce"}, {"d", "remove"}, {"esc", "back"}, {"?", "help"},
	}
}

// viewFooter is two rows: status/search/toast, then the key legend.
func (m Model) viewFooter(w int) string {
	var left string
	switch {
	case m.searching:
		left = styleKey.Render("/ ") + m.searchInput.View()
	case m.err != "":
		left = styleErr.Render("⚠ " + truncate(m.err, w/2))
	default:
		var chips []string
		if m.query != "" {
			chips = append(chips, pill("filter: "+truncate(m.query, 20), colCyan))
		}
		if n := len(m.marked); n > 0 {
			chips = append(chips, pill(fmt.Sprintf("%d selected", n), colMagenta))
		}
		left = strings.Join(chips, " ")
	}

	var right string
	if n := len(m.toasts); n > 0 {
		t := m.toasts[n-1]
		glyph, c := "●", colBlue
		switch t.kind {
		case toastOK:
			glyph, c = "✓", colGreen
		case toastErr:
			glyph, c = "✗", colRed
		}
		right = pill(glyph+" "+truncate(t.text, w/2), c)
	}
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	row1 := left + strings.Repeat(" ", gap) + right
	row1 = truncateANSI(row1, w)

	hints := m.listHints()
	switch m.screen {
	case screenAdd:
		hints = []hint{{"enter", "add"}, {"esc", "cancel"}}
	case screenDetail:
		hints = m.detailHints()
	}
	if m.modal != modalNone {
		hints = []hint{{"esc", "close"}}
	}
	return row1 + "\n" + hintLine(hints, w)
}

func (m Model) viewHelp() string {
	section := func(title string, rows ...[2]string) string {
		var b strings.Builder
		b.WriteString(fg(colPurple, false).Bold(true).Render(title) + "\n")
		for _, r := range rows {
			b.WriteString(styleKey.Render(padRight(r[0], 14)) + styleDim.Render(r[1]) + "\n")
		}
		return b.String()
	}
	left := section("Navigate",
		[2]string{"↑ ↓  j k", "move"},
		[2]string{"← →  tab", "switch pane"},
		[2]string{"pgup pgdn", "page"},
		[2]string{"g  G", "top / bottom"},
		[2]string{"enter", "open / apply filter"},
	) + "\n" + section("Select",
		[2]string{"space", "mark and move on"},
		[2]string{"ctrl+a", "mark all visible"},
		[2]string{"esc", "clear marks / search"},
	)
	right := section("Act",
		[2]string{"p  r", "pause  resume"},
		[2]string{"v  n", "verify  reannounce"},
		[2]string{"d", "remove (asks first)"},
		[2]string{"a", "add a torrent"},
	) + "\n" + section("View",
		[2]string{"/", "search by name"},
		[2]string{"s  S", "sort column  reverse"},
		[2]string{"1-5  tab", "detail tabs"},
		[2]string{"?  q", "help  quit"},
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "    ", right)
	title := gradientText("Keyboard shortcuts", colCyan, colMagenta, true)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colCyan.color()).Padding(0, 3).
		Render(title + "\n\n" + strings.TrimRight(body, "\n"))
}

func (m Model) viewDeleteConfirm() string {
	names := make([]string, 0, len(m.confirm))
	for _, hash := range m.confirm {
		for _, t := range m.torrents {
			if t.InfoHash == hash {
				names = append(names, t.Name)
			}
		}
	}
	var b strings.Builder
	noun := "torrent"
	if len(m.confirm) != 1 {
		noun = fmt.Sprintf("%d torrents", len(m.confirm))
	}
	b.WriteString(fg(colRed, false).Bold(true).Render("Remove "+noun+"?") + "\n\n")
	for i, n := range names {
		if i == 5 {
			b.WriteString(styleDim.Render(fmt.Sprintf("  …and %d more", len(names)-5)) + "\n")
			break
		}
		b.WriteString(styleText.Render("  • "+truncate(n, 50)) + "\n")
	}
	b.WriteString("\n" + hintLine([]hint{{"y", "remove, keep files"}, {"D", "remove and delete files"}, {"n", "cancel"}}, 70))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colRed.color()).Padding(1, 3).Render(b.String())
}
