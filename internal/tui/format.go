package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// humanBytes renders a byte count as e.g. "1.2 MB" - KB/MB/GB/TB, decimal
// (1000-based) since that's what every torrent client's own status line
// already uses and what a user expects to see next to a download.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// humanRate renders a bytes-per-second speed; zero renders as a dash so an
// idle column reads as "nothing happening" rather than a wall of "0 B/s".
func humanRate(bps float64) string {
	if bps < 1 {
		return "-"
	}
	return humanBytes(int64(bps)) + "/s"
}

// humanETA renders remaining time at the given speed; unknown or absurd
// estimates render as a dash.
func humanETA(left int64, bps float64) string {
	if left <= 0 {
		return "-"
	}
	if bps < 1024 {
		return "∞"
	}
	secs := int64(float64(left) / bps)
	switch {
	case secs > 99*3600:
		return "∞"
	case secs >= 3600:
		return fmt.Sprintf("%dh %02dm", secs/3600, (secs%3600)/60)
	case secs >= 60:
		return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// ago renders how long before now t was, in the coarsest unit that reads well.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func ratioString(r float64) string {
	if math.IsInf(r, 1) {
		return "∞" // matches Desktop's own SessionRatioDisplay convention for an unbounded ratio
	}
	return strconv.FormatFloat(r, 'f', 2, 64)
}

func itoa(n int) string { return strconv.Itoa(n) }

// truncate cuts plain text (no ANSI) to at most w display cells, marking the
// cut with an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// padRight pads s (which may contain ANSI) with spaces to w display cells.
func padRight(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// padLeft right-aligns s (which may contain ANSI) within w display cells.
func padLeft(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

// clipLines keeps at most n lines.
func clipLines(lines []string, n int) []string {
	if n < 0 {
		n = 0
	}
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}
