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

// The live palette. These are assigned by applyTheme (themes.go) and read by
// every view; they are semantic roles, not literal colours - "primary" is
// cyan in one theme and amber in another.
var (
	cPrimary   rgb // the signature accent: cursor, keys, download speed
	cSecondary rgb // the contrasting accent: upload speed, gradient end
	cTertiary  rgb // a third accent: section headings, categories
	cGood      rgb // seeding, healthy, success
	cWarn      rgb // paused, caution
	cBad       rgb // errors
	cInfo      rgb // neutral information

	cText   rgb
	cDim    rgb
	cFaint  rgb
	cBorder rgb
	cTrack  rgb // empty part of a bar / missing piece
	cSelBg  rgb // selected row background

	cOnPrimary rgb // text drawn on a primary-coloured background (the active tab)

	// Gradients: the logo and titles, a downloading bar, a seeding bar.
	brandFrom, brandTo rgb
	barFrom, barTo     rgb
	seedFrom, seedTo   rgb
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
			b.WriteString(style(cTrack).Render("░"))
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
		fg := cFaint
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
		return stateLook{"▼", cPrimary, barFrom, barTo}
	case "Seeding":
		return stateLook{"▲", cGood, seedFrom, seedTo}
	case "Paused":
		return stateLook{"‖", cWarn, cWarn, cWarn}
	case "Error":
		return stateLook{"×", cBad, cBad, cBad}
	case "CheckingFiles":
		return stateLook{"◐", cTertiary, cTertiary, cInfo}
	case "FetchingMetadata":
		return stateLook{"◌", cSecondary, cSecondary, cTertiary}
	default:
		return stateLook{"○", cDim, cDim, cDim}
	}
}

// fg is a shorthand foreground style, optionally on the selection background.
func fg(c rgb, selected bool) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(c.color())
	if selected {
		s = s.Background(cSelBg.color())
	}
	return s
}

// Shared one-off styles, rebuilt by applyTheme whenever the palette changes.
var (
	styleDim   lipgloss.Style
	styleFaint lipgloss.Style
	styleText  lipgloss.Style
	styleErr   lipgloss.Style
	styleKey   lipgloss.Style
	styleLabel lipgloss.Style
)

func rebuildStyles() {
	styleDim = lipgloss.NewStyle().Foreground(cDim.color())
	styleFaint = lipgloss.NewStyle().Foreground(cFaint.color())
	styleText = lipgloss.NewStyle().Foreground(cText.color())
	styleErr = lipgloss.NewStyle().Foreground(cBad.color())
	styleKey = lipgloss.NewStyle().Foreground(cPrimary.color()).Bold(true)
	styleLabel = lipgloss.NewStyle().Foreground(cDim.color())
}

// pill is a small rounded-looking tag: coloured text on a dim block.
func pill(text string, c rgb) string {
	return lipgloss.NewStyle().Foreground(c.color()).Background(cTrack.color()).Padding(0, 1).Bold(true).Render(text)
}

// panel draws content inside a rounded border, sized to exactly w x h cells
// (border included). A focused panel gets the cyan border.
func panel(content string, w, h int, focused bool) string {
	if w < 4 || h < 3 {
		return ""
	}
	border := cBorder
	if focused {
		border = cPrimary
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
