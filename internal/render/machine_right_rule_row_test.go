package render

import (
	"strings"
	"testing"

	"github.com/kazuph/ikesu/internal/model"
)

// The right list's first rule sits on the pane's top border row, like the
// space list's first rule, except where a narrow header wraps its sort button
// onto that row; the sort button must stay visible there.
func TestMachineRightFirstRuleRowKeepsSortButton(t *testing.T) {
	for _, width := range []int{18, 20, 26, 40, 72} {
		for _, jobs := range []bool{false, true} {
			s := model.New("/tmp/right-rule", 240, 44)
			s.SidebarVisible, s.SidebarSize, s.RightSidebarSize = true, 26, width
			in := Input{State: s, JobsPanel: jobs, Panes: map[int]*PaneSnap{}}
			m := MachineSidebar{Active: "local", Groups: []MachineGroup{{ID: "local", Label: "Local", Status: "online", Input: &in}, {ID: "remote", Label: "Remote", Status: "online", Input: &in}}}
			f := ComposeMachines(in, &m).Frame
			a := s.RightSidebarArea()
			row := func(y int) string {
				var b strings.Builder
				for x := a.X; x < a.X+a.W; x++ {
					b.WriteString(f.Cell(x, y).Content)
				}
				return b.String()
			}
			sort := RightSidebarHeaderFor(s, a.Y).Sort
			wrapped := !jobs && sort.W > 0 && sort.Y == a.Y+1
			if wrapped && !strings.Contains(row(a.Y+1), SidebarAgentSortLabel(s)) {
				t.Fatalf("width %d: sort button covered: %q", width, row(a.Y+1))
			}
			first := a.Y + 1
			if wrapped {
				first = a.Y + 2
			}
			if !strings.Contains(row(first), "──") {
				t.Fatalf("width %d jobs %v: first rule not on row %d: %q / %q", width, jobs, first, row(a.Y+1), row(a.Y+2))
			}
			for _, hit := range m.Hits {
				if hit.Right && hit.Rect.Y < first {
					t.Fatalf("width %d: hit above the list: %+v", width, hit)
				}
			}
		}
	}
}
