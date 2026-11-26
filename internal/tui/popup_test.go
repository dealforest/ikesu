package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/teststate"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestG2PopupOwnerInputPasteLFReorderAndSelectionCleanupActualPTY(t *testing.T) {
	teststate.Lock(t)
	teststate.Setenv(t, "SHELL", "/bin/sh")
	a := specRuntimeApp(t)
	t.Cleanup(a.sup.KillAll)
	owner := a.st.ActiveWorkspace().ID
	root := a.focusedPane()
	dir := t.TempDir()
	rootFile, popupFile, bFile := filepath.Join(dir, "root"), filepath.Join(dir, "popup"), filepath.Join(dir, "B")
	capture := func(path string, n int, ready string) string {
		return fmt.Sprintf("stty raw -echo; printf '%s'; dd bs=1 count=%d of='%s' 2>/dev/null; exec sleep 60", ready, n, path)
	}
	p, err := a.sup.SpawnWith(root, a.cwd, 80, 20, nil, []string{"/bin/sh", "-c", capture(rootFile, 1, "ROOTREADY")})
	if err != nil {
		t.Fatal(err)
	}
	specWaitText(t, p, "ROOTREADY")
	width, height := api.PopupSize{Cells: 40}, api.PopupSize{Cells: 12}
	if _, err = a.OpenPopup(a.cwd, capture(popupFile, 2, "POPREADY"), &width, &height); err != nil {
		t.Fatal(err)
	}
	popID := a.st.Popup.PaneID
	pop := a.sup.Get(popID)
	specWaitText(t, pop, "POPREADY")
	process := pop.ShellPID()
	specFeed(a, "Q")
	a.exec(model.CmdNewWorkspace{Cwd: a.cwd})
	bID := a.focusedPane()
	a.sup.Kill(bID)
	bInner := a.innerRect(bID)
	wantB := "B\x1b[27;2;13~P\x1b[<0;2;2M\x1b[<0;2;2m"
	b, err := a.sup.SpawnWith(bID, a.cwd, bInner.W, bInner.H, nil, []string{"/bin/sh", "-c", capture(bFile, len(wantB), "\x1b[?1000h\x1b[?1006hBREADY")})
	if err != nil {
		t.Fatal(err)
	}
	specWaitText(t, b, "BREADY")
	if a.st.PopupVisible() {
		t.Fatal("popup visible in B")
	}
	out := render.Compose(render.Input{State: a.st, Panes: a.paneSnaps()})
	if strings.Contains(frameTextForPopup(out.Frame), "POPREADY") {
		t.Fatal("hidden popup was drawn")
	}
	specFeed(a, "B\x1b[27;2;13~\x1b[200~P\x1b[201~")
	bMouse := uv.Mouse{X: bInner.X + 1, Y: bInner.Y + 1, Button: uv.MouseLeft}
	a.onMouse(input.Event{Kind: input.KindMouseClick, Mouse: bMouse})
	a.onMouse(input.Event{Kind: input.KindMouseRelease, Mouse: bMouse})
	specWaitFile(t, bFile, []byte(wantB))
	a.exec(model.CmdMoveWorkspace{Index: 0, InsertIndex: 1})
	a.exec(model.CmdSelectWorkspace{Index: 1})
	if a.st.ActiveWorkspace().ID != owner || a.focusedPane() != popID || pop.ShellPID() != process {
		t.Fatal("owner/runtime changed")
	}
	specFeed(a, "R")
	specWaitFile(t, popupFile, []byte("QR"))
	if data, _ := os.ReadFile(rootFile); len(data) != 0 {
		t.Fatalf("popup input leaked into tile: %q", data)
	}
	inner := a.innerRect(popID)
	a.onMouse(input.Event{Kind: input.KindMouseClick, Mouse: uv.Mouse{X: inner.X, Y: inner.Y, Button: uv.MouseLeft}})
	a.onMouse(input.Event{Kind: input.KindMouseMotion, Mouse: uv.Mouse{X: inner.X + 2, Y: inner.Y, Button: uv.MouseLeft}})
	selection := a.sel
	a.exec(model.CmdSelectWorkspace{Index: 0})
	if a.sel != nil || a.popupSel != selection {
		t.Fatal("hidden selection leaked or vanished")
	}
	a.exec(model.CmdSelectWorkspace{Index: 1})
	if a.sel != selection {
		t.Fatal("selection was not restored")
	}
	a.exec(model.CmdCloseWorkspace{Index: 0})
	if a.st.Popup == nil || a.sel != selection || a.sup.Get(popID) != pop {
		t.Fatal("B close removed owner runtime/selection")
	}
	a.exec(model.CmdPopupResizeLock{Locked: true})
	a.exec(model.CmdCloseWorkspace{Index: 0})
	if a.st.Popup != nil || a.sel != nil || a.popupSel != nil || a.sup.Get(popID) != nil {
		t.Fatal("owner close leaked popup state/runtime")
	}
}

