package tui

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/sidebarplugin"
	"github.com/kazuph/ikesu/internal/usage"
)

// usageRefresh is how often the usage limits are read again (AgentPal reads
// them every 30 seconds).
const usageRefresh = 30 * time.Second

// sidebarPanelState holds the usage rows and plugin outputs shown at the
// bottom of the left sidebar, and whether each section is folded.
type sidebarPanelState struct {
	started          bool
	usage            []usage.Row
	hasUsage         bool
	plugins          []sidebarplugin.Item
	usageCollapsed   bool
	pluginsCollapsed bool
}

// startSidebarPanel starts reading usage and running the sidebar plugins until
// done closes. It runs once per App.
func (a *App) startSidebarPanel(done <-chan struct{}) {
	if a.panel == nil {
		a.panel = &sidebarPanelState{}
	}
	if a.panel.started {
		return
	}
	a.panel.started = true
	func() {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-done; cancel() }()
		home, _ := os.UserHomeDir()
		go func() {
			client := &http.Client{Timeout: 8 * time.Second}
			var codex *usage.Codex
			publish := func(claude *usage.Claude) {
				rows := usage.Rows(claude, codex)
				known := false
				for _, r := range rows {
					known = known || r.Bucket != nil
				}
				a.Do(func() {
					a.panel.usage, a.panel.hasUsage = rows, known
					a.dirty = true
				})
			}
			for {
				// Claude のキャッシュは手元のファイルなのですぐ出す。Codex は問い合わせが遅いことがあるので後から足す。
				claude := usage.ReadClaude(usage.ClaudeHome(os.Getenv, home), time.Now())
				publish(claude)
				if c := usage.ReadCodex(ctx, client, usage.CodexHome(os.Getenv, home), time.Now()); c != nil {
					codex = c
				}
				publish(claude)
				select {
				case <-ctx.Done():
					return
				case <-time.After(usageRefresh):
				}
			}
		}()
		runner := &sidebarplugin.Runner{Dir: sidebarplugin.Dir(config.Path())}
		runner.OnChange = func() {
			items := runner.Items()
			a.Do(func() { a.panel.plugins = items; a.dirty = true })
		}
		go runner.Run(ctx)
	}()
}

// sidebarPanel builds the render input for the panel; nil when there is
// nothing to show (no usage data and no plugins).
func (a *App) sidebarPanel() *render.SidebarPanel {
	if a.panel == nil {
		return nil
	}
	p := &render.SidebarPanel{Now: time.Now()}
	if a.panel.hasUsage {
		sec := render.SidebarPanelSection{Key: "usage", Title: "使用量", Collapsed: a.panel.usageCollapsed}
		for i := range a.panel.usage {
			row := a.panel.usage[i]
			sec.Lines = append(sec.Lines, render.SidebarPanelLine{Usage: &row})
		}
		p.Sections = append(p.Sections, sec)
	}
	if len(a.panel.plugins) > 0 {
		sec := render.SidebarPanelSection{Key: "plugins", Title: "プラグイン", Collapsed: a.panel.pluginsCollapsed}
		for _, it := range a.panel.plugins {
			line := render.SidebarPanelLine{Text: it.Text}
			switch {
			case !it.Ran:
				line = render.SidebarPanelLine{Text: it.Name + ": …", Error: true}
			case it.Err != "" && it.Text == "":
				line = render.SidebarPanelLine{Text: it.Name + ": " + it.Err, Error: true}
			case it.Text == "":
				line = render.SidebarPanelLine{Text: it.Name + ": （出力なし）", Error: true}
			}
			sec.Lines = append(sec.Lines, line)
		}
		p.Sections = append(p.Sections, sec)
	}
	if len(p.Sections) == 0 {
		return nil
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
	a.st.SidebarPanelRows = render.SidebarPanelRows(p, max(0, area.H-5))
	return p
}

// sidebarPanelClick folds or opens a section when its heading is clicked.
func (a *App) sidebarPanelClick(x, y int) bool {
	for _, h := range render.SidebarPanelLayout(a.st, a.sidebarPanel()) {
		if !pointIn(x, y, h.Rect) {
			continue
		}
		switch h.Key {
		case "usage":
			a.panel.usageCollapsed = !a.panel.usageCollapsed
		case "plugins":
			a.panel.pluginsCollapsed = !a.panel.pluginsCollapsed
		}
		a.dirty = true
		return true
	}
	return false
}
