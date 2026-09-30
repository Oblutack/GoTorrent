package tui

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Oblutack/GoTorrent/internal/tuiclient"
)

// torrentByHash finds a torrent in the full (unfiltered) list.
func (m Model) torrentByHash(hash string) (tuiclient.TorrentSummary, bool) {
	for _, t := range m.torrents {
		if t.InfoHash == hash {
			return t, true
		}
	}
	return tuiclient.TorrentSummary{}, false
}

func (m Model) viewDetail(w, h int) string {
	iw, ih := w-4, h-2

	// The detail fetch is authoritative once it has landed; until then the
	// list's own summary keeps the header populated.
	t := m.detail.TorrentSummary
	if t.InfoHash == "" {
		t, _ = m.torrentByHash(m.detailHash)
	}
	look := lookFor(t.State)
	down, up := m.rateFor(m.detailHash)

	title := gradientText(truncate(t.Name, iw-18), brandFrom, brandTo, true)
	if t.Name == "" {
		title = styleDim.Render("loading…")
	}
	stateTag := fg(look.color, false).Bold(true).Render(look.glyph + " " + t.State)
	line1 := title + "  " + stateTag

	line2 := styleFaint.Render(m.detailHash)

	p := t.Progress()
	pct := fmt.Sprintf(" %5.1f%%", p*100)
	barW := max(10, iw-lipgloss.Width(pct))
	line3 := gradientBar(p, barW, look.from, look.to, nil) + styleText.Render(pct)

	eta := "-"
	if statusGroup(t.State) == "downloading" && t.Left > 0 {
		eta = humanETA(t.Left, down)
	}
	stat := func(label, val string, c rgb) string {
		return styleLabel.Render(label+" ") + fg(c, false).Render(val)
	}
	line4 := strings.Join([]string{
		stat("↓", humanRateOrZero(down), cPrimary),
		stat("↑", humanRateOrZero(up), cSecondary),
		stat("ETA", eta, cText),
		stat("ratio", ratioString(t.SeedRatio), cText),
		stat("peers", fmt.Sprintf("%d (%d seeds)", t.PeerCount, t.SeedCount), cText),
		stat("size", humanBytes(t.TotalLength), cText),
	}, styleFaint.Render("  ·  "))

	tabs := make([]string, tabCount)
	for i := range tabs {
		label := fmt.Sprintf("%d %s", i+1, tabNames[i])
		if detailTab(i) == m.activeTab {
			tabs[i] = lipgloss.NewStyle().Foreground(cOnPrimary.color()).Background(cPrimary.color()).Bold(true).Padding(0, 1).Render(label)
		} else {
			tabs[i] = styleDim.Padding(0, 1).Render(label)
		}
	}
	tabRow := strings.Join(tabs, " ")

	head := []string{line1, line2, "", line3, line4, "", tabRow, ""}
	avail := ih - len(head)
	if avail < 1 {
		avail = 1
	}

	var content []string
	switch {
	case m.detailLoading:
		content = []string{styleDim.Render("loading…")}
	default:
		switch m.activeTab {
		case tabOverview:
			content = m.overviewLines(t, iw)
		case tabFiles:
			content = m.fileLines(iw)
		case tabPeers:
			content = m.peerLines(iw)
		case tabTrackers:
			content = m.trackerLines(iw)
		case tabPieces:
			content = m.pieceLines(iw, avail)
		}
	}

	scroll := m.detailScroll
	if maxScroll := len(content) - avail; scroll > maxScroll {
		scroll = max(0, maxScroll)
	}
	content = content[scroll:]
	if len(content) > avail {
		content = content[:avail]
	}

	lines := append(head, content...)
	return panel(strings.Join(clipLines(lines, ih), "\n"), w, h, true)
}

func kv(label, value string) string {
	return styleLabel.Render(padRight(label, 14)) + value
}

