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
	large.Input(a, []byte("x"))
	pid := tabA.Layout.Focused()
	want := viewerTerminalSizes(a)[pid]
	for _, step := range []struct {
		key       string
		workspace int
		tab       *model.Tab
	}{{"\x1b2", 1, tabB}, {"\x1b1", 0, tabA}} {
		large.Input(a, []byte(step.key))
		if large.workspace != a.st.Workspaces[step.workspace].ID || a.sizeOwners[step.tab].viewer != large {
			t.Fatalf("workspace %d was not selected and claimed", step.workspace)
		}
		if got := viewerTerminalSizes(a)[pid]; got != want || a.sizeOwners[tabA].viewer != large {
			t.Fatalf("workspace A after selecting workspace %d: size %v, want %v with original owner", step.workspace, got, want)
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
