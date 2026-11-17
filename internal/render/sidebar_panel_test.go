package render

import (
	"image/color"
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/model"
)

func TestSidebarPanelDrawsPluginLinesWithColorsAboveFooter(t *testing.T) {
	s := model.New("/tmp", 120, 40)
	s.SidebarVisible = true
	panel := &SidebarPanel{Sections: []SidebarPanelSection{
		{Key: "usage.30s.sh", Title: "usage", Lines: []SidebarPanelLine{{Text: "CC W  \x1b[38;2;229;72;77m██\x1b[0m│ 40%"}, {Text: "CX W  ----"}}},
		{Key: "tenki.1h.sh", Title: "tenki", Lines: []SidebarPanelLine{{Text: "sunny 18C"}}},
	}}
	area := s.SidebarArea()
	s.SidebarPanelRows = SidebarPanelRows(panel, area.H-5)
	if s.SidebarPanelRows != 5 {
		t.Fatalf("panel rows = %d", s.SidebarPanelRows)
	}
	f := NewFrame(120, 40)
	drawSidebar(f, s, Input{State: s, SidebarPanel: panel})
	fr := SidebarFooter(s)
	lines := []string{}
	for y := fr.Y - 5; y < fr.Y; y++ {
		lines = append(lines, frameLine(f, y))
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"▾ usage", "CC W  ██│ 40%", "CX W", "▾ tenki", "sunny 18C"} {
		if !strings.Contains(text, want) {
			t.Fatalf("panel missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") || strings.Contains(text, "[38;2") {
		t.Fatalf("escape sequence drawn as text:\n%s", text)
	}
	if c := f.Cell(fr.X+7, fr.Y-4); c == nil || c.Content != "█" || !reflect.DeepEqual(c.Style.Fg, color.RGBA{229, 72, 77, 255}) {
		t.Fatalf("colored cell = %+v", c)
	}
	if !strings.Contains(frameLine(f, fr.Y), "[menu]") {
		t.Fatal("footer moved")
	}
	heads := SidebarPanelLayout(s, panel)
	if len(heads) != 2 || heads[0].Rect.Y != fr.Y-5 || heads[1].Rect.Y != fr.Y-2 {
		t.Fatalf("headings = %+v", heads)
	}
	panel.Sections[0].Collapsed = true
	if got := SidebarPanelRows(panel, area.H-5); got != 3 {
		t.Fatalf("collapsed rows = %d", got)
	}
}

func TestSidebarPanelLeavesHalfForSpaces(t *testing.T) {
	lines := make([]SidebarPanelLine, 30)
	panel := &SidebarPanel{Sections: []SidebarPanelSection{{Key: "plugins", Title: "プラグイン", Lines: lines}}}
	if got := SidebarPanelRows(panel, 20); got != 10 {
		t.Fatalf("rows = %d, want half of 20", got)
	}
}

// 結合文字と ZWJ の絵文字は、書記素ごとに1つのセルへまとめて描く。
func TestPanelTextKeepsGraphemeClusters(t *testing.T) {
	f := NewFrame(20, 1)
	drawANSIText(f, 0, 0, 20, "が👩‍💻x", uv.Style{})
	if c := f.Cell(0, 0); c == nil || c.Content != "が" {
		t.Fatalf("combining mark dropped: %+v", c)
	}
	if c := f.Cell(2, 0); c == nil || c.Content != "👩‍💻" {
		t.Fatalf("zwj emoji split: %+v", c)
	}
	if c := f.Cell(4, 0); c == nil || c.Content != "x" {
		t.Fatalf("width after emoji: %+v", c)
	}
}
