package client

import (
	"fmt"
	"testing"
)

func TestPresetFirstCellAvoidsDividerWithRealEndpoint(t *testing.T) {
	h := newRightMachineHarness(t)
	m := h.m
	e := m.endpoints["local"]
	if err := m.paint(); err != nil {
		t.Fatal(err)
	}
	preset := m.regions.ActionBar.Preset
	sidebar := e.snapshot.State.SidebarArea()
	if preset.W == 0 || preset.X != sidebar.X+sidebar.W {
		t.Fatalf("preset %+v not at pane edge %+v", preset, sidebar)
	}
	before := e.snapshot.State.SidebarArea().W
	for _, final := range []string{"M", "m"} {
		raw := fmt.Sprintf("\x1b[<0;%d;%d%s", preset.X+1, preset.Y+1, final)
		if err := m.input(mouseEvent(t, raw)); err != nil {
			t.Fatal(err)
		}
		if m.drag != nil {
			t.Fatalf("preset %s captured as divider drag", final)
		}
	}
	h.until("preset first cell changes width", func() bool { return e.snapshot.State.SidebarArea().W != before })
	if err := m.paint(); err != nil {
		t.Fatal(err)
	}
	sidebar = e.snapshot.State.SidebarArea()
	for _, point := range []struct{ x, y int }{
		{sidebar.X + sidebar.W - 1, sidebar.Y + sidebar.H - 1},
		{sidebar.X + sidebar.W, sidebar.Y + sidebar.H - 2},
	} {
		for _, final := range []string{"M", "m"} {
			raw := fmt.Sprintf("\x1b[<0;%d;%d%s", point.x+1, point.y+1, final)
			if err := m.input(mouseEvent(t, raw)); err != nil {
				t.Fatal(err)
			}
			if final == "M" && (m.drag == nil || !m.drag.divider) {
				t.Fatalf("divider press at %+v was not captured", point)
			}
			if final == "m" && m.drag != nil {
				t.Fatalf("divider release at %+v was not cleared", point)
			}
		}
	}
}
