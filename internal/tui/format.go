package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// progressBar renders frac (0-1) as a fixed-width ASCII bar plus a
// percentage - deliberately plain text, not a real graphical widget:
// Desktop's own hand-drawn piece map/speed graph controls don't translate
// to a terminal grid (see ROADMAP.md's own note on this), and this is
// deliberately the simplest honest thing that's still genuinely readable
// in a table cell.
func progressBar(frac float64) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	const width = 6
	filled := int(math.Round(frac * width))
	return fmt.Sprintf("%s%s %3.0f%%",
		strings.Repeat("#", filled), strings.Repeat(".", width-filled), frac*100)
}

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

func ratioString(r float64) string {
	if math.IsInf(r, 1) {
		return "∞" // matches Desktop's own SessionRatioDisplay convention for an unbounded ratio
	}
	return strconv.FormatFloat(r, 'f', 2, 64)
}

func itoa(n int) string { return strconv.Itoa(n) }
