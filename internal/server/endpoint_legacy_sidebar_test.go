package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestEndpointLegacySidebarControlsArePerViewer(t *testing.T) {
	teststate.Lock(t)
	dir, done := startTestServer(t)
	defer stopServer(t, dir, done)
	old := dialClient(t, dir)
	defer old.conn.Close()
	modern := dialClient(t, dir)
	defer modern.conn.Close()
	peer := endpoint.Current()
	methods := make([]string, 0, len(peer.Methods))
	for _, method := range peer.Methods {
		if method != "workspace_new" {
			methods = append(methods, method)
		}
	}
	peer.Methods = methods
	old.send(t, clientMsg{Type: "hello", Peer: peer, Cols: 120, Rows: 40})
	modern.send(t, clientMsg{Type: "hello", Peer: endpoint.Current(), Cols: 120, Rows: 40})
	old.readUntil(t, 5*time.Second, func(m clientMsg) bool { return m.Type == "output" && old.lastInput != nil })
	modern.readUntil(t, 5*time.Second, func(m clientMsg) bool { return m.Type == "output" && modern.lastInput != nil })
	if !old.lastInput.LegacySidebarControls || modern.lastInput.LegacySidebarControls {
		t.Fatal("incorrect viewer compatibility selection")
	}
	state := old.lastInput.State
	r := render.LegacySidebarPresetRect(state)
	previous := state.PresetLabel()
	click := fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", r.X+1, r.Y+1, r.X+1, r.Y+1)
	old.send(t, clientMsg{Type: "input", Data: encode64([]byte(click))})
	old.readUntil(t, 5*time.Second, func(m clientMsg) bool { return m.Type == "output" && old.lastInput.State.PresetLabel() != previous })
	menu := render.LegacySidebarMenuRect(old.lastInput.State)
	click = fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", menu.X+1, menu.Y+1, menu.X+1, menu.Y+1)
	old.send(t, clientMsg{Type: "input", Data: encode64([]byte(click))})
	old.readUntil(t, 5*time.Second, func(m clientMsg) bool { return m.Type == "output" && old.lastInput.Menu != nil })
	old.send(t, clientMsg{Type: "input", Data: encode64([]byte("\x1b[27u"))})
	old.readUntil(t, 5*time.Second, func(m clientMsg) bool { return m.Type == "output" && old.lastInput.Menu == nil })
	count := len(old.lastInput.State.Workspaces)
	create := render.LegacySidebarNewRect(old.lastInput.State)
	click = fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", create.X+1, create.Y+1, create.X+1, create.Y+1)
	old.send(t, clientMsg{Type: "input", Data: encode64([]byte(click))})
	old.readUntil(t, 5*time.Second, func(m clientMsg) bool { return m.Type == "output" && len(old.lastInput.State.Workspaces) == count+1 })
	modern.send(t, clientMsg{Type: "resize", Cols: 119, Rows: 40})
	modern.readUntil(t, 5*time.Second, func(m clientMsg) bool { return m.Type == "output" && modern.lastInput.State.Cols == 119 })
	if modern.lastInput.LegacySidebarControls {
		t.Fatal("legacy selection leaked to modern viewer")
	}
}
