package tui

func (a *App) paneMenuLinkAt(x, y, pid int) string {
	if a.sup == nil || a.sidebarConfig.UI.NativeMenus {
		return ""
	}
	pane, ok := a.paneAt(x, y)
	if !ok || pane.ID != pid {
		return ""
	}
	inner := a.innerRect(pid)
	if !pointIn(x, y, inner) {
		return ""
	}
	if p := a.sup.Get(pid); p != nil {
		if a.cm != nil && a.cm.pane == pid {
			return p.WebLinkAtBufferRow(x-inner.X, a.cm.top+y-inner.Y)
		}
		off := a.scroll[pid]
		if parked := a.paneCopies[pid]; parked != nil {
			off = max(a.bufferLen(p)-inner.H-parked.top, 0)
		}
		return p.WebLinkAt(x-inner.X, y-inner.Y, off, inner.H, a.viewerContext != nil)
	}
	return ""
}
