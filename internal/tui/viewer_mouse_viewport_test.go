package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestViewerMouseReportsPaintedPTYRow(t *testing.T) {
	teststate.Lock(t)
	for _, tc := range []struct {
		name, mode, report string
		pixelHost          bool
	}{
		{"cell", "", "\x1b[<0;1;11M", false},
		{"pixel", "\\033[?1016h", "\x1b[<0;5;169M", false},
		{"pixel-host", "\\033[?1016h", "\x1b[<0;3;165M", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := sizeOwnerRuntimeApp(t)
			pid := a.focusedPane()
			path := filepath.Join(t.TempDir(), "mouse")
			command := fmt.Sprintf("stty raw -echo; printf '\\033[?1003h\\033[?1006h%s\\033[11;1Hpainted\\033[36;1HREADY'; dd bs=1 count=%d of='%s' 2>/dev/null; exec sleep 60", tc.mode, len(tc.report), path)
			p, err := a.sup.SpawnWith(pid, a.cwd, 112, 36, nil, []string{"/bin/sh", "-c", command})
			if err != nil {
				t.Fatal(err)
			}
			large := a.NewViewer(io.Discard, 140, 40, nil)
			large.Begin(a, 140*8, 40*16)
			small := a.NewViewer(io.Discard, 50, 30, nil)
			small.Begin(a, 50*10, 30*20)
			specWaitText(t, p, "READY")
			var out *render.Output
			small.SetSnapshotSink(func(in render.Input) error {
				out = render.ComposeClient(in, nil, "", false)
				return nil
			})
			small.with(a, func(ui *App) { ui.draw() })
			view := out.PaneViewports[pid]
			inner := view.Inner
			if got := out.Frame.Cell(inner.X, inner.Y).Content; got != "p" {
				t.Fatalf("first visible row starts with %q, want painted", got)
			}
			small.Rendered(a, *out, 0)
			// A cursor move before the next frame must not change the
			// coordinates of a click on the frame the client painted.
			p.WithEmu(func(e *vt.Emulator) { _, _ = e.Write([]byte("\x1b[1;1H")) })
			x, y := inner.X, inner.Y
			if tc.pixelHost {
				small.Input(a, []byte("\x1b[?1016;2$y"))
				x, y = x*10+3, y*20+5
			}
			small.Input(a, []byte(fmt.Sprintf("\x1b[<0;%d;%dM", x+1, y+1)))
			specWaitFile(t, path, []byte(tc.report))
			if a.sizeOwners[a.currentSizeTab()].viewer != large {
				t.Fatal("mouse press took size ownership")
			}
		})
	}
}

func TestViewerMouseDoesNotReportPTYPadding(t *testing.T) {
	teststate.Lock(t)
	a := sizeOwnerRuntimeApp(t)
	pid := a.focusedPane()
	path := filepath.Join(t.TempDir(), "mouse")
	const want = "\x1b[<0;1;1M"
	command := fmt.Sprintf("stty raw -echo; printf '\\033[?1003h\\033[?1006hREADY'; dd bs=1 count=%d of='%s' 2>/dev/null; exec sleep 60", len(want), path)
	p, err := a.sup.SpawnWith(pid, a.cwd, 22, 26, nil, []string{"/bin/sh", "-c", command})
	if err != nil {
		t.Fatal(err)
	}
	small := a.NewViewer(io.Discard, 50, 30, nil)
	small.Begin(a, 0, 0)
	large := a.NewViewer(io.Discard, 140, 40, nil)
	large.Begin(a, 0, 0)
	specWaitText(t, p, "READY")
	large.with(a, func(ui *App) { ui.draw() })
	inner := large.app.paintedPaneViews[pid].Inner
	press := func(x, y int) { large.Input(a, []byte(fmt.Sprintf("\x1b[<0;%d;%dM", x+1, y+1))) }
	press(inner.X, inner.Y)       // ten blank rows above the live screen
	press(inner.X+22, inner.Y+10) // right of the live screen
	press(inner.X, inner.Y+10)    // first cell of the live screen
	specWaitFile(t, path, []byte(want))
}
