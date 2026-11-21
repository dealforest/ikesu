package client

import (
	"fmt"
	"os"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/render"
)

// Set IKESU_RIGHT_TEST_SERVER_BIN to an actual old binary for this compatibility check.
func TestLegacyChromeRealEndpoint(t *testing.T) {
	if os.Getenv("IKESU_RIGHT_TEST_SERVER_BIN") == "" {
		t.Skip("requires an old endpoint executable")
	}
	h := newRightMachineHarness(t)
	m := h.m
	e := m.endpoints["local"]
	if endpoint.Allows(e.connection.writer.available, endpoint.MachineGeneration, e.connection.writer.methods, "workspace_new") {
		t.Fatal("endpoint is not legacy")
	}
	if err := m.paint(); err != nil {
		t.Fatal(err)
	}
	click := func(x, y int) {
		t.Helper()
		for _, suffix := range []string{"M", "m"} {
			if err := m.input(mouseEvent(t, fmt.Sprintf("\x1b[<0;%d;%d%s", x+1, y+1, suffix))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !m.koiInput.LegacySidebarControls || m.regions.ActionBar.Preset.W != 0 {
		t.Fatal("legacy sidebar layout not selected")
	}
	preset := render.LegacySidebarPresetRect(e.snapshot.State)
	old := e.snapshot.State.PresetLabel()
	click(preset.X, preset.Y)
	h.until("legacy preset changed", func() bool { return e.snapshot.State.PresetLabel() != old })
	if err := m.paint(); err != nil {
		t.Fatal(err)
	}
	menu := render.LegacySidebarMenuRect(e.snapshot.State)
	click(menu.X, menu.Y)
	h.until("legacy menu opened", func() bool { return e.snapshot.Menu != nil })
	escape := searchKey(uv.KeyEscape, 0, "")
	escape.Raw = []byte("\x1b[27u")
	if err := m.input(escape); err != nil {
		t.Fatal(err)
	}
	h.until("legacy menu closed", func() bool { return e.snapshot.Menu == nil })
	if err := m.paint(); err != nil {
		t.Fatal(err)
	}
	copy := m.regions.ActionBar.Copy
	if copy.W == 0 {
		t.Fatal("copy not drawn")
	}
	click(copy.X, copy.Y)
	h.until("legacy copy opened", func() bool { return e.snapshot.Copy != nil })
	remote := m.endpoints["remote"]
	before := len(remote.snapshot.State.Workspaces)
	if err := m.createWorkspace("remote"); err != nil {
		t.Fatal(err)
	}
	h.until("legacy remote new and switch", func() bool {
		return m.active == "remote" && m.pending == nil && len(remote.snapshot.State.Workspaces) == before+1
	})
}

func TestSidebarLayoutModernAndOffline(t *testing.T) {
	h := newRightMachineHarness(t)
	m := h.m
	e := m.endpoints["local"]
	if err := m.paint(); err != nil {
		t.Fatal(err)
	}
	if m.koiInput.LegacySidebarControls || m.regions.ActionBar.Preset.W == 0 {
		t.Fatal("modern endpoint used legacy layout")
	}
	connection := e.connection
	e.connection = nil
	e.snapshot.LegacySidebarControls = true
	if err := m.paint(); err != nil {
		e.connection = connection
		t.Fatal(err)
	}
	e.connection = connection
	if m.koiInput.LegacySidebarControls {
		t.Fatal("offline snapshot used legacy layout")
	}
}
