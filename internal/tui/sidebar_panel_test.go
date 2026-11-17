package tui

import (
	"bytes"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/runtime"
	"github.com/kazuph/ikesu/internal/sidebarplugin"
	"github.com/kazuph/ikesu/internal/usage"
)

// 使用量とプラグインの見出しをクリックすると、その節だけが閉じたり開いたりする。
func TestSidebarPanelHeadingFolds(t *testing.T) {
	a := NewApp(model.New(t.TempDir(), 120, 40), runtime.NewSupervisor(), &bytes.Buffer{}, "")
	a.st.SidebarVisible = true
	b := usage.Bucket{UsedPercent: 40}
	a.panel = &sidebarPanelState{usage: []usage.Row{{Label: "CC W", Bucket: &b}, {Label: "CX W", Codex: true}}, hasUsage: true,
		plugins: []sidebarplugin.Item{{File: "tenki.1h.sh", Name: "tenki", Text: "sunny", Ran: true}}}
	a.layoutSidebarPanel()
	before := a.st.SidebarPanelRows
	if before != 5 {
		t.Fatalf("panel rows = %d", before)
	}
	heads := render.SidebarPanelLayout(a.st, a.sidebarPanel())
	if len(heads) != 2 {
		t.Fatalf("headings = %+v", heads)
	}
	a.sidebarClick(uv.Mouse{X: heads[0].Rect.X + 2, Y: heads[0].Rect.Y, Button: uv.MouseLeft})
	if !a.panel.usageCollapsed || a.panel.pluginsCollapsed {
		t.Fatal("usage heading did not fold only usage")
	}
	a.layoutSidebarPanel()
	if a.st.SidebarPanelRows != before-2 {
		t.Fatalf("folded rows = %d", a.st.SidebarPanelRows)
	}
	heads = render.SidebarPanelLayout(a.st, a.sidebarPanel())
	a.sidebarClick(uv.Mouse{X: heads[0].Rect.X, Y: heads[0].Rect.Y, Button: uv.MouseLeft})
	if a.panel.usageCollapsed {
		t.Fatal("second click did not open usage")
	}
}

// データが何も無いときは、パネルを出さずスペースの欄を狭めない。
func TestSidebarPanelHiddenWithoutData(t *testing.T) {
	a := NewApp(model.New(t.TempDir(), 120, 40), runtime.NewSupervisor(), &bytes.Buffer{}, "")
	a.st.SidebarVisible = true
	a.panel = &sidebarPanelState{usage: usage.Rows(nil, nil)}
	if p := a.layoutSidebarPanel(); p != nil || a.st.SidebarPanelRows != 0 {
		t.Fatalf("empty panel shown: %+v rows=%d", p, a.st.SidebarPanelRows)
	}
}
