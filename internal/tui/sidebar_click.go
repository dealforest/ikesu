package tui

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
)

// sidebarClick handles clicks inside the sidebar region.
func (a *App) sidebarClick(m uv.Mouse) {
	if m.Button == uv.MouseRight {
		a.sidebarRightClick(m)
		return
	}
	if m.Button != uv.MouseLeft {
		return
	}
	if a.sidebarPanelClick(m.X, m.Y) {
		return
	}
	if a.sidebarPress(m) {
		return
	}
	s := a.st
	if s.SidebarVisible && !a.serverUI && pointIn(m.X, m.Y, render.SidebarToggleRect(s)) {
		a.exec(model.CmdToggleSidebar{})
		return
	}

	if pointIn(m.X, m.Y, a.sidebarMenuRect(a.serverUI)) {
		a.openMainMenu(0, 0)
		a.positionMainMenu(a.serverUI)
		return
	}
	if a.legacySidebarControls && pointIn(m.X, m.Y, render.LegacySidebarNewRect(s)) {
		a.newWorkspace()
		return
	}
	r, ok := a.sidebarRowAt(m.X, m.Y)
	if !ok {
		return
	}

	switch r.Kind {
	case render.RowWorkspace:
		if r.Ws == s.Active {
			a.openWorkspaceMenu(m.X, m.Y, r.Ws)
		} else {
			a.exec(model.CmdSelectWorkspace{Index: r.Ws})
		}
	case render.RowAgent:
		a.exec(model.CmdSelectWorkspace{Index: r.Ws})
		a.exec(model.CmdFocusPane{Pane: r.Pane})
	}
}

// Card padding retains its workspace owner; empty panel rows stay inert.
func (a *App) sidebarRightClick(m uv.Mouse) {
	s := a.st
	if pointIn(m.X, m.Y, a.sidebarMenuRect(a.serverUI)) ||
		a.legacySidebarControls && pointIn(m.X, m.Y, render.LegacySidebarNewRect(s)) {
		return
	}
	r, ok := a.sidebarRowAt(m.X, m.Y)
	if ok && (r.Kind == render.RowWorkspace || r.Kind == render.RowGit || r.Kind == render.RowCardBlank) {
		a.openWorkspaceMenu(m.X, m.Y, r.Ws)
	}
}
