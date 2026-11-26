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
	cfg, err := config.Decode([]byte("[keys]\nswitch_workspace='alt+1..9'\n"))
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
	large.Begin(a, 0, 0)
	small := a.NewViewer(io.Discard, 50, 30, nil)
	small.Begin(a, 0, 0)
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
	}{{large, "\x1b2", 1}, {large, "\x1b1", 0}, {small, "\x1b1", 0}, {small, "\x1b2", 1}} {
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