func (m Model) overviewLines(t tuiclient.TorrentSummary, iw int) []string {
	d := m.detail
	val := func(s string) string { return styleText.Render(s) }
	var out []string
	add := func(label, value string) {
		if value != "" {
			out = append(out, kv(label, value))
		}
	}
	add("State", val(t.State))
	add("Downloaded", val(humanBytes(t.Downloaded)))
	add("Uploaded", val(humanBytes(t.Uploaded)))
	add("Remaining", val(humanBytes(t.Left)))
	add("Pieces", val(fmt.Sprintf("%d / %d", t.HavePieces, t.NumPieces))+styleDim.Render(fmt.Sprintf("   piece size %s", humanBytes(d.PieceLength))))
	add("Availability", val(fmt.Sprintf("%d copies of the rarest piece", t.MinAvailability)))
	add("Peers", val(fmt.Sprintf("%d connected · %d seeds · %d leechers", t.PeerCount, t.SeedCount, t.LeechCount)))
	if d.InEndgame {
		add("Endgame", fg(cWarn, false).Render("yes, duplicating the last requests"))
	}
	if t.Private {
		add("Private", fg(cWarn, false).Render("yes (no DHT, PEX or LSD)"))
	}
	if t.Category != "" {
		add("Category", fg(cTertiary, false).Render(t.Category))
	}
	if len(t.Tags) > 0 {
		add("Tags", fg(cInfo, false).Render("#"+strings.Join(t.Tags, "  #")))
	}
	if t.ForceStart {
		add("Queue", fg(cWarn, false).Render("force started"))
	}
	if d.SeedingDurationSeconds > 0 {
		add("Seeding for", val((time.Duration(d.SeedingDurationSeconds) * time.Second).Round(time.Second).String()))
	}
	out = append(out, "")
	add("Save path", val(truncate(d.DownloadDir, iw-16)))
	add("Content", val(truncate(d.ContentPath, iw-16)))
	add("Source", val(truncate(d.Source, iw-16)))
	if d.CreatedBy != "" {
		add("Created by", val(d.CreatedBy))
	}
	if d.CreationDate != nil {
		add("Created", val(d.CreationDate.Format("2006-01-02 15:04")))
	}
	if d.Comment != "" {
		add("Comment", val(truncate(d.Comment, iw-16)))
	}
	if !t.AddedOn.IsZero() {
		add("Added", val(t.AddedOn.Format("2006-01-02 15:04")))
	}
	return out
}

var priorityColor = map[string]rgb{
	"high": cSecondary, "normal": cText, "low": cInfo, "skip": cFaint,
}

func (m Model) fileLines(iw int) []string {
	var real []tuiclient.FileEntry
	for _, f := range m.files {
		if !f.Padding {
			real = append(real, f)
		}
	}
	if len(real) == 0 {
		return []string{styleDim.Render("no files yet (waiting for metadata)")}
	}
	nameW := max(10, iw-24)
	out := []string{styleDim.Render(padRight("NAME", nameW) + padLeft("SIZE", 12) + "  PRIORITY")}
	for _, f := range real {
		c, ok := priorityColor[f.Priority]
		if !ok {
			c = cText
		}
		out = append(out, styleText.Render(padRight(truncate(strings.Join(f.Path, "/"), nameW), nameW))+
			styleDim.Render(padLeft(humanBytes(f.Length), 12))+"  "+fg(c, false).Render(f.Priority))
	}
	return out
}

// clientName decodes an Azureus-style peer id ("-XX1234-...") into a client
// name when the two-letter code is a well-known one.
func clientName(peerID string) string {
	raw, err := hex.DecodeString(peerID)
	if err != nil || len(raw) < 8 || raw[0] != '-' || raw[7] != '-' {
		return ""
	}
	names := map[string]string{
		"GT": "GoTorrent", "qB": "qBittorrent", "TR": "Transmission", "UT": "µTorrent",
		"lt": "libtorrent", "LT": "libtorrent", "DE": "Deluge", "AZ": "Vuze",
	}
	return names[string(raw[1:3])]
}

func (m Model) peerLines(iw int) []string {
	if len(m.peers) == 0 {
		return []string{styleDim.Render("no peers connected")}
	}
	peers := append([]tuiclient.PeerEntry(nil), m.peers...)
	rate := func(addr string) (float64, float64) {
		if r, ok := m.peerRates[addr]; ok {
			return r.down, r.up
		}
		return 0, 0
	}
	sort.SliceStable(peers, func(i, j int) bool {
		di, _ := rate(peers[i].Addr)
		dj, _ := rate(peers[j].Addr)
		if di != dj {
			return di > dj
		}
		return peers[i].Addr < peers[j].Addr
	})

	out := []string{styleDim.Render(padRight("ADDRESS", 22) + padRight("", 5) + padRight("PROGRESS", 16) + padLeft("DOWN", 11) + padLeft("UP", 11) + "  LINK      CLIENT")}
	for _, p := range peers {
		dir, dirCol := "in ", cInfo
		if p.Outbound {
			dir, dirCol = "out", cTertiary
		}
		d, u := rate(p.Addr)
		link := fg(cGood, false).Render("● open  ")
		if p.PeerChoking {
			link = fg(cWarn, false).Render("○ choked")
		}
		out = append(out, styleText.Render(padRight(truncate(p.Addr, 21), 22))+
			fg(dirCol, false).Render(padRight(dir, 5))+
			gradientBar(p.Progress, 10, barFrom, barTo, nil)+styleDim.Render(fmt.Sprintf(" %3.0f%%", p.Progress*100))+" "+
			rateCell(d, cPrimary, 11, false)+rateCell(u, cSecondary, 11, false)+"  "+link+" "+styleDim.Render(clientName(p.PeerID)))
	}
	return out
}

