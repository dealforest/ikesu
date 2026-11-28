package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestViewerDragCopiesPaintedRowsAcrossPTYResize(t *testing.T) {
	teststate.Lock(t)
	teststate.Setenv(t, "SSH_CONNECTION", "isolated-test")
	a := sizeOwnerRuntimeApp(t)
	pid := a.focusedPane()
	var rows strings.Builder
	for i := 1; i <= 26; i++ {
		fmt.Fprintf(&rows, "\\033[%d;1HR%02d", i, i)
	}
	command := fmt.Sprintf("printf '%s\\033[1;1H'; exec sleep 60", rows.String())
	p, err := a.sup.SpawnWith(pid, a.cwd, 112, 36, nil, []string{"/bin/sh", "-c", command})
	if err != nil {
		t.Fatal(err)
	}
	large := a.NewViewer(io.Discard, 140, 40, nil)
	large.Begin(a, 0, 0)
	var clipboard bytes.Buffer
	small := a.NewViewer(&clipboard, 50, 30, nil)
	small.Begin(a, 0, 0)
	specWaitText(t, p, "R26")
	var out *render.Output
	small.SetSnapshotSink(func(in render.Input) error {
		out = render.ComposeClient(in, nil, "", false)
		return nil
	})
	draw := func() {
		small.with(a, func(ui *App) { ui.draw() })
		small.Rendered(a, *out, 0)
	}
	draw()
	inner := out.PaneViewports[pid].Inner
	for i := 0; i < 3; i++ {
		var visible strings.Builder
		for x := 0; x < 3; x++ {
			visible.WriteString(out.Frame.Cell(inner.X+x, inner.Y+i).Content)
		}
		if want := fmt.Sprintf("R%02d", i+1); visible.String() != want {
			t.Fatalf("visible row %d = %q, want %q", i, visible.String(), want)
		}
	}
	mouse := func(code, x, y int, suffix string) {
		small.Input(a, []byte(fmt.Sprintf("\x1b[<%d;%d;%d%s", code, inner.X+x+1, inner.Y+y+1, suffix)))
	}
	mouse(0, 0, 0, "M")
	mouse(32, 2, 2, "M")
	if got := viewerTerminalSizes(a)[pid]; got != [2]int{22, 26} || a.sizeOwners[a.currentSizeTab()].viewer != small {
		t.Fatalf("drag did not claim PTY: %v", got)
	}
	large.Input(a, []byte("\x1b[<35;40;10M"))
	p.WithEmu(func(e *vt.Emulator) { _, _ = e.Write([]byte("\x1b[36;1H")) })
	draw()
	if top := out.PaneViewports[pid].BufferTop; top != 10 {
		t.Fatalf("new frame starts at buffer row %d, want 10", top)
	}
	clipboard.Reset()
	mouse(0, 2, 2, "m")
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("R01\nR02\nR03")) + "\a"
	if got := clipboard.String(); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
}
