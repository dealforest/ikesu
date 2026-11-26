package tui

import (
	"github.com/kazuph/ikesu/internal/benchstage"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"

	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/runtime"
)

// paneGrid extracts the innerH×w cells a pane displays at scroll offset off
// (0 = live screen bottom). Rows come from scrollback followed by screen.
func (a *App) paneGrid(p *runtime.Pane, off, h, w int) [][]uv.Cell {
	var snap render.PaneSnap
	a.fillPaneGrid(&snap, p, off, layout.Rect{W: w, H: h})
	return snap.Grid
}

func (a *App) fillPaneGrid(snap *render.PaneSnap, p *runtime.Pane, off int, inner layout.Rect) {
	defer benchstage.Span("ui.panegrid")()
	h, w := inner.H, inner.W
	cw, ch := p.CellPixels()
	benchstage.Values("panegrid.materialized", h*w, 0)
	var g [][]uv.Cell
	p.WithEmu(func(e *vt.Emulator) {
		sb := e.ScrollbackLen()
		eh := e.Height()
		top := runtime.PaneViewportTop(e, off, h, a.viewerContext != nil)
		snap.Viewport = render.PaneViewport{Inner: inner, BufferTop: top, ScreenTop: top - sb, Cols: e.Width(), Rows: eh, CellW: cw, CellH: ch}
		for r := 0; r < h; r++ {
			i := top + r
			row := make([]uv.Cell, w)
			if vt.ExperimentalFlatScreen && i >= sb && i-sb < eh {
				e.CopyScreenRow(row, 0, i-sb)
				g = append(g, row)
				continue
			}
			for x := 0; x < w; x++ {
				var c *uv.Cell
				switch {
				case i < 0:
				case i < sb:
					c = e.ScrollbackCellAt(x, i)
				case i-sb < eh:
					c = e.CellAt(x, i-sb)
				}
				if c != nil {
					row[x] = *c
				} else {
					row[x] = uv.Cell{Content: " ", Width: 1}
				}
			}
			g = append(g, row)
		}
		if benchstage.Enabled {
			info := e.ScreenStorage()
			benchstage.Values("screen.cell.bytes", p.ID, info.CellBytes)
			benchstage.Values("screen.cell.capacity", p.ID, info.CellCapacity)
			benchstage.Values("screen.ids.arraybytes", p.ID, info.IDArrayBytes)
			benchstage.Values("screen.ids.live", p.ID, info.LiveIDs)
		}
	})
	snap.Grid = g
}

// bufferLen returns the total number of stored lines (scrollback + screen).
func (a *App) bufferLen(p *runtime.Pane) int {
	n := 0
	p.WithEmu(func(e *vt.Emulator) {
		n = e.ScrollbackLen() + e.Height()
	})
	return n
}

// bufferLine returns the cells of buffer line i (scrollback indices first).
func (a *App) bufferLine(p *runtime.Pane, i int) []uv.Cell {
	var row []uv.Cell
	p.WithEmu(func(e *vt.Emulator) {
		sb := e.ScrollbackLen()
		w := e.Width()
		row = make([]uv.Cell, w)
		for x := 0; x < w; x++ {
			var c *uv.Cell
			if i < sb {
				c = e.ScrollbackCellAt(x, i)
			} else {
				c = e.CellAt(x, i-sb)
			}
			if c != nil {
				row[x] = *c
			} else {
				row[x] = uv.Cell{Content: " ", Width: 1}
			}
		}
	})
	return row
}

// viewTop returns the buffer index of the first visible row for a pane shown
// at scroll offset off with inner height h.
func viewTop(p *runtime.Pane, off, h int) int {
	top := 0
	p.WithEmu(func(e *vt.Emulator) {
		sb := e.ScrollbackLen()
		end := sb + e.Height() - 1 - off
		top = end - h + 1
	})
	return top
}

// selRender converts the in-progress selection (buffer coords) into render
// coordinates of the pane's inner rect.
func (a *App) selRender() *render.Selection {
	s := a.sel
	p := a.sup.Get(s.pane)
	if p == nil {
		return nil
	}
	top := viewTop(p, a.scroll[s.pane], s.inner.H)
	// Normalize so From is before To in reading order.
	ax, ay, hx, hy := s.ax, s.ay, s.hx, s.hy
	if ay > hy || (ay == hy && ax > hx) {
		ax, ay, hx, hy = hx, hy, ax, ay
	}
	return &render.Selection{
		Pane:  s.pane,
		FromX: ax, FromY: ay - top,
		ToX: hx, ToY: hy - top,
	}
}

// selText extracts the selected text (trailing blanks trimmed per line) and
// the character count.
func (a *App) selText(s *selState) (string, int) {
	p := a.sup.Get(s.pane)
	if p == nil {
		return "", 0
	}
	ax, ay, hx, hy := s.ax, s.ay, s.hx, s.hy
	if ay > hy || (ay == hy && ax > hx) {
		ax, ay, hx, hy = hx, hy, ax, ay
	}
	var sb strings.Builder
	chars := 0
	for i := ay; i <= hy; i++ {
		row := a.bufferLine(p, i)
		x0, x1 := 0, len(row)-1
		if i == ay {
			x0 = ax
		}
		if i == hy {
			x1 = hx
		}
		if x1 >= len(row) {
			x1 = len(row) - 1
		}
		if x0 > x1 || x0 < 0 {
			if i < hy {
				sb.WriteByte('\n')
			}
			continue
		}
		var line strings.Builder
		for x := x0; x <= x1; x++ {
			c := row[x]
			if c.Width == 0 {
				continue // placeholder of a wide glyph; head emitted it
			}
			if c.Content == "" {
				line.WriteByte(' ')
			} else {
				line.WriteString(c.Content)
			}
		}
		txt := strings.TrimRight(line.String(), " \t")
		chars += len([]rune(txt))
		sb.WriteString(txt)
		if i < hy {
			sb.WriteByte('\n')
		}
	}
	return sb.String(), chars
}

func (a *App) livePaneViewport(p *runtime.Pane, inner layout.Rect) render.PaneViewport {
	cw, ch := p.CellPixels()
	view := render.PaneViewport{Inner: inner, CellW: cw, CellH: ch}
	p.WithEmu(func(e *vt.Emulator) {
		view.BufferTop = runtime.PaneViewportTop(e, a.scroll[p.ID], inner.H, a.viewerContext != nil)
		view.ScreenTop = view.BufferTop - e.ScrollbackLen()
		view.Cols, view.Rows = e.Width(), e.Height()
	})
	return view
}
