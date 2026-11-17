package render

import (
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
)

// SidebarPanel is the block at the bottom of the left sidebar, above the
// [menu]/[new] footer: one section per sidebar plugin with its output. Each
// section folds from its heading like a space section.
type SidebarPanel struct {
	Sections []SidebarPanelSection
}

// SidebarPanelSection is one foldable section.
type SidebarPanelSection struct {
	Key, Title string
	Collapsed  bool
	Lines      []SidebarPanelLine
}

// SidebarPanelLine is a line of text; SGR colors in it are drawn.
type SidebarPanelLine struct {
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
			base := uv.Style{Fg: f.Palette.Text}
			if line.Error {
				base = uv.Style{Fg: f.Palette.Dim}
			}
			drawANSIText(f, x+1, y, x+w, line.Text, base)
			y++
		}
	}
}

// drawANSIText draws text whose SGR sequences (colors, bold, dim) come from a
// plugin's output, clipped at limit.
func drawANSIText(f *Frame, x, y, limit int, text string, base uv.Style) {
	style := base
	for i := 0; i < len(text) && x < limit; {
		if text[i] == 0x1b {
			end := strings.IndexByte(text[i:], 'm')
			if end < 0 {
				break
			}
			style = applySGR(style, base, text[i+2:i+end])
			i += end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		w := ansi.StringWidth(string(r))
		if x+w > limit {
			break
		}
		if w > 0 {
			f.Set(x, y, string(r), style)
		}
		x += w
		i += size
	}
}

// applySGR applies one SGR parameter list ("1;38;2;217;119;87") to style.
func applySGR(style, base uv.Style, params string) uv.Style {
	parts := strings.Split(params, ";")
	if params == "" {
		parts = []string{"0"}
	}
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			style = base
		case n == 1:
			style.Attrs |= uv.AttrBold
		case n == 2:
			style.Attrs |= uv.AttrFaint
		case n == 22:
			style.Attrs &^= uv.AttrBold | uv.AttrFaint
		case n >= 30 && n <= 37:
			style.Fg = ansi.BasicColor(n - 30)
		case n >= 90 && n <= 97:
			style.Fg = ansi.BasicColor(n - 90 + 8)
		case n == 39:
			style.Fg = base.Fg
		case n == 38 && i+2 < len(parts) && parts[i+1] == "5":
			if c, err := strconv.Atoi(parts[i+2]); err == nil && c >= 0 && c < 256 {
				style.Fg = ansi.IndexedColor(c)
			}
			i += 2
		case n == 38 && i+4 < len(parts) && parts[i+1] == "2":
			r, _ := strconv.Atoi(parts[i+2])
			g, _ := strconv.Atoi(parts[i+3])
			b, _ := strconv.Atoi(parts[i+4])
			style.Fg = color.RGBA{uint8(r), uint8(g), uint8(b), 0xff}
			i += 4
		}
	}
	return style
}
