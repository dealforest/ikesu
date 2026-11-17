package render

import (
	"fmt"
	"image/color"
	"math"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/usage"
)

// SidebarPanel is the block at the bottom of the left sidebar, above the
// [menu]/[new] footer: the usage limits (as in AgentPal) and the outputs of
// the sidebar plugins. Each section folds from its heading like a space
// section.
type SidebarPanel struct {
	Sections []SidebarPanelSection
	Now      time.Time
}

// SidebarPanelSection is one foldable section.
type SidebarPanelSection struct {
	Key, Title string
	Collapsed  bool
	Lines      []SidebarPanelLine
}

// SidebarPanelLine is a usage bar or a line of text.
type SidebarPanelLine struct {
	Usage *usage.Row
	Text  string
	Error bool
}

// sidebarPanelMaxShare caps the panel so the spaces list keeps most rows.
const sidebarPanelMaxShare = 2

// SidebarPanelRows is how many rows the panel takes in a sidebar whose
// space list has room rows (headings first, lines cut when short of space).
func SidebarPanelRows(p *SidebarPanel, room int) int {
	if p == nil {
		return 0
	}
	limit := room / sidebarPanelMaxShare
	n := 0
	for _, sec := range p.Sections {
		n++
		if !sec.Collapsed {
			n += len(sec.Lines)
		}
	}
	return min(n, max(0, limit))
}

// SidebarPanelHeading is a clickable heading row.
type SidebarPanelHeading struct {
	Key  string
	Rect layout.Rect
}

// SidebarPanelLayout returns where each heading is drawn, for click tests.
func SidebarPanelLayout(s *model.AppState, p *SidebarPanel) []SidebarPanelHeading {
	rows := s.SidebarPanelRows
	if p == nil || rows <= 0 {
		return nil
	}
	fr := SidebarFooter(s)
	y, end := fr.Y-rows, fr.Y
	var out []SidebarPanelHeading
	for _, sec := range p.Sections {
		if y >= end {
			break
		}
		out = append(out, SidebarPanelHeading{Key: sec.Key, Rect: layout.Rect{X: fr.X, Y: y, W: fr.W, H: 1}})
		y++
		if !sec.Collapsed {
			y += len(sec.Lines)
		}
	}
	return out
}

func drawSidebarPanel(f *Frame, s *model.AppState, in Input) {
	p := in.SidebarPanel
	rows := s.SidebarPanelRows
	if p == nil || rows <= 0 {
		return
	}
	fr := SidebarFooter(s)
	x, w := fr.X, fr.W
	y, end := fr.Y-rows, fr.Y
	f.Fill(x, y, w, rows, " ", uv.Style{})
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	for _, sec := range p.Sections {
		if y >= end {
			return
		}
		drawSectionHeading(f, x, y, w, sec.Title, sec.Collapsed, false, false)
		y++
		if sec.Collapsed {
			continue
		}
		for _, line := range sec.Lines {
			if y >= end {
				return
			}
			switch {
			case line.Usage != nil:
				drawUsageLine(f, x, y, w, *line.Usage, now)
			case line.Error:
				f.Text(x+1, y, truncate(line.Text, max(0, w-1)), uv.Style{Fg: f.Palette.Dim}, x+w)
			default:
				f.Text(x+1, y, truncate(line.Text, max(0, w-1)), uv.Style{Fg: f.Palette.Text}, x+w)
			}
			y++
		}
	}
}

var (
	claudeUsageColor = color.RGBA{0xd9, 0x77, 0x57, 0xff}
	codexUsageColor  = color.RGBA{0x6c, 0x9e, 0xf8, 0xff}
	overuseColor     = color.RGBA{0xe5, 0x48, 0x4d, 0xff}
	surplusColor     = color.RGBA{0x46, 0xc4, 0x6e, 0xff}
)

// drawUsageLine draws "CC W  ████▓▓│░░░ 62% 21:00": the colored part is what
// is left, │ is the even pace for the time left, red marks use beyond the
// pace and green the room under it.
func drawUsageLine(f *Frame, x, y, w int, row usage.Row, now time.Time) {
	const labelW = 7 // a space, up to five characters ("CC 5h", "FAB W") and a gap
	if w < labelW+6 {
		f.Text(x+1, y, truncate(row.Label, max(0, w-1)), uv.Style{Fg: f.Palette.Dim}, x+w)
		return
	}
	f.Text(x+1, y, row.Label, uv.Style{Fg: f.Palette.Dim, Attrs: uv.AttrBold}, x+labelW-1)
	if row.Bucket == nil {
		f.Text(x+labelW, y, "----", uv.Style{Fg: f.Palette.Dim}, x+w)
		return
	}
	b := *row.Bucket
	remaining := b.RemainingPercent()
	pct := fmt.Sprintf("%3d%%", int(math.Round(remaining)))
	reset := resetLabel(b.ResetTime(), now)
	// The reset time is shown only when the bar keeps at least ten cells.
	tail := " " + pct
	barW := w - labelW - ansi.StringWidth(tail)
	if reset != "" && barW-1-ansi.StringWidth(reset) >= 10 {
		tail += " " + reset
		barW -= 1 + ansi.StringWidth(reset)
	}
	if barW < 1 {
		return
	}
	fill := claudeUsageColor
	if row.Codex {
		fill = codexUsageColor
	}
	observed := row.ObservedAt
	if observed.IsZero() {
		observed = now
	}
	pace, hasPace := b.PaceRemainingPercent(observed)
	diff, hasDiff := usage.PaceDifference{}, false
	if hasPace {
		diff, hasDiff = usage.Difference(remaining, pace)
	}
	marker := -1
	if hasPace {
		marker = min(barW-1, max(0, int(pace/100*float64(barW))))
	}
	bx := x + labelW
	for i := 0; i < barW; i++ {
		pos := (float64(i) + 0.5) / float64(barW) * 100
		glyph, style := "░", uv.Style{Fg: f.Palette.Dim}
		if pos < remaining {
			glyph, style = "█", uv.Style{Fg: fill}
		}
		if hasDiff && pos >= diff.Lower && pos < diff.Upper {
			c := surplusColor
			if diff.Kind == usage.Overuse {
				c = overuseColor
			}
			glyph, style = "█", uv.Style{Fg: c}
		}
		if i == marker {
			glyph, style = "│", uv.Style{Fg: f.Palette.Text, Attrs: uv.AttrBold}
		}
		f.Set(bx+i, y, glyph, style)
	}
	f.Text(bx+barW, y, tail, uv.Style{Fg: f.Palette.Text}, x+w)
}

// resetLabel is the reset time: the clock time when it is within a day,
// otherwise month/day.
func resetLabel(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.Local()
	if local.Sub(now) < 24*time.Hour {
		return local.Format("15:04")
	}
	return local.Format("1/2")
}
