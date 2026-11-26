package tui

import (
	"io"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestViewerWorkspaceRoundTripKeepsLastInputSize(t *testing.T) {
	teststate.Lock(t)
	a := sizeOwnerRuntimeApp(t)
	cfg, err := config.Decode([]byte("[keys]\nswitch_workspace='alt+1..9'\nnext_workspace='prefix+o'\nopen_notification_target=''\n"))
	if err != nil {
		t.Fatal(err)
	}
	a.sidebarConfig = cfg
	if err := a.StartInitialPane(); err != nil {
		t.Fatal(err)
	}
	a.exec(model.CmdNewWorkspace{Cwd: t.TempDir(), NoFocus: true})
	tabA, tabB := a.st.Workspaces[0].TabRef(), a.st.Workspaces[1].TabRef()
	large := a.NewViewer(io.Discard, 140, 40, nil)
	large.Begin(a, 140*8, 40*16)
	small := a.NewViewer(io.Discard, 50, 30, nil)
	small.Begin(a, 50*10, 30*20)
	small.Input(a, []byte("\x1b2x"))
	large.Input(a, []byte("x"))
	paneA, paneB := tabA.Layout.Focused(), tabB.Layout.Focused()
	sizes := viewerTerminalSizes(a)
	if sizes[paneA] != [2]int{112, 36} || sizes[paneB] != [2]int{22, 26} {
		t.Fatalf("unexpected initial pane sizes: %v", sizes)
	}
	for _, visit := range []struct {
		viewer *Viewer
		keys   string
		target int
	}{{large, "\x1b2", 1}, {large, "\x1b1", 0}, {small, "\x1b1", 0}, {small, "\x1b2", 1},
		{large, "\x02o", 1}, {large, "\x02o", 0}, {small, "\x02o", 0}, {small, "\x02o", 1}} {
		visit.viewer.Input(a, []byte(visit.keys))
		if visit.viewer.workspace != a.st.Workspaces[visit.target].ID {
			t.Fatalf("workspace %d was not selected", visit.target)
		}
		if a.sizeOwners[tabA].viewer != large || a.sizeOwners[tabB].viewer != small {
			t.Fatalf("visiting workspace %d changed ownership", visit.target)
		}
		if got := viewerTerminalSizes(a); got[paneA] != sizes[paneA] || got[paneB] != sizes[paneB] {
			t.Fatalf("visiting workspace %d changed PTY sizes: %v -> %v", visit.target, sizes, got)
		}
		if w, h := a.sup.Get(paneA).CellPixels(); w != 8 || h != 16 {
			t.Fatalf("visiting workspace %d changed A's pixels to %dx%d", visit.target, w, h)
		}
		if w, h := a.sup.Get(paneB).CellPixels(); w != 10 || h != 20 {
			t.Fatalf("visiting workspace %d changed B's pixels to %dx%d", visit.target, w, h)
		}
		if visit.viewer == large && visit.target == 1 {
			// A newer resize while Mac views B cannot reinterpret A's
			// previously recorded cell geometry when Mac returns.
			large.Resize(a, 140, 40, 140*9, 40*18)
		}
	}
}

func TestViewerAttachResizeInputAndDetachSizeOwnership(t *testing.T) {
	teststate.Lock(t)
	for _, event := range []string{"attach", "resize", "key", "detach"} {
		t.Run(event, func(t *testing.T) {
			a := sizeOwnerRuntimeApp(t)
			if err := a.StartInitialPane(); err != nil {
				t.Fatal(err)
			}
			large := a.NewViewer(io.Discard, 140, 40, nil)
			large.Begin(a, 0, 0)
			large.Input(a, []byte("x"))
			small := a.NewViewer(io.Discard, 50, 30, nil)
			small.Begin(a, 0, 0)
			want := large
			switch event {
			case "resize":
				// Establish ownership explicitly so this case detects Resize
				// stealing it even with the old attachment behavior.
				large.Input(a, []byte("x"))
				small.Resize(a, 52, 31, 0, 0)
			case "key":
				small.Input(a, []byte("x"))
				want = small
			case "detach":
				large.Input(a, []byte("x"))
				large.Detach(a)
				want = small
			}
			if a.sizeOwners[a.currentSizeTab()].viewer != want {
				t.Fatalf("%s left the wrong viewer owning the PTY", event)
			}
			checkCurrentTabSizes(t, a, want.cols, want.rows)
		})
	}
}
