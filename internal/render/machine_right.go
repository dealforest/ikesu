package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
)

// RightAgentRows is the full endpoint card sequence before viewport clipping.
func RightAgentRows(in Input) []SidebarRow {
	cfg := in.SidebarConfig
	if in.ServerUI && len(cfg.UI.Sidebar.Agents.Rows) == 0 {
		cfg.UI.Sidebar.Agents.Rows = [][]config.SidebarToken{{{Token: "workspace"}}, {{Token: "agent"}}}
	}
	rows := SidebarRows(in.State, in.Panes, cfg)
	for i, row := range rows {
		if row.Kind == RowSep {
			return rows[i+1:]
		}
	}
	return nil
}

type machineRightRow struct {
	group  int
	row    SidebarRow
	header bool
	rule   bool
	text   string
}

func (m *MachineSidebar) rightRows(active Input) []machineRightRow {
	var rows []machineRightRow
	limit := max(model.SidebarMinDefault, active.State.RightSidebarArea().W) - 1

	for gi, group := range m.Groups {
		// Like the space list's machine headings, each heading sits between two
		// rules; a folded machine's lower rule is shared with the next heading.
		if len(rows) == 0 || !rows[len(rows)-1].rule {
			rows = append(rows, machineRightRow{group: gi, rule: true})
		}
		glyph := "▾ "
		if m.RightCollapsed[group.ID] {
			glyph = "▸ "
		}
		rows = append(rows, machineRightRow{group: gi, header: true, text: glyph + group.Label + ": " + group.Status}, machineRightRow{group: gi, rule: true})
		if group.Reason != "" && (group.Status == "awaiting approval" || group.Status == "承認待ち。クリックで承認ページを開く") {
			for _, text := range []string{group.Status, group.Reason} {
				line, width := "", 0
				for _, r := range text {
					w := ansi.StringWidth(string(r))
					if width+w > limit || r == '\n' {
						rows = append(rows, machineRightRow{group: gi, header: true, text: line})
						line, width = "", 0
					}
					if r != '\n' {
						line += string(r)
						width += w
					}
				}
				rows = append(rows, machineRightRow{group: gi, header: true, text: line})
			}
		}
		if group.Input == nil || m.RightCollapsed[group.ID] {
			continue
		}
		var source []SidebarRow
		if active.JobsPanel {
			for i := range group.Input.Jobs {
				source = append(source, SidebarRow{Kind: RowJob, Line: i})
			}
		} else {
			source = RightAgentRows(*group.Input)
		}
		for _, row := range source {
			rows = append(rows, machineRightRow{group: gi, row: row})
		}
	}
	return rows
}
func (m *MachineSidebar) drawRight(f *Frame, active Input) {
	area := RightSidebarListRect(active.State, active.RightSidebarPanel)
	if active.State.RightSidebarArea().W == 0 {
		return
	}
	rows := m.rightRows(active)
	m.RightMaxScroll = max(0, len(rows)-area.H)
	m.RightScroll = max(0, min(m.RightScroll, m.RightMaxScroll))
	f.Fill(area.X, area.Y, area.W, area.H, " ", uv.Style{Fg: f.Palette.Text, Bg: f.Palette.PopupBG})
	for n := 0; n < area.H && n+m.RightScroll < len(rows); n++ {
		entry := rows[n+m.RightScroll]
		group := m.Groups[entry.group]
		y := area.Y + n
		if entry.rule {
			f.Fill(area.X, y, area.W, 1, "─", uv.Style{Fg: f.Palette.Dim, Bg: f.Palette.PopupBG})
			continue
		}
		hit := MachineHit{Machine: group.ID, Row: entry.row, Header: entry.header, Right: true, Rect: layout.Rect{X: area.X, Y: y, W: area.W, H: 1}}
		m.Hits = append(m.Hits, hit)
		if entry.header {
			if area.W <= 1 {
				continue
			}
			if glyph, label, ok := strings.Cut(entry.text, " "); ok && (glyph == "▾" || glyph == "▸") {
				// Drawn like the space list's machine headings.
				f.Text(area.X, y, glyph, uv.Style{Fg: f.Palette.Accent, Bg: f.Palette.PopupBG}, area.X+area.W)
				f.Text(area.X+2, y, truncate(label, max(0, area.W-2)), uv.Style{Fg: f.Palette.Dim, Bg: f.Palette.PopupBG, Attrs: uv.AttrBold}, area.X+area.W)
				continue
			}
			f.Text(area.X, y, entry.text, uv.Style{Fg: f.Palette.Text, Bg: f.Palette.PopupBG}, area.X+area.W)
			continue
		}
		in := *group.Input

		drawSidebarRow(f, in.State, in, entry.row, area.X, y, area.W, false)
		machineRowBackground(f, area.X, y, area.W)
		if in.SidebarConfig.UI.TransparentSidebarCards {
			clearSidebarCardBackground(f, in.State, entry.row, area.X, y, area.W)
		}
	}
}
