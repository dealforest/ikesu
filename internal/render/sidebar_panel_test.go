package render

import (
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/usage"
)

func TestSidebarPanelDrawsUsageAndPluginsAboveFooter(t *testing.T) {
	s := model.New("/tmp", 120, 40)
	s.SidebarVisible = true
	now := time.Unix(1000, 0)
	b := usage.Bucket{UsedPercent: 70, WindowSeconds: 100, ResetAt: 1050}
	panel := &SidebarPanel{Now: now, Sections: []SidebarPanelSection{
		{Key: "usage", Title: "使用量", Lines: []SidebarPanelLine{{Usage: &usage.Row{Label: "CC W", Bucket: &b, ObservedAt: now}}, {Usage: &usage.Row{Label: "CX W", Codex: true}}}},
		{Key: "plugins", Title: "プラグイン", Lines: []SidebarPanelLine{{Text: "sunny 18C"}}},
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
	for _, want := range []string{"▾ 使用量", "CC W", "30%", "│", "CX W", "----", "▾ プラグイン", "sunny 18C"} {
		if !strings.Contains(text, want) {
			t.Fatalf("panel missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(frameLine(f, fr.Y), "[menu]") {
		t.Fatal("footer moved")
	}
	// 見出しの位置はクリックの判定と同じ。
	heads := SidebarPanelLayout(s, panel)
	if len(heads) != 2 || heads[0].Rect.Y != fr.Y-5 || heads[1].Rect.Y != fr.Y-2 {
		t.Fatalf("headings = %+v", heads)
	}
	// 閉じた節は見出しだけになり、スペースの欄が広がる。
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
