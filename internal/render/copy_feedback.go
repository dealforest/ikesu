package render

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/kazuph/ikesu/internal/layout"
	"strings"
)

// Recorded feedback has one text row, a dot and gap, and a border on each side.
func drawCopyFeedback(f *Frame, area layout.Rect, text string) {
	width := min(area.W, ansi.StringWidth(text)+4)
	if width < 4 || area.H < 3 {
		return
	}
	x, y := area.X+(area.W-width)/2, area.Y+area.H-3
	border := uv.Style{Fg: f.Palette.Green, Bg: f.Palette.PopupBG}
	ix, iy, iw, _ := f.Box(x, y, width, 3, border)
	f.Fill(ix, iy, iw, 1, " ", uv.Style{Bg: f.Palette.PopupBG})
	f.Set(ix, iy, "●", border)
	f.Text(ix+2, iy, text, uv.Style{Fg: f.Palette.Text, Bg: f.Palette.PopupBG, Attrs: uv.AttrBold}, ix+iw)
}

func drawSidebarCopyFeedback(f *Frame, in Input) {
	s := in.State
	if in.Copied == "" || !s.SidebarVisible || in.SidebarConfig.UI.Toast.Clipboard.Enabled != nil && !*in.SidebarConfig.UI.Toast.Clipboard.Enabled {
		return
	}
	area := s.SidebarArea()
	if area.W > 1 && area.H > 0 {
		x, y, w := area.X, area.Y+area.H-1, area.W-2
		if menu := sidebarMenuRect(s, in.ServerUI, in.SidebarMenuRight); !in.LegacySidebarControls && menu.W > 0 {
			feedbackX, feedbackWidth := x+menu.W, w-menu.W
			if in.SidebarMenuRight {
				feedbackX, feedbackWidth = x, min(w, menu.X-x)
			}
			if feedbackWidth < ansi.StringWidth(in.Copied) {
				y = SidebarFooter(s).Y
			} else {
				x, w = feedbackX, feedbackWidth
			}
		}
		f.Fill(x, y, w, 1, " ", uv.Style{})
		copyStyle := uv.Style{Fg: f.Palette.Green, Bg: f.Palette.PopupBG, Attrs: uv.AttrBold}
		label := truncate(in.Copied, w)
		f.Text(x, y, label, copyStyle, x+w)
		prefix := strings.TrimSuffix(in.Copied, " lines")
		for xx := x; xx < x+min(len([]rune(prefix)), len([]rune(label))); xx++ {
			if cell := f.Cell(xx, y); cell != nil {
				cell.Style.Underline = uv.UnderlineSingle
			}
		}
	}
}
