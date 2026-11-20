package render

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/model"
	"strings"
	"testing"
)

func TestPhoneTabRowsGeometryAndBackground(t *testing.T) {
	for _, phone := range []bool{false, true} {
		for _, show := range []bool{false, true} {
			s := model.New(t.TempDir(), 100, 35)
			s.PhoneTabRows = phone
			s.ShowTabBar = show
			s.Apply(model.CmdNewTab{Workspace: 0})
			s.ActiveWorkspace().Tabs[0].Name = "firsttab"
			out := Compose(Input{State: s})
			height := 0
			if show {
				height = 1
				if phone {
					height = 2
				}
			}
			if s.TabBarArea().H != height || s.PanesArea().Y != height {
				t.Fatalf("phone=%v show=%v tab=%+v panes=%+v", phone, show, s.TabBarArea(), s.PanesArea())
			}
			if !show {
				if len(out.TabHits) != 0 {
					t.Fatal(out.TabHits)
				}
				continue
			}
			hit := out.TabHits[0]
			if hit.H != height {
				t.Fatal("hit", hit)
			}
			if got := strings.Count(strings.Split(screenText(t, out, s.Cols, s.Rows), "\n")[0], "firsttab"); got != 1 {
				t.Fatal("label count", got)
			}
			if phone {
				for x := hit.X; x < hit.X+hit.W; x++ {
					a, b := out.Frame.Cell(x, 0), out.Frame.Cell(x, 1)
					if a.Style.Bg != b.Style.Bg || b.Content != " " {
						t.Fatalf("column %d backgrounds or text %#v %#v", x, a, b)
					}
				}
			}
		}
	}
}

func TestPhoneSwitcherTabRowsAndScroll(t *testing.T) {
	for _, phone := range []bool{false, true} {
		s := model.New(t.TempDir(), 54, 20)
		s.MobileWidthThreshold = 64
		s.PhoneTabRows = phone
		s.Apply(model.CmdNewTab{Workspace: 0})
		s.ActiveWorkspace().Tabs[0].Name = "firsttab"
		rows := MobileRows(s, nil)
		var index []int
		for i, r := range rows {
			if r.Kind == RowMobileTab && r.Line == 0 {
				index = append(index, i)
			}
		}
		expected := 1
		if phone {
			expected = 2
		}
		if len(index) != expected {
			t.Fatal(phone, index)
		}
		if phone && (index[1] != index[0]+1 || !rows[index[1]].Continuation) {
			t.Fatal(rows)
		}
		out := Compose(Input{State: s, MobileSwitcher: true, MobileScroll: index[0] - 1})
		if got := strings.Count(screenText(t, out, s.Cols, s.Rows), "firsttab"); got != 1 {
			t.Fatal("label count", got)
		}
		if phone {
			off := max(0, min(index[0]-1, max(0, len(rows)-MobileViewport(s).H)))
			y := MobileViewport(s).Y + index[0] - off
			for x := 1; x < s.Cols; x++ {
				a, b := out.Frame.Cell(x, y), out.Frame.Cell(x, y+1)
				if a.Style.Bg != b.Style.Bg || b.Content != " " {
					t.Fatalf("row backgrounds/text at %d %+v %+v", x, a, b)
				}
			}
		}
	}
}

func TestPhoneTabStripLeavesPaneCellsUncovered(t *testing.T) {
	s := model.New(t.TempDir(), 100, 35)
	s.PhoneTabRows = true
	s.PaneBorders = false
	pid := s.FocusedPane()
	in := Input{State: s, Panes: map[int]*PaneSnap{pid: {Grid: [][]uv.Cell{{{Content: "X", Width: 1}}}}}}
	out := Compose(in)
	area := s.PanesArea()
	if area.Y != 2 {
		t.Fatal(area)
	}
	if c := out.Frame.Cell(area.X, area.Y); c == nil || c.Content != "X" {
		t.Fatalf("pane starts under tabs: %v", c)
	}
}

func TestPhoneTabVisibilityAndHorizontalScroll(t *testing.T) {
	for _, phone := range []bool{false, true} {
		s := model.New(t.TempDir(), 80, 30)
		s.PhoneTabRows = phone
		s.HideTabBarWhenSingleTab = true
		if out := Compose(Input{State: s}); s.TabBarArea().H != 0 || len(out.TabHits) != 0 || s.PanesArea().Y != 0 {
			t.Fatalf("single hidden: %v %+v", phone, s.TabBarArea())
		}
		s.HideTabBarWhenSingleTab = false
		for i := 0; i < 12; i++ {
			s.Apply(model.CmdNewTab{Workspace: 0})
		}
		for _, offset := range []int{0, 20, 80} {
			out := Compose(Input{State: s, TabScroll: offset, TabScrollManual: true})
			h := 1
			if phone {
				h = 2
			}
			if s.PanesArea().Y != h {
				t.Fatal("pane overlap", s.PanesArea())
			}
			seen := 0
			for _, r := range out.TabHits {
				if r.W > 0 {
					seen++
					if r.H != h || r.Y != 0 || r.X < s.TabBarArea().X || r.X+r.W > s.TabBarArea().X+s.TabBarArea().W {
						t.Fatal("bad visible tab", r)
					}
				}
			}
			if seen == 0 {
				t.Fatal("scroll hid all tabs", offset)
			}
		}
	}
}