func frameTextForPopup(f *render.Frame) string {
	var b strings.Builder
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			if c := f.Cell(x, y); c != nil {
				b.WriteString(c.Content)
			}
		}
	}
	return b.String()
}

func TestG2PopupMouseCoordinatesWheelSelectionAutoscrollAndOSC52(t *testing.T) {
	teststate.Lock(t)
	teststate.Setenv(t, "SHELL", "/bin/sh")
	for _, reporting := range []bool{false, true} {
		t.Run(fmt.Sprint(reporting), func(t *testing.T) {
			a := specRuntimeApp(t)
			t.Cleanup(a.sup.KillAll)
			width, height := api.PopupSize{Cells: 40}, api.PopupSize{Cells: 12}
			path := filepath.Join(t.TempDir(), "received")
			want := "\x1b[<0;3;3M\x1b[<0;3;3m\x1b[<64;3;3M"
			command := "i=0; while [ $i -lt 100 ]; do printf 'line %s\\r\\n' $i; i=$((i+1)); done; printf READY; exec sleep 60"
			if reporting {
				command = fmt.Sprintf("stty raw -echo; printf '\\033[?1000h\\033[?1006hREADY'; dd bs=1 count=%d of='%s' 2>/dev/null; exec sleep 60", len(want), path)
			}
			if _, err := a.OpenPopup(a.cwd, command, &width, &height); err != nil {
				t.Fatal(err)
			}
			id := a.st.Popup.PaneID
			p := a.sup.Get(id)
			specWaitText(t, p, "READY")
			inner := a.innerRect(id)
			m := uv.Mouse{X: inner.X + 2, Y: inner.Y + 2, Button: uv.MouseLeft}
			if reporting {
				a.onMouse(input.Event{Kind: input.KindMouseClick, Mouse: m})
				a.onMouse(input.Event{Kind: input.KindMouseRelease, Mouse: m})
				m.Button = uv.MouseWheelUp
				a.onMouse(input.Event{Kind: input.KindMouseWheel, Mouse: m})
				specWaitFile(t, path, []byte(want))
				if a.sel != nil || a.scroll[id] != 0 {
					t.Fatal("reporting input mixed with host selection/scroll")
				}
				return
			}
			m.Button = uv.MouseWheelUp
			a.onMouse(input.Event{Kind: input.KindMouseWheel, Mouse: m})
			if a.scroll[id] != 3 || a.scroll[a.st.FocusedPane()] != 0 {
				t.Fatal("wheel scrolled tile")
			}
			m.Button = uv.MouseLeft
			a.onMouse(input.Event{Kind: input.KindMouseClick, Mouse: m})
			m.Y = inner.Y - 1
			a.onMouse(input.Event{Kind: input.KindMouseMotion, Mouse: m})
			a.popupSelectionAutoScroll()
			if a.scroll[id] != 6 || a.sel == nil || a.sel.pane != id {
				t.Fatal("popup autoscroll missed runtime")
			}
			// Capture OSC52 bytes in memory; no system clipboard is touched.
			var output bytes.Buffer
			a.out = &output
			a.onMouse(input.Event{Kind: input.KindMouseRelease, Mouse: m})
			if !strings.Contains(output.String(), "\x1b]52;c;") || a.sel != nil {
				t.Fatal("popup selection was not copied")
			}
		})
	}
}

