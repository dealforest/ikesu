package tui

import (
	"bytes"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/runtime"
	"github.com/kazuph/ikesu/internal/sidebarplugin"
)

// プラグインごとに見出しが付き、クリックしたプラグインの節だけが閉じたり開いたりする。
func TestSidebarPanelHeadingFolds(t *testing.T) {
	a := NewApp(model.New(t.TempDir(), 120, 40), runtime.NewSupervisor(), &bytes.Buffer{}, "")
	a.st.SidebarVisible = true
	a.panel = &sidebarPanelState{plugins: []sidebarplugin.Item{
		{File: "usage.30s.sh", Name: "usage", Lines: []string{"CC W  \x1b[31m██\x1b[0m 40%", "CX W  ----"}, Ran: true},
		{File: "tenki.1h.sh", Name: "tenki", Lines: []string{"sunny"}, Ran: true},
	}}
	a.layoutSidebarPanel()
	before := a.st.SidebarPanelRows
	if before != 5 {
		t.Fatalf("panel rows = %d", before)
	}
	if a.panel.columns.Load() <= 0 {
		t.Fatal("sidebar width not passed to plugins")
	}
	heads := render.SidebarPanelLayout(a.st, a.sidebarPanel())
	if len(heads) != 2 || heads[0].Key != "usage.30s.sh" {
		t.Fatalf("headings = %+v", heads)
	}
	a.sidebarClick(uv.Mouse{X: heads[0].Rect.X + 2, Y: heads[0].Rect.Y, Button: uv.MouseLeft})
	if !a.panel.collapsed["usage.30s.sh"] || a.panel.collapsed["tenki.1h.sh"] {
		t.Fatal("heading did not fold only its plugin")
	}
	a.layoutSidebarPanel()
	if a.st.SidebarPanelRows != before-2 {
		t.Fatalf("folded rows = %d", a.st.SidebarPanelRows)
	}
	heads = render.SidebarPanelLayout(a.st, a.sidebarPanel())
	a.sidebarClick(uv.Mouse{X: heads[0].Rect.X, Y: heads[0].Rect.Y, Button: uv.MouseLeft})
	if a.panel.collapsed["usage.30s.sh"] {
		t.Fatal("second click did not open it")
	}
}

// プラグインが無いときは、パネルを出さずスペースの欄を狭めない。
func TestSidebarPanelHiddenWithoutPlugins(t *testing.T) {
	a := NewApp(model.New(t.TempDir(), 120, 40), runtime.NewSupervisor(), &bytes.Buffer{}, "")
	a.st.SidebarVisible = true
	a.panel = &sidebarPanelState{}
	if p := a.layoutSidebarPanel(); p != nil || a.st.SidebarPanelRows != 0 {
		t.Fatalf("empty panel shown: %+v rows=%d", p, a.st.SidebarPanelRows)
	}
}

// パネルの行数は画面（ビューアー）ごとに覚え、切り替えても混ざらない。
func TestSidebarPanelRowsFollowEachViewer(t *testing.T) {
	s := model.New(t.TempDir(), 120, 40)
	s.SidebarPanelRows = 5
	d := captureDisplay(s)
	s.SidebarPanelRows = 0
	d.apply(s)
	if s.SidebarPanelRows != 5 {
		t.Fatalf("panel rows not restored: %d", s.SidebarPanelRows)
	}
}

// サイドバーを閉じて起動しても、最初の実行の前に開いたときの幅が決まっている。
func TestSidebarPanelWidthKnownBeforeFirstRun(t *testing.T) {
	a := NewApp(model.New(t.TempDir(), 120, 40), runtime.NewSupervisor(), &bytes.Buffer{}, "")
	a.st.SidebarVisible = false
	done := make(chan struct{})
	defer close(done)
	a.startSidebarPanel(done)
	if got := a.panel.columns.Load(); got <= 0 || got != int64(a.st.ExpandedSidebarWidth()-2) {
		t.Fatalf("columns = %d", got)
	}
}
