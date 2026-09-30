package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

func (m Model) viewList(w, h int) string {
	sideW := sidebarW
	if w < 90 {
		sideW = 0 // too narrow for a sidebar; the filter keys still work
	}
	mainW := w - sideW
	main := m.viewTorrentPanel(mainW, h)
	if sideW == 0 {
		return main
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, m.viewSidebar(sideW, h), main)
}

func (m Model) viewSidebar(w, h int) string {
	inner := w - 4
	var lines []string
	lines = append(lines, styleDim.Render("FILTERS"))
	prev := filterKind(-1)
	for i, e := range m.side {
		if e.f.kind != prev {
			switch e.f.kind {
			case filterCategory:
				lines = append(lines, "", styleDim.Render("CATEGORIES"))
			case filterTag:
				lines = append(lines, "", styleDim.Render("TAGS"))
			}
			prev = e.f.kind
		}
		lines = append(lines, m.sidebarLine(e, i == m.sideCursor, inner))
	}
	return panel(strings.Join(clipLines(lines, h-2), "\n"), w, h, m.focus == focusSidebar)
}

func (m Model) sidebarLine(e sideEntry, selected bool, inner int) string {
	focused := selected && m.focus == focusSidebar
	active := e.f == m.filter

	label := truncate(e.label, inner-6)
	count := itoa(e.count)
	if !active && !focused {
		// plain row
		cs := styleFaint
		if e.count > 0 {
			cs = styleDim
		}
		return "  " + padRight(styleText.Render(label), inner-2-lipgloss.Width(count)) + cs.Render(count)
	}

	bg := focused
	marker := fg(colCyan, bg).Render("▌ ")
	text := fg(colCyan, bg).Bold(true).Render(label)
	cnt := fg(colCyan, bg).Render(count)
	gap := fg(colCyan, bg).Render(strings.Repeat(" ", max(0, inner-2-lipgloss.Width(label)-lipgloss.Width(count))))
	return marker + text + gap + cnt
}

// column is one list column; optional columns are dropped, highest drop
// number first, when the terminal is too narrow for the name to breathe.
type column struct {
	id    string
	title string
	w     int
	right bool
	drop  int
}

func listColumns(cw int) []column {
	cols := []column{
		{"mark", "", 2, false, 0},
		{"name", "Name", 0, false, 0},
		{"state", "State", 13, false, 0},
		{"progress", "Progress", 13, false, 0},
		{"down", "Down", 10, true, 0},
		{"up", "Up", 10, true, 1},
		{"eta", "ETA", 7, true, 2},
		{"peers", "Peers", 5, true, 3},
		{"ratio", "Ratio", 5, true, 4},
	}
	for {
		fixed, kept := 0, 0
		for _, c := range cols {
			if c.id != "name" {
				fixed += c.w
			}
			kept++
		}
		nameW := cw - fixed - (kept - 1) // one space between columns
		if nameW >= 20 || !dropOne(&cols) {
			for i := range cols {
				if cols[i].id == "name" {
					cols[i].w = max(nameW, 6)
				}
			}
			return cols
		}
	}
}

// dropOne removes the optional column with the highest drop number.
func dropOne(cols *[]column) bool {
	best, bestDrop := -1, 0
	for i, c := range *cols {
		if c.drop > bestDrop {
			best, bestDrop = i, c.drop
		}
	}
	if best < 0 {
		return false
	}
	*cols = append((*cols)[:best], (*cols)[best+1:]...)
	return true
}

var sortColumnID = map[sortCol]string{
	sortName: "name", sortState: "state", sortProgress: "progress",
	sortDown: "down", sortUp: "up", sortPeers: "peers", sortRatio: "ratio",
}

func (m Model) viewTorrentPanel(w, h int) string {
	cw := w - 4
	cols := listColumns(cw)

	// Column header.
	var head []string
	for _, c := range cols {
		title := c.title
		st := fg(colDim, false).Bold(true)
		if id, ok := sortColumnID[m.sortCol]; ok && id == c.id {
			title += " " + arrow(m.sortDesc)
			st = fg(colCyan, false).Bold(true)
		}
		cell := padRight(st.Render(title), c.w)
		if c.right {
			cell = padLeft(st.Render(title), c.w)
		}
		head = append(head, cell)
	}
	lines := []string{strings.Join(head, " ")}

	rows := m.listRows()
	switch {
	case len(m.visible) == 0:
		lines = append(lines, m.emptyState(cw, rows)...)
	default:
		end := min(len(m.visible), m.offset+rows)
		for i := m.offset; i < end; i++ {
			lines = append(lines, m.torrentRow(m.visible[i], cols, i == m.cursor))
		}
		for len(lines) < rows+1 {
			lines = append(lines, "")
		}
	}

	lines = append(lines, styleFaint.Render(strings.Repeat("─", cw)))
	lines = append(lines, m.selectionStrip(cw))
	return panel(strings.Join(clipLines(lines, h-2), "\n"), w, h, m.focus == focusList)
}

