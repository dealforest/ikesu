package render

import (
	"strings"
	"testing"
)

// At every sidebar width the remote machine header that keeps a click target
// also shows its ▾/▸ in the composed frame, including the one-column case.
func TestMachineHeaderGlyphVisibleAtEveryWidth(t *testing.T) {
	in := continuousInput(t, true, false)
	for _, collapsed := range []bool{false, true} {
		want := "▾"
		if collapsed {
			want = "▸"
		}
		m := MachineSidebar{Groups: []MachineGroup{{ID: "local", Label: "LOCAL", Input: &in}, {ID: "remote", Label: "REMOTE", Input: &in}}, Active: "local", Collapsed: map[string]bool{"remote": collapsed}}
		for _, w := range []int{26, 18, 10, 5, 3, 2} {
			continuousWidth(in, w)
			f := ComposeMachines(in, &m).Frame
			found := false
			for _, hit := range m.Hits {
				if hit.Right || !hit.Header || hit.Machine != "remote" {
					continue
				}
				found = true
				if got := f.Cell(hit.Rect.X, hit.Rect.Y).Content; got != want {
					t.Fatalf("collapsed=%t width=%d header W=%d shows %q, want %q", collapsed, w, hit.Rect.W, got, want)
				}
			}
			if !found {
				t.Fatalf("collapsed=%t width=%d remote header target missing", collapsed, w)
			}
		}
	}
}

func TestMachineConnectButtonMatchesNarrowHeaderHit(t *testing.T) {
	t.Parallel()
	in := continuousInput(t, true, false)
	m := MachineSidebar{Groups: []MachineGroup{
		{ID: "local", Label: "LOCAL", Input: &in},
		{ID: "remote", Label: "REMOTE", Status: "disconnected"},
	}, Active: "local"}
	for _, width := range []int{26, 18, 17, 14, 11, 10, 5} {
		continuousWidth(in, width)
		frame := ComposeMachines(in, &m).Frame
		found := false
		for _, hit := range m.Hits {
			if hit.Machine != "remote" || hit.Right || !(hit.Header || hit.Connect) {
				continue
			}
			var text strings.Builder
			for x := hit.Rect.X; x < hit.Rect.X+hit.Rect.W; x++ {
				text.WriteString(frame.Cell(x, hit.Rect.Y).Content)
			}
			if hit.Header && width < 11 && strings.Contains(text.String(), "[") {
				t.Fatalf("sidebar width %d draws a partial button in %q", width, text.String())
			}
			if hit.Connect && text.String() != "[connect]" {
				t.Fatalf("sidebar width %d draws %q at the connect target", width, text.String())
			}
			found = found || hit.Connect
		}
		if found != (width >= 11) {
			t.Fatalf("sidebar width %d: connect target present = %t", width, found)
		}
	}
}
