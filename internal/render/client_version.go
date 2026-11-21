package render

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func drawClientVersion(f *Frame, in Input, version string, notice bool, out *Output) {
	if version == "" || notice {
		return
	}
	bar := out.ActionBar
	left, right := bar.Copy.X+bar.Copy.W, bar.Cycle.X
	label := "ikesu v" + version
	width := ansi.StringWidth(label)
	style := uv.Style{Fg: f.Palette.Dim}
	if bar.Area.H == 0 || bar.Copy.W == 0 {
		return
	}
	if bar.Cycle.W > 0 && right-left >= width+2 {
		f.Text(left+(right-left-width)/2, bar.Area.Y, label, style, right-1)
		return
	}
	// The moved preset can leave too little room between the action buttons.
	// The former footer row has room for the full version on these displays.
	footer := SidebarFooter(in.State)
	if bar.Preset.W > 0 && in.Copied == "" && footer.W >= width+2 {
		if in.ServerUI {
			style.Bg = f.Palette.PopupBG
		}
		f.Text(footer.X+1, footer.Y, label, style, footer.X+footer.W-1)
	}
}
