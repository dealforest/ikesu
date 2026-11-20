package runtime

import (
	"regexp"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"

	"github.com/kazuph/ikesu/internal/weblink"
)

// PaneViewportTop is the first physical buffer row rendered in a pane.
func PaneViewportTop(e *vt.Emulator, off, height int, keepCursor bool) int {
	history, screen := e.ScrollbackLen(), e.Height()
	off = min(max(0, off), history)
	// Another viewer can have a taller shared PTY. Retain a quiet prompt
	// that would be cropped out of the shorter viewer’s bottom window.
	if keepCursor && off == 0 && e.CursorPosition().Y < screen-height {
		return history + max(0, e.CursorPosition().Y-height+1)
	}
	return history + screen - off - height
}

var webURL = regexp.MustCompile(`(?i)https?://[^\s\p{Z}<>"'\x60]+`)

// WebLinkAt takes pane-relative coordinates in the rendered viewport.
// Only the clicked logical line is examined, even in a large scrollback.
func (p *Pane) WebLinkAt(x, y, off, height int, keepCursor bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.webLinkAtLocked(x, PaneViewportTop(p.emu, off, height, keepCursor)+y)
}

func (p *Pane) WebLinkAtBufferRow(x, row int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.webLinkAtLocked(x, row)
}

func (p *Pane) webLinkAtLocked(x, row int) string {
	e := p.emu
	history := e.ScrollbackLen()
	end := history + e.Height()
	if row < 0 || row >= end || x < 0 {
		return ""
	}
	cellAt := func(x, y int) *uv.Cell {
		if y < history {
			return e.ScrollbackCellAt(x, y)
		}
		return e.CellAt(x, y-history)
	}
	cell := cellAt(x, row)
	for cell != nil && cell.Width == 0 && x > 0 {
		x--
		cell = cellAt(x, row)
	}
	if cell != nil && cell.Link.URL != "" {
		if weblink.Valid(cell.Link.URL) {
			return cell.Link.URL
		}
		return ""
	}
	refAt := func(y int) rowRef {
		if p.read == nil {
			return rowRef{}
		}
		if y < history {
			return p.read.historyRef(y)
		}
		return p.read.rows(p.altScreen)[y-history]
	}
	start := row
	// While the alternate screen is displayed, its first row cannot be a
	// continuation of the main scrollback immediately above it.
	floor := 0
	if p.altScreen && row >= history {
		floor = history
	}
	for start > floor && refAt(start).continued {
		start--
	}

	var line strings.Builder
	offset := -1
	for y := start; y < end; y++ {
		width := e.Width()
		if y < history {
			width = len(e.Scrollback().Line(y))
		}
		if recorded := refAt(y).width; recorded > 0 {
			width = min(width, recorded)
		}
		for col := range width {
			if y == row && col == x {
				offset = line.Len()
			}
			c := cellAt(col, y)
			if c == nil || c.Content == "" && c.Width != 0 {
				line.WriteByte(' ')
			} else if c.Width != 0 {
				line.WriteString(c.Content)
			}
		}
		if y+1 == end || (p.altScreen && y+1 == history) || !refAt(y+1).continued {
			break
		}
	}
	text := line.String()
	for _, span := range webURL.FindAllStringIndex(text, -1) {
		if offset < span[0] || offset >= span[1] {
			continue
		}
		raw := text[span[0]:span[1]]
		// Trim closing punctuation from prose only when it has no matching
		// opening delimiter inside the URL.
		for len(raw) > 0 {
			last := raw[len(raw)-1]
			opening := strings.IndexByte(")]}", last)
			if opening < 0 || strings.Count(raw, string(last)) <= strings.Count(raw, "([{"[opening:opening+1]) {
				break
			}
			raw = raw[:len(raw)-1]
		}
		if offset < span[0]+len(raw) && weblink.Valid(raw) {
			return raw
		}
	}
	return ""
}