func (m Model) emptyState(cw, rows int) []string {
	msg1 := gradientText("No torrents yet", colCyan, colMagenta, true)
	msg2 := styleDim.Render("press ") + styleKey.Render("a") + styleDim.Render(" to add a magnet link or .torrent")
	if len(m.torrents) > 0 {
		msg1 = gradientText("Nothing matches", colCyan, colMagenta, true)
		msg2 = styleDim.Render("press ") + styleKey.Render("esc") + styleDim.Render(" to clear the search, or pick another filter")
	}
	out := make([]string, 0, rows)
	pad := max(0, (rows-2)/2)
	for i := 0; i < pad; i++ {
		out = append(out, "")
	}
	center := func(s string) string { return strings.Repeat(" ", max(0, (cw-lipgloss.Width(s))/2)) + s }
	out = append(out, center(msg1), center(msg2))
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

var shortState = map[string]string{
	"CheckingFiles":    "Checking",
	"FetchingMetadata": "Metadata",
}

func (m Model) torrentRow(t tuiclient.TorrentSummary, cols []column, cursor bool) string {
	look := lookFor(t.State)
	down, up := m.rateFor(t.InfoHash)
	marked := m.marked[t.InfoHash]
	sel := cursor

	var selBg *rgb
	if sel {
		selBg = &colSelBg
	}
	sp := fg(colText, sel).Render(" ")

	cells := make([]string, 0, len(cols))
	for _, c := range cols {
		var s string
		switch c.id {
		case "mark":
			cur := fg(colCyan, sel).Render(" ")
			if cursor {
				cur = fg(colCyan, sel).Render("▌")
			}
			mk := fg(colMagenta, sel).Render(" ")
			if marked {
				mk = fg(colMagenta, sel).Bold(true).Render("●")
			}
			s = cur + mk
		case "name":
			st := fg(colText, sel)
			if sel {
				st = st.Bold(true)
			}
			s = st.Render(padRight(truncate(t.Name, c.w), c.w))
		case "state":
			name := t.State
			if short, ok := shortState[name]; ok {
				name = short
			}
			s = fg(look.color, sel).Render(padRight(look.glyph+" "+truncate(name, c.w-2), c.w))
		case "progress":
			p := t.Progress()
			s = gradientBar(p, 8, look.from, look.to, selBg) + fg(colDim, sel).Render(fmt.Sprintf(" %3.0f%%", p*100))
		case "down":
			s = rateCell(down, colCyan, c.w, sel)
		case "up":
			s = rateCell(up, colMagenta, c.w, sel)
		case "eta":
			eta := "-"
			if statusGroup(t.State) == "downloading" && t.Left > 0 {
				eta = humanETA(t.Left, down)
			}
			s = fg(colDim, sel).Render(padLeft(eta, c.w))
		case "peers":
			col := colDim
			if t.PeerCount > 0 {
				col = colText
			}
			s = fg(col, sel).Render(padLeft(itoa(t.PeerCount), c.w))
		case "ratio":
			s = fg(colDim, sel).Render(padLeft(ratioString(t.SeedRatio), c.w))
		}
		cells = append(cells, s)
	}
	return strings.Join(cells, sp)
}

func rateCell(bps float64, active rgb, w int, sel bool) string {
	text := humanRate(bps)
	c := colFaint
	if bps >= 1 {
		c = active
	}
	return fg(c, sel).Render(padLeft(text, w))
}

// selectionStrip is the one-line summary of the torrent under the cursor,
// with the cursor position at the right edge.
func (m Model) selectionStrip(cw int) string {
	t, ok := m.current()
	if !ok {
		return ""
	}
	pos := styleFaint.Render(fmt.Sprintf("%d/%d", m.cursor+1, len(m.visible)))

	parts := []string{
		styleText.Render(truncate(t.Name, max(10, cw/2))),
		styleDim.Render(humanBytes(t.TotalLength)),
	}
	if t.Category != "" {
		parts = append(parts, fg(colPurple, false).Render("▪ "+t.Category))
	}
	for _, tag := range t.Tags {
		parts = append(parts, fg(colBlue, false).Render("#"+tag))
	}
	if t.Private {
		parts = append(parts, fg(colAmber, false).Render("private"))
	}
	left := strings.Join(parts, styleFaint.Render(" · "))
	gap := max(1, cw-lipgloss.Width(left)-lipgloss.Width(pos))
	return truncateANSI(left+strings.Repeat(" ", gap)+pos, cw)
}
