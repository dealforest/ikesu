package render

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
)

func decodePanes(t *testing.T, data []byte) nativePanesPayload {
	t.Helper()
	prefix := "\x1b]7767;ikesu-ui;"
	if !strings.HasPrefix(string(data), prefix) || !strings.HasSuffix(string(data), "\x07") {
		t.Fatalf("bad OSC: %q", data)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(string(data), prefix), "\x07"))
	if err != nil {
		t.Fatal(err)
	}
	var p nativePanesPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNativePaneGeometryAndVisibility(t *testing.T) {
	state := model.New(t.TempDir(), 54, 46)
	state.MobileWidthThreshold = 64
	state.PaneBorders = false
	in := Input{State: state}
	var sig NativePaneSignal
	if sig.Next(false, in, 54, 46, false) != nil {
		t.Fatal("disabled")
	}
	p := decodePanes(t, sig.Next(true, in, 54, 46, false))
	if p.Kind != "panes" || p.Cols != 54 || p.Rows != 46 || len(p.Panes) != 1 || p.Panes[0] != (nativePane{ID: state.FocusedPane(), X: 0, Y: 2, W: 54, H: 44}) {
		t.Fatalf("borderless: %+v", p)
	}
	if sig.Next(true, in, 54, 46, false) != nil {
		t.Fatal("repeated")
	}
	state.PaneBorders = true
	p = decodePanes(t, sig.Next(true, in, 54, 46, false))
	if p.Panes[0] != (nativePane{ID: state.FocusedPane(), X: 1, Y: 3, W: 52, H: 42}) {
		t.Fatalf("bordered: %+v", p)
	}
	state.Apply(model.CmdSplitPane{Pane: state.FocusedPane(), Dir: layout.Horizontal, Ratio: 0.5})
	p = decodePanes(t, sig.Next(true, in, 54, 46, false))
	rects := state.DisplayPaneRects()
	if len(p.Panes) != len(rects) || len(rects) != 2 {
		t.Fatalf("split: %+v", p)
	}
	for i, pr := range rects {
		r, _ := model.PaneContentRect(pr.Rect, true)
		if p.Panes[i] != (nativePane{ID: pr.ID, X: r.X, Y: r.Y, W: r.W, H: r.H}) {
			t.Fatalf("split rect: %+v", p)
		}
	}
	p = decodePanes(t, sig.Next(true, in, 25, 46, false))
	for _, r := range p.Panes {
		if r.X+r.W > 25 {
			t.Fatal("clipped", p)
		}
	}
	p = decodePanes(t, sig.Next(true, in, 54, 46, false))
	if len(p.Panes) != 2 {
		t.Fatal("restore after clipping", p)
	}
	in.Dialog = &Dialog{Title: "test"}
	p = decodePanes(t, sig.Next(true, in, 25, 46, false))
	if len(p.Panes) != 0 {
		t.Fatal("modal", p)
	}
	if sig.Next(true, in, 26, 46, false) != nil {
		t.Fatal("modal repeated on resize")
	}
	in.Dialog = nil
	p = decodePanes(t, sig.Next(true, in, 54, 46, false))
	if len(p.Panes) != 2 {
		t.Fatal("restore", p)
	}
	in.MobileSwitcher = true
	p = decodePanes(t, sig.Next(true, in, 54, 46, false))
	if len(p.Panes) != 0 {
		t.Fatal("switcher", p)
	}
}

func TestNativePaneClearsForPopupAndCoveredAndRestores(t *testing.T) {
	s := model.New(t.TempDir(), 56, 44)
	s.MobileWidthThreshold = 64
	in := Input{State: s}
	var sig NativePaneSignal
	first := decodePanes(t, sig.Next(true, in, 56, 44, false))
	if len(first.Panes) != 1 {
		t.Fatal(first)
	}
	s.Apply(model.CmdOpenPopup{WorkspaceID: s.ActiveWorkspace().ID, Width: api.PopupSize{Cells: 20}, Height: api.PopupSize{Cells: 10}})
	if !s.PopupVisible() {
		t.Fatal("popup not visible")
	}
	if p := decodePanes(t, sig.Next(true, in, 56, 44, false)); len(p.Panes) != 0 {
		t.Fatal("popup exposed pane", p)
	}
	if sig.Next(true, in, 56, 44, false) != nil {
		t.Fatal("popup repeated")
	}
	s.Apply(model.CmdClosePopup{Pane: s.Popup.PaneID})
	if p := decodePanes(t, sig.Next(true, in, 56, 44, false)); len(p.Panes) != 1 {
		t.Fatal("popup close", p)
	}
	if p := decodePanes(t, sig.Next(true, in, 56, 44, true)); len(p.Panes) != 0 {
		t.Fatal("client overlay", p)
	}
	if p := decodePanes(t, sig.Next(true, in, 56, 44, false)); len(p.Panes) != 1 {
		t.Fatal("client overlay closed", p)
	}
}
