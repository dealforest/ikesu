package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/teststate"
	"github.com/kazuph/ikesu/internal/tui"
)

// closedSpaceServer is a test server whose claude command is a script that
// writes its argv, so a typed resume never starts a real agent.
func closedSpaceServer(t *testing.T) (*Server, string) {
	t.Helper()
	teststate.Setenv(t, "SHELL", "/bin/sh")
	s := newTestServer(t, "closed-spaces")
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(s.dir, "argv")
	fake := filepath.Join(s.dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + capture + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(s.dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("onboarding=false\n[agent_start.commands]\nclaude=["+strconv.Quote(fake)+"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	teststate.Setenv(t, config.EnvVar, cfgPath)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s.cfg = cfg
	s.app.SetClosedSpaceHooks(s.recordClosedSpace, s.listClosedSpaces, s.reopenClosedSpace)
	if err := s.app.StartInitialPane(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.sup.KillAll)
	return s, capture
}

func TestClosedSpaceIsListedAndReopenedWithLayoutAndAgentResume(t *testing.T) {
	teststate.Lock(t)
	s, capture := closedSpaceServer(t)
	work := filepath.Join(filepath.Dir(s.dir), "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	s.app.ExecCmd(model.CmdNewWorkspace{Cwd: work, Name: "keep me"})
	s.app.ExecCmd(model.CmdSplitFocused{Dir: layout.Vertical})
	s.app.ExecCmd(model.CmdRenameTab{Workspace: 1, Tab: 0, Label: "editor"})
	ws := s.app.State().Workspaces[1]
	panes := ws.Tabs[0].Layout.PaneIDs()
	if len(panes) != 2 {
		t.Fatalf("setup panes %v", panes)
	}
	label := s.app.WorkspaceLabel(1)
	s.app.RecordSession(panes[0], tui.AgentRecord{Agent: "claude", Kind: "id", Value: "closed-space-session", Source: "report:test"})

	s.app.ExecCmd(model.CmdCloseWorkspace{Index: 1})
	if n := len(s.app.State().Workspaces); n != 1 {
		t.Fatalf("workspaces after close = %d", n)
	}
	list := s.listClosedSpaces()
	if len(list) != 1 || list[0].Label != label || list[0].Panes != 2 || len(list[0].Agents) != 1 || list[0].Agents[0] != "claude" || list[0].Reason != "closed" {
		t.Fatalf("closed list = %+v", list)
	}
	waitClosedSpacesFile(t, s.dir, 1)

	beforeVisible, beforeSize := s.app.State().SidebarVisible, s.app.State().ExpandedSidebarWidth()
	index, err := s.reopenClosedSpace(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	st := s.app.State()
	if st.SidebarVisible != beforeVisible || st.ExpandedSidebarWidth() != beforeSize {
		t.Fatalf("sidebar changed: visible %v->%v size %d->%d", beforeVisible, st.SidebarVisible, beforeSize, st.ExpandedSidebarWidth())
	}
	if index != 1 || len(st.Workspaces) != 2 {
		t.Fatalf("reopen index %d, workspaces %d", index, len(st.Workspaces))
	}
	back := st.Workspaces[1]
	if back.Name != "keep me" || back.Cwd != work || len(back.Tabs) != 1 || back.Tabs[0].Name != "editor" || back.Tabs[0].Layout.PaneCount() != 2 {
		t.Fatalf("reopened space = %+v", back)
	}
	resumed := 0
	for _, pid := range back.Tabs[0].Layout.PaneIDs() {
		if s.sup.Get(pid) == nil {
			t.Fatalf("pane %d has no process", pid)
		}
		if rec, ok := s.app.SessionRecord(pid); ok && rec.Value == "closed-space-session" {
			resumed++
		}
	}
	if resumed != 1 {
		t.Fatalf("session records carrying the closed session = %d", resumed)
	}
	if got := s.listClosedSpaces(); len(got) != 0 {
		t.Fatalf("entry kept after reopen: %+v", got)
	}
	waitClosedSpacesFile(t, s.dir, 0)
	// The app loop does not run here, so the typed resume command is
	// checked on the screen instead of waiting for Enter.
	deadline := time.Now().Add(5 * time.Second)
	for {
		typed := false
		for _, pid := range back.Tabs[0].Layout.PaneIDs() {
			if rec, ok := s.app.SessionRecord(pid); ok && rec.Value == "closed-space-session" {
				screen := strings.ReplaceAll(s.sup.Get(pid).ScreenText(), "\n", "")
				typed = strings.Contains(screen, filepath.Join(filepath.Dir(capture), "claude")+" --resume closed-space-session")
			}
		}
		if typed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resume command was not typed into the AI pane")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestClosedSpaceSkipsMovedPanesAndRefusesMissingFolder(t *testing.T) {
	teststate.Lock(t)
	s, _ := closedSpaceServer(t)
	gone := filepath.Join(filepath.Dir(s.dir), "gone")
	if err := os.MkdirAll(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	// Moving the only tab away empties the space but keeps its pane.
	s.app.ExecCmd(model.CmdNewWorkspace{Cwd: gone, Name: "moved"})
	s.app.ExecCmd(model.CmdMoveTabToWorkspace{Workspace: 1, Index: 0, Dest: 0})
	if n := len(s.app.State().Workspaces); n != 1 {
		t.Fatalf("workspaces after move = %d", n)
	}
	if got := s.listClosedSpaces(); len(got) != 0 {
		t.Fatalf("moved space recorded: %+v", got)
	}
	// The last pane of a space closing records the space.
	s.app.ExecCmd(model.CmdNewWorkspace{Cwd: gone, Name: "deleted folder"})
	pane := s.app.State().Workspaces[1].Tabs[0].Layout.PaneIDs()[0]
	s.app.ExecCmd(model.CmdClosePane{Pane: pane})
	list := s.listClosedSpaces()
	if len(list) != 1 || list[0].Reason != "closed" {
		t.Fatalf("closed list = %+v", list)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reopenClosedSpace(list[0].ID); err == nil {
		t.Fatal("reopened a space whose folder is gone")
	}
	if n := len(s.app.State().Workspaces); n != 1 {
		t.Fatalf("failed reopen changed workspaces: %d", n)
	}
	if got := s.listClosedSpaces(); len(got) != 1 {
		t.Fatalf("failed reopen dropped the entry: %+v", got)
	}
	// A new server reads the saved list.
	waitClosedSpacesFile(t, s.dir, 1)
	fresh := &Server{dir: s.dir}
	if got := fresh.closedSpaceEntries(); len(got) != 1 || got[0].ID != list[0].ID {
		t.Fatalf("saved list = %+v", got)
	}
}

func TestClosedSpaceSkipsRollbackAndGoneFolderAndRefusesDamagedRecord(t *testing.T) {
	teststate.Lock(t)
	s, _ := closedSpaceServer(t)
	base := filepath.Dir(s.dir)
	gone := filepath.Join(base, "worktree")
	keep := filepath.Join(base, "keep")
	for _, d := range []string{gone, keep} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A folder deleted before its space closes (a removed worktree).
	s.app.ExecCmd(model.CmdNewWorkspace{Cwd: gone, Name: "worktree"})
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	s.app.ExecCmd(model.CmdCloseWorkspace{Index: 1})
	if got := s.listClosedSpaces(); len(got) != 0 {
		t.Fatalf("space with a deleted folder recorded: %+v", got)
	}
	// A damaged record is refused without touching the state.
	s.app.ExecCmd(model.CmdNewWorkspace{Cwd: keep, Name: "keep"})
	s.app.ExecCmd(model.CmdCloseWorkspace{Index: 1})
	entries := s.closedSpaceEntries()
	if len(entries) != 1 {
		t.Fatalf("entries %+v", entries)
	}
	entries[0].Workspace.Tabs[0].Layout = nil
	if _, err := s.reopenClosedSpace(entries[0].ID); err == nil {
		t.Fatal("damaged record reopened")
	}
	if n := len(s.app.State().Workspaces); n != 1 {
		t.Fatalf("damaged reopen changed workspaces: %d", n)
	}
}

func waitClosedSpacesFile(t *testing.T, dir string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var entries []closedSpaceEntry
		b, err := os.ReadFile(closedSpacesPath(dir))
		if err == nil && json.Unmarshal(b, &entries) == nil && len(entries) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("closed_spaces.json has %d entries (%v), want %d", len(entries), err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
