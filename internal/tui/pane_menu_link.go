package tui

import "github.com/kazuph/ikesu/internal/weblink"

func (a *App) paneMenuLinkAt(x, y, pid int) string {
	if a.sup == nil || a.sidebarConfig.UI.NativeMenus {
		return ""
	}
	pane, ok := a.paneAt(x, y)
	if !ok || pane.ID != pid {
		return ""
	}
	if p := a.sup.Get(pid); p != nil {
		if view, ok := a.paintedPaneViews[pid]; ok && (a.cm == nil || a.cm.pane != pid) {
			if !pointIn(x, y, view.Inner) {
				return ""
			}
			x, y := x-view.Inner.X, y-view.Inner.Y
			if view.Links != nil {
				url := weblink.At(view.Links.Menu, x, y)
				if weblink.Valid(url) {
					return url
				}
				return ""
			}
			return p.WebLinkAtBufferRow(x, view.BufferTop+y)
		}
		inner := a.innerRect(pid)
		if !pointIn(x, y, inner) {
			return ""
		}
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