func TestStage6aDuplicatePopupPreservesRunningProcess(t *testing.T) {
	teststate.Lock(t)
	teststate.Setenv(t, "SHELL", "/bin/sh")
	a := specRuntimeApp(t)
	t.Cleanup(a.sup.KillAll)
	first, err := a.OpenPopup(a.cwd, "printf POPREADY; exec sleep 60", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pop := a.st.Popup
	p := a.sup.Get(pop.PaneID)
	specWaitText(t, p, "POPREADY")
	if _, err := a.OpenPopup(a.cwd, "echo REPLACED", nil, nil); err == nil || err.Error() != "popup already open" {
		t.Fatalf("duplicate: %v", err)
	}
	if a.st.Popup != pop || a.st.Popup.TerminalID != first || a.sup.Get(pop.PaneID) != p {
		t.Fatal("replaced running popup")
	}
}

func TestStage6aPopupMinimumPTYGeometry(t *testing.T) {
	for _, tc := range []struct{ outer, inner int }{{6, 4}, {7, 4}, {8, 5}, {60, 57}} {
		r := popupInner(layout.Rect{X: 10, Y: 10, W: tc.outer, H: 4})
		if r.W != tc.inner || r.H != 2 {
			t.Fatal(tc, r)
		}
	}
}

func TestPopupWheelDuringSelectionCopiesThroughPaintedReleaseRow(t *testing.T) {
	teststate.Lock(t)
	teststate.Setenv(t, "SSH_CONNECTION", "isolated-test")
	for _, tc := range []struct {
		name    string
		repaint bool
		legacy  bool
		last    string
	}{
		{"repainted", true, false, "R98"},
		{"unpainted", false, false, "REA"},
		// A client that predates painted viewports still follows the wheel.
		{"legacy client", true, true, "R98"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testPopupWheelSelection(t, tc.repaint, tc.legacy, tc.last)
		})
	}
}

func testPopupWheelSelection(t *testing.T, repaint, legacy bool, wantLast string) {
	t.Helper()
	a := specRuntimeApp(t)
	t.Cleanup(a.sup.KillAll)
	width, height := api.PopupSize{Cells: 40}, api.PopupSize{Cells: 12}
	command := `i=1; while [ "$i" -le 100 ]; do printf 'R%02d\r\n' "$i"; i=$((i+1)); done; printf READY; exec /bin/cat`
	if _, err := a.OpenPluginPopup(a.cwd, []string{"/bin/sh", "-c", command}, nil, &width, &height); err != nil {
		t.Fatal(err)
	}
	pid := a.st.Popup.PaneID
	specWaitText(t, a.sup.Get(pid), "READY")
	var clipboard bytes.Buffer
	viewer := a.NewViewer(&clipboard, a.st.Cols, a.st.Rows, nil)
	viewer.Begin(a, 0, 0)
	var out *render.Output
	viewer.SetSnapshotSink(func(in render.Input) error {
		out = render.ComposeClient(in, nil, "", false)
		return nil
	})
	draw := func() {
		viewer.with(a, func(ui *App) { ui.draw() })
		sent := *out
		if legacy {
			sent.PaneViewports = nil
		}
		viewer.Rendered(a, sent, 0)
	}
	draw()
	inner := out.PaneViewports[pid].Inner
	visibleRow := func(y int) string {
		var row strings.Builder
		for x := inner.X; x < inner.X+3; x++ {
			row.WriteString(out.Frame.Cell(x, y).Content)
		}
		return row.String()
	}
	y := inner.Y + inner.H - 1
	if first, last := visibleRow(inner.Y), visibleRow(y); first != "R92" || last != "REA" {
		t.Fatalf("before wheel, painted rows = %q to %q, want R92 to READY", first, last)
	}
	viewer.Input(a, []byte(fmt.Sprintf("\x1b[<0;%d;%dM", inner.X+1, inner.Y+1)))
	viewer.Input(a, []byte(fmt.Sprintf("\x1b[<64;%d;%dM", inner.X+1, y+1)))
	if repaint {
		draw()
	}
	last := visibleRow(y)
	if last != wantLast {
		t.Fatalf("painted release row = %q, want %q", last, wantLast)
	}
	clipboard.Reset()
	viewer.Input(a, []byte(fmt.Sprintf("\x1b[<0;%d;%dm", inner.X+3, y+1)))
	want := "R92\nR93\nR94\nR95\nR96\nR97\n"
	if !repaint {
		want += "R98\nR99\nR100\n"
	}
	want += last
	payload, ok := strings.CutPrefix(clipboard.String(), "\x1b]52;c;")
	got, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(payload, "\a"))
	if !ok || err != nil || string(got) != want {
		t.Fatalf("clipboard = %q, want %q ending at the painted release row", got, want)
	}
}
