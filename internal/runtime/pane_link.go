package runtime

import (
	"bytes"
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

// WithViewport reads the grid and its menu links under the same emulator lock.
func (p *Pane) WithViewport(off, height, width int, keepCursor, menuLinks bool, read func(*vt.Emulator, int)) []weblink.Span {
	p.mu.Lock()
	defer p.mu.Unlock()
	top := PaneViewportTop(p.emu, off, height, keepCursor)
	read(p.emu, top)
	if !menuLinks {
		return nil
	}
	return p.webLinksLocked(top, height, width)
}

func (p *Pane) webLinkAtLocked(x, row int) string {
	if x < 0 || row < 0 {
		return ""
	}
	url := weblink.At(p.webLinksLocked(row, 1, x+1), x, 0)
	if !weblink.Valid(url) {
		return ""
	}
	return url
}

func (p *Pane) webLinksLocked(top, height, width int) []weblink.Span {
	e := p.emu
	history := e.ScrollbackLen()
	end := history + e.Height()
	first, last := max(top, 0), min(top+height, end)
	if first >= last || width <= 0 {
		return nil
	}
	cellAt := func(x, y int) *uv.Cell {
		if y < history {
			return e.ScrollbackCellAt(x, y)
		}
		return e.CellAt(x, y-history)
	}
	refAt := func(y int) rowRef {
		if p.read == nil {
			return rowRef{}
		}
		if y < history {
			return p.read.historyRef(y)
		}
		rows := p.read.rows(p.altScreen)
		if y-history < len(rows) {
			return rows[y-history]
		}
		return rowRef{}
	}
	rowWidth := func(y int) int {
		if y < history {
			return len(e.Scrollback().Line(y))
		}
		return e.Width()
	}
	textWidth := func(y int) int {
		width := rowWidth(y)
		if recorded := refAt(y).width; recorded > 0 {
			width = min(width, recorded)
		}
		return width
	}
	continued := func(y int) bool {
		return y > 0 && y < end && !(p.altScreen && y == history) && refAt(y).continued
	}
	var explicit, plain []weblink.Span

	// Inspect whole soft-wrapped lines touching the viewport so a visible
	// continuation still resolves to the complete URL.
	start := first
	for continued(start) {
		start--
	}
	var text []byte
	for start < last {
		stop := start + 1
		for continued(stop) {
			stop++
		}
		text = text[:0]
		for y := start; y < stop; y++ {
			n := textWidth(y)
			visibleCols := 0
			if y >= first && y < last {
				visibleCols = min(width, rowWidth(y))
			}
			for x := 0; x < max(n, visibleCols); x++ {
				c := cellAt(x, y)
				if x < visibleCols && c != nil && c.Width > 0 && c.Link.URL != "" {
					explicit = weblink.AppendSpan(explicit, weblink.Span{Row: y - top, Start: x, End: min(x+c.Width, visibleCols), URL: c.Link.URL})
				}
				if x >= n || c != nil && c.Width == 0 {
					continue
				}
				if c == nil || c.Content == "" {
					text = append(text, ' ')
				} else {
					text = append(text, c.Content...)
				}
			}
		}
		if bytes.Contains(text, []byte("://")) {
			var matches []weblink.Span // Start and End are byte offsets in text.
			for _, match := range webURL.FindAllIndex(text, -1) {
				if url := trimWebURL(string(text[match[0]:match[1]])); weblink.Valid(url) {
					matches = append(matches, weblink.Span{Start: match[0], End: match[0] + len(url), URL: url})
				}
			}
			offset, match := 0, 0
			for y := start; y < stop && match < len(matches); y++ {
				for x, n := 0, textWidth(y); x < n && match < len(matches); x++ {
					c := cellAt(x, y)
					if c != nil && c.Width == 0 {
						continue
					}
					for match < len(matches) && offset >= matches[match].End {
						match++
					}
					if match < len(matches) && offset >= matches[match].Start && y >= first && y < last && x < width {
						end := x + 1
						if c != nil {
							end = min(x+c.Width, width)
						}
						plain = weblink.AppendSpan(plain, weblink.Span{Row: y - top, Start: x, End: end, URL: matches[match].URL})
					}
					if c == nil || c.Content == "" {
						offset++
					} else {
						offset += len(c.Content)
					}
				}
			}
		}
		start = stop
	}
	return append(explicit, plain...)
}

// Preserve paired delimiters in URLs while trimming trailing prose punctuation.
func trimWebURL(raw string) string {
	for len(raw) > 0 {
		last := raw[len(raw)-1]
		opening := strings.IndexByte(")]}", last)
		if opening < 0 || strings.Count(raw, string(last)) <= strings.Count(raw, "([{"[opening:opening+1]) {
			break
		}
		raw = raw[:len(raw)-1]
	}
	return raw
}
