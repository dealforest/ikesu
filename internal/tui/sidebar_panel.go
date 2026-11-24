package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/sidebarplugin"
)

// sidebarPanelState holds the sidebar plugins' outputs shown at the bottom of
// the left sidebar, and which plugin sections are folded. It is shared by
// every viewer of the App.
type sidebarPanelState struct {
	done      <-chan struct{}
	cancel    context.CancelFunc
	plugins   []sidebarplugin.Item
	collapsed map[string]bool // by plugin file name
	columns   atomic.Int64    // the sidebar's content width, passed to scripts
	runner    *sidebarplugin.Runner
	rerunAt   time.Time // the last re-run for a width change
	rerunDue  bool      // a width change waits for widthRerunGap to pass
}

// widthRerunGap keeps viewers of different widths (a PC and the phone) from
// re-running the plugins on every frame.
const widthRerunGap = 30 * time.Second

// startSidebarPanel starts running the sidebar plugins until done closes. It
// can be restarted when the config enables them again.
func (a *App) startSidebarPanel(done <-chan struct{}) {
	if a.panel == nil {
		a.panel = &sidebarPanelState{}
	}
	panel := a.panel
	panel.done = done
	if panel.runner != nil || !a.sidebarConfig.SidebarPluginsEnabled() {
		return
	}
	// 最初の実行の前に幅を決める（サイドバーを閉じて起動しても、開いたときの幅を渡す）。
	panel.columns.Store(int64(max(0, a.st.ExpandedSidebarWidth()-2)))
	ctx, cancel := context.WithCancel(context.Background())
	panel.cancel = cancel
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	runner := &sidebarplugin.Runner{Dir: sidebarplugin.Dir(config.Path()), Columns: func() int { return int(panel.columns.Load()) }}
	// 結果は画面に反映する時点で読む。通知が前後しても、古い結果で新しい結果を上書きしない。
	runner.OnChange = func() {
		a.Do(func() {
			if panel.runner == runner {
				panel.plugins = runner.Items()
				a.dirty = true
			}
		})
	}
	panel.runner = runner
	go runner.Run(ctx)
}

func (a *App) updateSidebarPanel() {
	panel := a.panel
	if panel == nil || panel.done == nil {
		return
	}
	if a.sidebarConfig.SidebarPluginsEnabled() {
		a.startSidebarPanel(panel.done)
		return
	}
	if panel.cancel != nil {
		panel.cancel()
		panel.cancel = nil
		panel.runner = nil
		panel.plugins = nil
		panel.rerunDue = false
		panel.rerunAt = time.Time{}
		a.dirty = true
	}
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
	if a.panel != nil {
		// 幅はサイドバーを開いたときの幅。変わったら、新しい幅で全部のプラグインを実行し直す。
		if cols := int64(max(0, a.st.ExpandedSidebarWidth()-2)); a.panel.columns.Swap(cols) != cols {
			a.panel.rerunDue = true
		}
		// 30秒より短い間に幅が続けて変わっても、最後の幅で必ず1回は実行し直す。
		if a.panel.rerunDue && a.panel.runner != nil && time.Since(a.panel.rerunAt) >= widthRerunGap {
			a.panel.rerunDue = false
			a.panel.rerunAt = time.Now()
			a.panel.runner.RerunAll()
		}
	}
	if p == nil || !a.st.SidebarVisible {
		return p
	}
	area := a.st.SidebarArea()
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
