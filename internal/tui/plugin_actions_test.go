package tui

import (
	"encoding/json"
	"fmt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/plugin"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/teststate"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func installTestLinkPlugin(t *testing.T, a *App) (*plugin.Store, *plugin.Engine) {
	t.Helper()
	root := t.TempDir()
	manifest := `id="ui.test"
name="UI"
version="0.1.0"
min_ikesu_version="0.1.0"
[[actions]]
id="capture"
title="Capture"
command=["/bin/sh","-c","printf '%s' \"$IKESU_PLUGIN_CONTEXT_JSON\""]
[[link_handlers]]
id="link"
title="Link"
pattern="^https://example[.]test/"
action="capture"
`
	if err := os.WriteFile(filepath.Join(root, "ikesu-plugin.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	store := &plugin.Store{ConfigRoot: t.TempDir(), StateRoot: t.TempDir(), Version: "0.1.0"}
	if _, err := store.Link(root, true, nil); err != nil {
		t.Fatal(err)
	}
	engine := &plugin.Engine{Store: store, BinPath: "/ikesu", SocketPath: "/socket"}
	a.SetPluginHost(func() *plugin.Engine { return engine }, nil)
	return store, engine
}

func TestPluginConfiguredKeyAndControlHyperlinkActualPTY(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	defer a.sup.KillAll()
	store, engine := installTestLinkPlugin(t, a)
	var cfg config.Config
	cfg.Keys.Command = []config.PluginKeyBinding{{Key: "prefix+l", Type: "plugin_action", Command: "ui.test.capture"}}
	a.SetSidebarConfig(cfg, false)
	pid := a.st.FocusedPane()
	rect := a.rectForPane(pid)
	if _, err := a.sup.SpawnWith(pid, a.cwd, rect.W, rect.H, nil, []string{"/bin/cat"}); err != nil {
		t.Fatal(err)
	}
	a.prefixKey(uv.Key{Code: 'l'})
	engine.Wait()
	logs, err := store.Logs("ui.test", nil)
	if err != nil || len(logs) != 1 || logs[0].Stdout == nil {
		t.Fatal(logs, err)
	}
	var ctx plugin.Context
	if err := json.Unmarshal([]byte(*logs[0].Stdout), &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx["workspace_id"] != "s1" || ctx["focused_pane_id"] != "p1" || ctx["invocation_source"] != "keybind" {
		t.Fatal(ctx)
	}
	a.sup.Kill(pid)
	p, err := a.sup.SpawnWith(pid, a.cwd, rect.W, rect.H, nil, []string{"/bin/sh", "-c", "printf '\033]8;;https://example.test/hidden\033\\Click\033]8;;\033\\'; exec /bin/cat"})
	if err != nil {
		t.Fatal(err)
	}
	specWaitText(t, p, "Click")
	r := a.innerRect(pid)
	mouse := uv.Mouse{X: r.X + 1, Y: r.Y, Button: uv.MouseLeft}
	if a.pluginLinkClick(pid, mouse) {
		t.Fatal("plain click invoked handler")
	}
	mouse.Mod = uv.ModCtrl
	if !a.pluginLinkClick(pid, mouse) {
		t.Fatal("control hyperlink not handled")
	}
	engine.Wait()
	logs, err = store.Logs("ui.test", nil)
	if err != nil || len(logs) != 2 {
		t.Fatal(logs, err)
	}
	if err := json.Unmarshal([]byte(*logs[0].Stdout), &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx["clicked_url"] != "https://example.test/hidden" || ctx["link_handler_id"] != "link" || ctx["invocation_source"] != "link_click" {
		t.Fatal(ctx)
	}
}

func TestViewerControlClickPassesPaintedLinkToPlugin(t *testing.T) {
	teststate.Lock(t)
	a := sizeOwnerRuntimeApp(t)
	store, engine := installTestLinkPlugin(t, a)
	pid := a.focusedPane()
	const first = "https://example.test/first"
	const eleventh = "https://example.test/eleventh"
	command := `printf '\033[1;1H%s\033[11;1H%s\033[12;1HREADY\033[1;1H' "$1" "$2"; exec /bin/cat`
	p, err := a.sup.SpawnWith(pid, a.cwd, 112, 36, nil, []string{"/bin/sh", "-c", command, "test", first, eleventh})
	if err != nil {
		t.Fatal(err)
	}
	large := a.NewViewer(io.Discard, 140, 40, nil)
	large.Begin(a, 0, 0)
	small := a.NewViewer(io.Discard, 82, 30, nil)
	small.Begin(a, 0, 0)
	specWaitText(t, p, "READY")
	var out *render.Output
	small.SetSnapshotSink(func(in render.Input) error {
		out = render.ComposeClient(in, nil, "", false)
		return nil
	})
	small.with(a, func(ui *App) { ui.draw() })
	view := out.PaneViewports[pid]
	x, y := view.Inner.X, view.Inner.Y
	var visible []byte
	for i := range len(first) {
		visible = append(visible, out.Frame.Cell(x+i, y).Content...)
	}
	if view.Inner.H != 26 || view.Rows != 36 || string(visible) != first {
		t.Fatalf("painted row = %q, viewport = %+v", visible, view)
	}
	small.Rendered(a, *out, 0)
	// The client still shows row 1 after the cursor moves to row 36.
	p.WithEmu(func(e *vt.Emulator) { _, _ = e.Write([]byte("\x1b[36;1H")) })
	small.Input(a, []byte(fmt.Sprintf("\x1b[<16;%d;%dM", x+2, y+1)))
	engine.Wait()
	logs, err := store.Logs("ui.test", nil)
	if err != nil || len(logs) != 1 || logs[0].Stdout == nil {
		t.Fatal(logs, err)
	}
	var ctx plugin.Context
	if err := json.Unmarshal([]byte(*logs[0].Stdout), &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx["clicked_url"] != string(visible) {
		t.Fatalf("plugin URL = %q, painted URL = %q", ctx["clicked_url"], visible)
	}
	if a.sizeOwners[a.currentSizeTab()].viewer != large {
		t.Fatal("Ctrl-click took size ownership")
	}
}