func (m Model) trackerLines(iw int) []string {
	if len(m.trackers) == 0 {
		return []string{styleDim.Render("no trackers")}
	}
	now := m.detailAt
	var out []string
	for _, t := range m.trackers {
		dot, c := "●", cGood
		status := fmt.Sprintf("%d seeders · %d leechers · announced %s", t.Seeders, t.Leechers, ago(t.LastAnnounce, now))
		switch {
		case t.LastError != "":
			dot, c = "●", cBad
		case t.LastAnnounce.IsZero():
			dot, c = "○", cWarn
			status = "not announced yet"
		}
		out = append(out, fg(c, false).Render(dot)+" "+styleText.Render(truncate(t.URL, iw-4)))
		out = append(out, "  "+styleDim.Render(status))
		if t.LastError != "" {
			out = append(out, "  "+styleErr.Render(truncate(t.LastError, iw-4)))
		}
	}
	return out
}

func (m Model) pieceLines(iw, avail int) []string {
	if m.pieces.NumPieces == 0 {
		return []string{styleDim.Render("no piece information yet (waiting for metadata)")}
	}
	mapH := max(1, avail-2)
	lines, per := pieceMap(m.pieces, iw, mapH)
	unit := "each block = 1 piece"
	if per > 1 {
		unit = fmt.Sprintf("each cell = %d pieces", per)
	}
	have := fg(cPrimary, false).Render("█")
	missing := fg(cTrack, false).Render("█")
	legend := fmt.Sprintf("%s have   %s missing   %s",
		have, missing,
		styleDim.Render(fmt.Sprintf("%d / %d pieces · %.1f%% · %s",
			m.pieces.HaveCount, m.pieces.NumPieces,
			100*float64(m.pieces.HaveCount)/float64(m.pieces.NumPieces), unit)))
	return append(lines, "", legend)
}

// pieceMap draws the have/missing bitfield inside a w x h character area and
// returns its rows plus how many pieces each cell covers (1 when every piece
// has its own block).
//
// Each text cell shows two vertically adjacent half-cells (an upper half as
// the glyph foreground, a lower half as its background), which makes the
// drawing grid square. A torrent with few pieces is drawn as big square
// blocks with a one-cell gutter, using the largest block size that fits, so
// the map fills the panel instead of huddling in one corner. A torrent with
// more pieces than half-cells aggregates runs of pieces into single cells,
// shaded by how complete the run is. Complete areas take a cyan-to-magenta
// gradient by position; missing ones stay a dim track.
func pieceMap(p tuiclient.PiecesResponse, w, h int) ([]string, int) {
	if w <= 0 || h <= 0 || p.NumPieces == 0 {
		return nil, 1
	}

	// Largest block edge (in half-cells) such that every piece fits.
	block := 1
	for _, b := range []int{8, 6, 4, 3, 2} {
		cols := w / b
		if cols == 0 {
			continue
		}
		rows := (p.NumPieces + cols - 1) / cols // block rows
		if rows*b <= h*2 {
			block = b
			break
		}
	}

	var colorAt func(x, hy int) (rgb, bool)
	per := 1
	if block > 1 {
		cols := w / block
		pos := func(i int) float64 {
			if p.NumPieces <= 1 {
				return 0
			}
			return float64(i) / float64(p.NumPieces-1)
		}
		colorAt = func(x, hy int) (rgb, bool) {
			if x/block >= cols {
				return rgb{}, false
			}
			lx, ly := x%block, hy%block
			if block >= 3 && (lx == block-1 || ly == block-1) {
				return rgb{}, false // the gutter between blocks
			}
			i := (hy/block)*cols + x/block
			if i >= p.NumPieces {
				return rgb{}, false
			}
			if p.Has(i) {
				return lerpRGB(brandFrom, brandTo, pos(i)), true
			}
			return cTrack, true
		}
	} else {
		cells := w * h * 2
		per = (p.NumPieces + cells - 1) / cells
		used := (p.NumPieces + per - 1) / per
		colorAt = func(x, hy int) (rgb, bool) {
			idx := hy*w + x
			if idx >= used {
				return rgb{}, false
			}
			lo, hi := idx*per, min(p.NumPieces, (idx+1)*per)
			have := 0
			for i := lo; i < hi; i++ {
				if p.Has(i) {
					have++
				}
			}
			frac := float64(have) / float64(hi-lo)
			pos := 0.0
			if used > 1 {
				pos = float64(idx) / float64(used-1)
			}
			return lerpRGB(cTrack, lerpRGB(brandFrom, brandTo, pos), frac), true
		}
	}

	lines := make([]string, 0, h)
	for row := 0; row < h; row++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			top, okTop := colorAt(x, row*2)
			bot, okBot := colorAt(x, row*2+1)
			switch {
			case !okTop && !okBot:
				b.WriteString(" ")
			case !okTop:
				b.WriteString(lipgloss.NewStyle().Foreground(bot.color()).Render("▄"))
			case !okBot:
				b.WriteString(lipgloss.NewStyle().Foreground(top.color()).Render("▀"))
			default:
				b.WriteString(lipgloss.NewStyle().Foreground(top.color()).Background(bot.color()).Render("▀"))
			}
		}
		lines = append(lines, b.String())
	}
	// Trim trailing empty rows so a small torrent doesn't leave a tall void.
	for len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, per
}
