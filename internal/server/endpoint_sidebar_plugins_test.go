package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/sidebarplugin"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestRightSidebarPluginCapabilityKeepsLegacyBottomJobClickable(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("onboarding=false\n[session]\nresume_agents_on_restore=false\n[ui.sidebar_plugins]\nenabled=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(sidebarplugin.Dir(cfg), "right")
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "probe.30s.sh"), []byte("#!/bin/sh\nprintf 'plugin body\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	teststate.Setenv(t, config.EnvVar, cfg)
	dir, done := startTestServer(t)
	defer stopServer(t, dir, done)

	old, modern := dialClient(t, dir), dialClient(t, dir)
	defer old.conn.Close()
	defer modern.conn.Close()
	legacyPeer := endpoint.Current()
	legacyPeer.Methods = slices.DeleteFunc(legacyPeer.Methods, func(method string) bool { return method == endpoint.RightSidebarPlugins })
	const rows = 18
	old.send(t, clientMsg{Type: "hello", Peer: legacyPeer, Cols: 120, Rows: rows})
	modern.send(t, clientMsg{Type: "hello", Peer: endpoint.Current(), Cols: 120, Rows: rows})
	wait := func(c *testConn, ready func(render.Input) bool) {
		t.Helper()
		c.readUntil(t, 5*time.Second, func(m clientMsg) bool {
			return m.Type == "output" && c.lastInput != nil && ready(*c.lastInput)
		})
		if c.lastInput == nil || !ready(*c.lastInput) {
			t.Fatal("connection closed before the expected snapshot")
		}
	}
	click := func(c *testConn, r layout.Rect) {
		t.Helper()
		cursor := fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", r.X+1, r.Y+1, r.X+1, r.Y+1)
		c.send(t, clientMsg{Type: "input", Data: encode64([]byte(cursor))})
	}
	wait(old, func(in render.Input) bool { return in.State.FocusedPane() == 1 })
	wait(modern, func(in render.Input) bool {
		p := in.RightSidebarPanel
		return p != nil && len(p.Sections) == 1 && len(p.Sections[0].Lines) == 1 &&
			p.Sections[0].Lines[0].Text == "plugin body"
	})
	apiOK(t, dir, api.MethodPaneReportAgent, map[string]any{
		"pane_id": "p1", "agent": "codex", "source": "codex", "session_id": "sidebar-compat", "state": "idle",
	}, nil)
	for i := range rows - 2 {
		apiOK(t, dir, api.MethodRunStart, api.RunStartParams{Label: fmt.Sprintf("compat-job-%02d", i),
			CallerPane: "p1", Argv: []string{"/usr/bin/true"}}, nil)
	}
	for _, c := range []*testConn{old, modern} {
		click(c, render.RightSidebarHeaderFor(c.lastInput.State, 0).Jobs)
		wait(c, func(in render.Input) bool { return in.JobsPanel && len(in.Jobs) == rows-2 })
	}

	right := modern.lastInput.RightSidebarPanel
	if !right.Rules {
		t.Fatal("server did not agree on the ruled layout with a modern client")
	}
	heads := render.SidebarPanelHeadings(right, render.RightSidebarPanelRect(modern.lastInput.State, right))
	if len(heads) != 1 {
		t.Fatalf("modern headings = %+v", heads)
	}
	click(modern, heads[0].Rect)
	folded := func(in render.Input) bool {
		return in.RightSidebarPanel != nil && in.RightSidebarPanel.Sections[0].Collapsed
	}
	wait(modern, folded)
	// Folded: the heading between its two rules takes the bottom three rows.
	panelRect := render.RightSidebarPanelRect(modern.lastInput.State, modern.lastInput.RightSidebarPanel)
	heads = render.SidebarPanelHeadings(modern.lastInput.RightSidebarPanel, panelRect)
	if panelRect.Y != rows-3 || panelRect.H != 3 || len(heads) != 1 || heads[0].Rect.Y != rows-2 {
		t.Fatalf("modern collapsed panel = %+v headings = %+v", panelRect, heads)
	}
	heading := heads[0].Rect
	if old.lastInput.RightSidebarPanel != nil {
		t.Fatal("server sent the right panel to a client without capability")
	}
	in := *old.lastInput
	view := render.SidebarViewForInput(in)
	// The legacy client has a job on the row where the modern heading sits.
	var last render.SidebarPosition
	for _, row := range view.Rows {
		if row.Right && row.Y == heading.Y {
			last = row
		}
	}
	if !last.Right || last.Row.Kind != render.RowJob {
		t.Fatalf("legacy row at y=%d = %+v", heading.Y, last)
	}
	want := "job log: " + in.Jobs[last.Row.Line].Label
	click(old, heading)
	wait(old, func(in render.Input) bool { return in.State.PopupVisible() && in.State.Popup.Title == want })

	// A new snapshot after the legacy click must keep the same shared fold.
	modern.send(t, clientMsg{Type: "resize", Cols: 121, Rows: rows})
	wait(modern, func(in render.Input) bool { return in.State.Cols == 121 })
	if !folded(*modern.lastInput) {
		t.Fatal("legacy job click toggled modern plugin")
	}
}

// A display from before the ruled plugin headings keeps the old layout: the
// server neither sends the rules to it nor reads its clicks with them.
func TestSidebarPanelRulesFollowEachDisplay(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("onboarding=false\n[session]\nresume_agents_on_restore=false\n[ui.sidebar_plugins]\nenabled=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(sidebarplugin.Dir(cfg), "right")
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "probe.30s.sh"), []byte("#!/bin/sh\nprintf 'plugin body\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	teststate.Setenv(t, config.EnvVar, cfg)
	dir, done := startTestServer(t)
	defer stopServer(t, dir, done)

	previous := dialClient(t, dir)
	defer previous.conn.Close()
	peer := endpoint.Current()
	peer.Methods = slices.DeleteFunc(peer.Methods, func(method string) bool { return method == endpoint.SidebarPanelRules })
	previous.send(t, clientMsg{Type: "hello", Peer: peer, Cols: 120, Rows: 18})
	ready := func(in render.Input) bool {
		p := in.RightSidebarPanel
		return p != nil && len(p.Sections) == 1 && len(p.Sections[0].Lines) == 1
	}
	previous.readUntil(t, 5*time.Second, func(m clientMsg) bool {
		return m.Type == "output" && previous.lastInput != nil && ready(*previous.lastInput)
	})
	p := previous.lastInput.RightSidebarPanel
	r := render.RightSidebarPanelRect(previous.lastInput.State, p)
	heads := render.SidebarPanelHeadings(p, r)
	if p.Rules || r.H != 2 || len(heads) != 1 || heads[0].Rect.Y != r.Y {
		t.Fatalf("previous display got rules=%t panel=%+v headings=%+v", p.Rules, r, heads)
	}
	// Its click on the old heading row folds the plugin on the server.
	at := heads[0].Rect
	previous.send(t, clientMsg{Type: "input", Data: encode64([]byte(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", at.X+2, at.Y+1, at.X+2, at.Y+1)))})
	previous.readUntil(t, 5*time.Second, func(m clientMsg) bool {
		return m.Type == "output" && previous.lastInput != nil && previous.lastInput.RightSidebarPanel != nil && previous.lastInput.RightSidebarPanel.Sections[0].Collapsed
	})
	if !previous.lastInput.RightSidebarPanel.Sections[0].Collapsed {
		t.Fatal("old heading click did not fold the plugin")
	}
}
