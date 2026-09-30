package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// rgb is a 24-bit colour. lipgloss downgrades these to whatever the terminal
// actually supports (256/16 colours), so the palette degrades gracefully
// rather than needing its own capability detection.
type rgb struct{ r, g, b uint8 }

func (c rgb) hex() string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }

func (c rgb) color() lipgloss.Color { return lipgloss.Color(c.hex()) }

func lerpRGB(a, b rgb, t float64) rgb {
	t = clamp01(t)
	mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return rgb{mix(a.r, b.r), mix(a.g, b.g), mix(a.b, b.b)}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// The neon palette: a cyan-to-magenta gradient as the signature, on whatever
// background the terminal already has.
var (
	colCyan    = rgb{0x00, 0xe5, 0xff}
	colMagenta = rgb{0xff, 0x2b, 0xd6}
	colPurple  = rgb{0xb4, 0x8e, 0xff}
	colGreen   = rgb{0x3d, 0xdc, 0x97}
	colAmber   = rgb{0xff, 0xb4, 0x54}
	colRed     = rgb{0xff, 0x53, 0x70}
	colBlue    = rgb{0x6e, 0xa8, 0xfe}

	colText   = rgb{0xe6, 0xed, 0xf3}
	colDim    = rgb{0x8b, 0x94, 0x9e}
	colFaint  = rgb{0x48, 0x4f, 0x58}
	colBorder = rgb{0x30, 0x36, 0x3d}
	colTrack  = rgb{0x2b, 0x31, 0x3a} // empty part of a bar / missing piece
	colSelBg  = rgb{0x1b, 0x26, 0x3b} // selected row background
)

// gradientText colours each rune of s along a from->to gradient.
func gradientText(s string, from, to rgb, bold bool) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runes {
		t := 0.0
		if len(runes) > 1 {
			t = float64(i) / float64(len(runes)-1)
		}
		st := lipgloss.NewStyle().Foreground(lerpRGB(from, to, t).color()).Bold(bold)
		b.WriteString(st.Render(string(r)))
	}
	return b.String()
}

// partialBlocks are the eighth-width block glyphs, index n = n/8 of a cell.
var partialBlocks = []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// gradientBar renders frac (0-1) as a width-cell bar whose colour runs from
// `from` to `to` across the whole bar width, with a smooth eighth-block edge
// and a dim track for the unfilled part. bg, when non-nil, is painted behind
// every cell so the bar sits correctly inside a highlighted row.
func gradientBar(frac float64, width int, from, to rgb, bg *rgb) string {
	if width <= 0 {
		return ""
	}
	frac = clamp01(frac)
	cells := frac * float64(width)
	full := int(cells)
	rem := cells - float64(full)
	part := int(rem * 8)

	style := func(fg rgb) lipgloss.Style {
		s := lipgloss.NewStyle().Foreground(fg.color())
		if bg != nil {
			s = s.Background(bg.color())
		}
		return s
	}
	var b strings.Builder
	for i := 0; i < width; i++ {
		t := 0.0
		if width > 1 {
			t = float64(i) / float64(width-1)
		}
		switch {
		case i < full:
			b.WriteString(style(lerpRGB(from, to, t)).Render("█"))
		case i == full && part > 0:
			b.WriteString(style(lerpRGB(from, to, t)).Render(string(partialBlocks[part])))
		default:
			b.WriteString(style(colTrack).Render("░"))
		}
	}
	return b.String()
}

// sparkLevels are the eight vertical block heights, lowest first.
var sparkLevels = []rune("▁▂▃▄▅▆▇█")

// sparkline renders the last `width` values of vals as vertical blocks scaled
// to the largest value in view, coloured along from->to left to right. Fewer
// values than width are right-aligned so the newest sample is always at the
// right edge.
func sparkline(vals []float64, width int, from, to rgb) string {
	if width <= 0 {
		return ""
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	maxV := 0.0
	for _, v := range vals {
		if v > maxV {
			maxV = v
		}
	}
	var b strings.Builder
	pad := width - len(vals)
	for i := 0; i < width; i++ {
		t := 0.0
		if width > 1 {
			t = float64(i) / float64(width-1)
		}
		level := 0
		fg := colFaint
		if i >= pad && maxV > 0 {
			v := vals[i-pad]
			level = int(math.Round(v / maxV * float64(len(sparkLevels)-1)))
			if v > 0 {
				fg = lerpRGB(from, to, t)
			}
		}
		b.WriteString(lipgloss.NewStyle().Foreground(fg.color()).Render(string(sparkLevels[level])))
	}
	return b.String()
}

// stateLook is how a torrent state is drawn: a glyph, a colour, and the bar
// gradient used for its progress.
type stateLook struct {
	glyph    string
	color    rgb
	from, to rgb
}

func lookFor(state string) stateLook {
	switch state {
	case "Downloading":
		return stateLook{"▼", colCyan, colCyan, colMagenta}
	case "Seeding":
		return stateLook{"▲", colGreen, colGreen, colCyan}
	case "Paused":
		return stateLook{"‖", colAmber, colAmber, colAmber}
	case "Error":
		return stateLook{"×", colRed, colRed, colRed}
	case "CheckingFiles":
		return stateLook{"◐", colPurple, colPurple, colBlue}
	case "FetchingMetadata":
		return stateLook{"◌", colMagenta, colMagenta, colPurple}
	default:
		return stateLook{"○", colDim, colDim, colDim}
	}
}

// fg is a shorthand foreground style, optionally on the selection background.
func fg(c rgb, selected bool) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(c.color())
	if selected {
		s = s.Background(colSelBg.color())
	}
	return s
}

// Shared one-off styles.
var (
	styleDim   = lipgloss.NewStyle().Foreground(colDim.color())
	styleFaint = lipgloss.NewStyle().Foreground(colFaint.color())
	styleText  = lipgloss.NewStyle().Foreground(colText.color())
	styleErr   = lipgloss.NewStyle().Foreground(colRed.color())
	styleKey   = lipgloss.NewStyle().Foreground(colCyan.color()).Bold(true)
	styleLabel = lipgloss.NewStyle().Foreground(colDim.color())
)

// pill is a small rounded-looking tag: coloured text on a dim block.
func pill(text string, c rgb) string {
	return lipgloss.NewStyle().Foreground(c.color()).Background(colTrack.color()).Padding(0, 1).Bold(true).Render(text)
}

// panel draws content inside a rounded border, sized to exactly w x h cells
// (border included). A focused panel gets the cyan border.
func panel(content string, w, h int, focused bool) string {
	if w < 4 || h < 3 {
		return ""
	}
	border := colBorder
	if focused {
		border = colCyan
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border.color()).
		Padding(0, 1).
		Width(w - 2).
		Height(h - 2).
		MaxWidth(w).
		MaxHeight(h).
		Render(content)
}
