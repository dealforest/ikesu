package tui

import (
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/session"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestReopenClosedSpaceMenuListsAndReopens(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	reopened := ""
	a.SetClosedSpaceHooks(func(session.Workspace, string, string) {}, func() []ClosedSpace {
		return []ClosedSpace{
			{ID: "new", Label: "api", Panes: 2, Agents: []string{"claude", "codex"}, ClosedAt: time.Now().Add(-3 * time.Minute)},
			{ID: "old", Label: "docs", Panes: 1, Reason: "exited", ClosedAt: time.Now().Add(-26 * time.Hour)},
		}
	}, func(id string) (int, error) {
		reopened = id
		return 0, nil
	})
	r := render.SidebarMenuRect(a.st)
	sidebarEvent(a, input.KindMouseClick, r.X, r.Y)
	if a.menu == nil || a.menu.kind != menuMain {
		t.Fatal("main menu missing")
	}
	item := -1
	for i, it := range a.menu.m.Items {
		if it.Label == menuReopenSpace && !it.Disabled {
			item = i
		}
	}
	if item < 0 {
		t.Fatal("reopen item missing or disabled")
	}
	a.activateMenu(item)
	if a.menu == nil || a.menu.kind != menuMove || len(a.menu.m.Items) != 2 {
		t.Fatalf("reopen list = %+v", a.menu)
	}
	if got := a.menu.m.Items[0].Label; got != "api · 2 panes · claude,codex · 3m ago" {
		t.Fatalf("first row %q", got)
	}
	if got := a.menu.m.Items[1].Label; got != "docs · 1 pane · shell exited · 1d ago" {
		t.Fatalf("second row %q", got)
	}
	a.activateMenu(1)
	if reopened != "old" {
		t.Fatalf("reopened %q", reopened)
	}
}

func TestReopenClosedSpaceMenuWithEmptyList(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	a.SetClosedSpaceHooks(func(session.Workspace, string, string) {}, func() []ClosedSpace { return nil }, func(string) (int, error) { return 0, nil })
	a.openReopenSpaceMenu(0, 0)
	if a.menu != nil || a.toast == nil {
		t.Fatalf("empty list should toast, menu=%+v", a.menu)
	}
}

func TestRollbackCloseIsNotRecordedAndOnlyLastPaneCapturesTheSpace(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	recorded := 0
	a.SetClosedSpaceHooks(func(session.Workspace, string, string) { recorded++ }, func() []ClosedSpace { return nil }, func(string) (int, error) { return 0, nil })
	a.exec(model.CmdNewWorkspace{Cwd: a.cwd, Name: "rollback"})
	a.execWithoutClosedRecord(model.CmdCloseWorkspace{Index: len(a.st.Workspaces) - 1})
	if recorded != 0 {
		t.Fatalf("rollback close recorded %d", recorded)
	}
	a.exec(model.CmdNewWorkspace{Cwd: a.cwd, Name: "two panes"})
	a.exec(model.CmdSplitFocused{Dir: layout.Vertical})
	panes := a.st.Workspaces[len(a.st.Workspaces)-1].Tabs[0].Layout.PaneIDs()
	if c := a.closingSpaceFor(model.CmdClosePane{Pane: panes[0]}); c != nil {
		t.Fatal("closing one of two panes captured the space")
	}
	a.exec(model.CmdClosePane{Pane: panes[0]})
	a.exec(model.CmdClosePane{Pane: panes[1]})
	if recorded != 1 {
		t.Fatalf("last pane close recorded %d", recorded)
	}
}
