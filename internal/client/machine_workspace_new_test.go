package client

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/kazuph/ikesu/internal/endpoint"
)

func TestMachineWorkspaceNewRealEndpoints(t *testing.T) {
	h := newRightMachineHarness(t)
	local := h.m.endpoints["local"]
	localCount := len(local.snapshot.State.Workspaces)
	if err := h.m.createWorkspace("local"); err != nil {
		t.Fatal(err)
	}
	h.until("local workspace", func() bool { return len(local.snapshot.State.Workspaces) == localCount+1 })
	e := h.m.endpoints["remote"]
	if !endpoint.Allows(e.connection.writer.available, endpoint.MachineGeneration, e.connection.writer.methods, "workspace_new") {
		t.Fatal("real endpoint did not advertise workspace_new")
	}
	before := len(e.snapshot.State.Workspaces)
	cwd := e.snapshot.State.ActiveWorkspace().Cwd
	if err := h.m.createWorkspace("remote"); err != nil {
		t.Fatal(err)
	}
	h.until("new workspace and switch", func() bool {
		return h.m.active == "remote" && h.m.pending == nil && len(e.snapshot.State.Workspaces) == before+1
	})
	if got := e.snapshot.State.ActiveWorkspace().Cwd; got != cwd {
		t.Fatalf("new workspace cwd = %q, want %q", got, cwd)
	}
}

func TestMachineWorkspaceNewLegacyCoordinatesAndNarrowGuard(t *testing.T) {
	m := searchFixture(t)
	m.inputBatch = true
	e := m.endpoints["local"]
	e.connection.writer.methods = []string{"input"}
	e.snapshot.State.SidebarVisible = true
	e.snapshot.State.Cols, e.snapshot.State.Rows = 120, 40
	if err := m.createWorkspace("local"); err != nil {
		t.Fatal(err)
	}
	if len(e.connection.queue) != 1 || e.connection.queue[0].Type != "input" {
		t.Fatalf("legacy input: %+v", e.connection.queue)
	}
	b, err := base64.StdEncoding.DecodeString(e.connection.queue[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	area := e.snapshot.State.SidebarArea()
	want := fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", area.X+area.W-5, area.Y+area.H-2, area.X+area.W-5, area.Y+area.H-2)
	if string(b) != want {
		t.Fatalf("input %q, want %q", b, want)
	}
	e.connection.queue = nil
	e.snapshot.State.SidebarVisible = false
	if err := m.createWorkspace("local"); err != nil {
		t.Fatal(err)
	}
	if len(e.connection.queue) != 0 || m.notice == "" {
		t.Fatalf("collapsed sidebar sent input or omitted notice: %+v %q", e.connection.queue, m.notice)
	}
}
