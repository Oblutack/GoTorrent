package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	tabActive  = lipgloss.NewStyle().Bold(true).Underline(true)
)

// View satisfies tea.Model.
func (m Model) View() string {
	switch m.screen {
	case screenConnect:
		return m.viewConnect()
	case screenList:
		return m.viewList()
	case screenDetail:
		return m.viewDetail()
	case screenAdd:
		return m.viewAdd()
	}
	return ""
}

func (m Model) viewConnect() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("GoTorrent") + "\n\n")
	b.WriteString("gottrentd address\n")
	b.WriteString(m.addrInput.View() + "\n\n")
	b.WriteString("bearer token\n")
	b.WriteString(m.tokenInput.View() + "\n\n")
	if m.connecting {
		b.WriteString(dimStyle.Render("connecting...") + "\n")
	}
	if m.err != "" {
		b.WriteString(errStyle.Render(m.err) + "\n")
	}
	b.WriteString(dimStyle.Render("\ntab: switch field  enter: connect  ctrl+c: quit"))
	return b.String()
}

func (m Model) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("GoTorrent") + "  ")
	b.WriteString(m.statusLine() + "\n\n")
	b.WriteString(m.table.View() + "\n")
	if m.err != "" {
		b.WriteString("\n" + errStyle.Render(m.err) + "\n")
	}
	b.WriteString(dimStyle.Render("\nenter: detail  a: add  p: pause  r: resume  v: verify  n: reannounce  d: delete  q: quit"))
	return b.String()
}

func (m Model) statusLine() string {
	live := "polling"
	if m.wsConnected {
		live = "live"
	}
	return dimStyle.Render(fmt.Sprintf("%d torrents  ↓ %s  ↑ %s  [%s]",
		m.session.TorrentCount, humanBytes(m.session.TotalDownloaded), humanBytes(m.session.TotalUploaded), live))
}

func (m Model) viewDetail() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(m.detail.Name) + "  " + dimStyle.Render(m.detail.State) + "\n")
	b.WriteString(dimStyle.Render(m.detail.InfoHash) + "\n\n")

	tabs := []string{"Files", "Peers", "Trackers"}
	for i, t := range tabs {
		if detailTab(i) == m.activeTab {
			b.WriteString(tabActive.Render(t))
		} else {
			b.WriteString(dimStyle.Render(t))
		}
		b.WriteString("  ")
	}
	b.WriteString("\n\n")

	if m.detailLoading {
		b.WriteString(dimStyle.Render("loading...") + "\n")
	} else {
		switch m.activeTab {
		case tabFiles:
			b.WriteString(m.viewFiles())
		case tabPeers:
			b.WriteString(m.viewPeers())
		case tabTrackers:
			b.WriteString(m.viewTrackers())
		}
	}

	if m.err != "" {
		b.WriteString("\n" + errStyle.Render(m.err) + "\n")
	}
	b.WriteString(dimStyle.Render("\ntab: switch tab  esc: back  q: quit"))
	return b.String()
}

func (m Model) viewFiles() string {
	if len(m.files) == 0 {
		return dimStyle.Render("(no files yet)") + "\n"
	}
	var b strings.Builder
	for _, f := range m.files {
		fmt.Fprintf(&b, "%-50s %10s  %s\n", strings.Join(f.Path, "/"), humanBytes(f.Length), f.Priority)
	}
	return b.String()
}

func (m Model) viewPeers() string {
	if len(m.peers) == 0 {
		return dimStyle.Render("(no peers)") + "\n"
	}
	var b strings.Builder
	for _, p := range m.peers {
		dir := "in"
		if p.Outbound {
			dir = "out"
		}
		fmt.Fprintf(&b, "%-22s %-4s %10s down  %10s up  %3.0f%%\n",
			p.Addr, dir, humanBytes(p.Downloaded), humanBytes(p.Uploaded), p.Progress*100)
	}
	return b.String()
}

func (m Model) viewTrackers() string {
	if len(m.trackers) == 0 {
		return dimStyle.Render("(no trackers)") + "\n"
	}
	var b strings.Builder
	for _, t := range m.trackers {
		status := fmt.Sprintf("%d seeders, %d leechers", t.Seeders, t.Leechers)
		if t.LastError != "" {
			status = errStyle.Render(t.LastError)
		}
		fmt.Fprintf(&b, "%-60s %s\n", t.URL, status)
	}
	return b.String()
}

func (m Model) viewAdd() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Add torrent") + "\n\n")
	b.WriteString("magnet URI, .torrent URL, or a local file path\n")
	b.WriteString(m.addInput.View() + "\n\n")
	if m.err != "" {
		b.WriteString(errStyle.Render(m.err) + "\n")
	}
	b.WriteString(dimStyle.Render("\nenter: add  esc: cancel"))
	return b.String()
}
