package tui

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/sidebarplugin"
)

// sidebarPanelState holds the sidebar plugins' outputs shown at the bottom of
// the left sidebar, and which plugin sections are folded. It is shared by
// every viewer of the App.
type sidebarPanelState struct {
	started   bool
	plugins   []sidebarplugin.Item
	collapsed map[string]bool // by plugin file name
	columns   atomic.Int64    // the sidebar's content width, passed to scripts
}

// startSidebarPanel starts running the sidebar plugins until done closes. It
// runs once per App.
func (a *App) startSidebarPanel(done <-chan struct{}) {
	if a.panel == nil {
		a.panel = &sidebarPanelState{}
	}
	if a.panel.started {
		return
	}
	a.panel.started = true
	panel := a.panel
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-done; cancel() }()
	runner := &sidebarplugin.Runner{Dir: sidebarplugin.Dir(config.Path()), Columns: func() int { return int(panel.columns.Load()) }}
	runner.OnChange = func() {
		items := runner.Items()
		a.Do(func() { panel.plugins = items; a.dirty = true })
	}
	go runner.Run(ctx)
}

// sidebarPanel builds the render input for the panel; nil without plugins.
func (a *App) sidebarPanel() *render.SidebarPanel {
	if a.panel == nil || len(a.panel.plugins) == 0 {
		return nil
	}
	p := &render.SidebarPanel{}
	for _, it := range a.panel.plugins {
		sec := render.SidebarPanelSection{Key: it.File, Title: it.Name, Collapsed: a.panel.collapsed[it.File]}
		switch {
		case !it.Ran:
			sec.Lines = []render.SidebarPanelLine{{Text: "…", Error: true}}
		case it.Err != "" && len(it.Lines) == 0:
			sec.Lines = []render.SidebarPanelLine{{Text: it.Err, Error: true}}
		case len(it.Lines) == 0:
			sec.Lines = []render.SidebarPanelLine{{Text: "（出力なし）", Error: true}}
		default:
			for _, line := range it.Lines {
				sec.Lines = append(sec.Lines, render.SidebarPanelLine{Text: line})
			}
		}
		p.Sections = append(p.Sections, sec)
	}
	return p
}

// layoutSidebarPanel decides the panel's rows for this frame.
func (a *App) layoutSidebarPanel() *render.SidebarPanel {
	p := a.sidebarPanel()
	a.st.SidebarPanelRows = 0
	if p == nil || !a.st.SidebarVisible {
		return p
	}
	area := a.st.SidebarArea()
	a.panel.columns.Store(int64(max(0, area.W-2)))
	a.st.SidebarPanelRows = render.SidebarPanelRows(p, max(0, area.H-5))
	return p
}

// sidebarPanelClick folds or opens a plugin's section when its heading is clicked.
func (a *App) sidebarPanelClick(x, y int) bool {
	for _, h := range render.SidebarPanelLayout(a.st, a.sidebarPanel()) {
		if !pointIn(x, y, h.Rect) || strings.TrimSpace(h.Key) == "" {
			continue
		}
		if a.panel.collapsed == nil {
			a.panel.collapsed = map[string]bool{}
		}
		a.panel.collapsed[h.Key] = !a.panel.collapsed[h.Key]
		a.dirty = true
		return true
	}
	return false
}
