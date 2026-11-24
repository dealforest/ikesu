package tui

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/plugin"
	"github.com/kazuph/ikesu/internal/runtime"
)

func TestAndroidPairViewerReplacesPopupAndKeepsErrorVisible(t *testing.T) {
	a := specRuntimeApp(t)
	t.Cleanup(a.sup.KillAll)
	root := t.TempDir()
	files := map[string]string{
		"ikesu-plugin.toml": "id = \"kazuph.android\"\nname = \"Android\"\nversion = \"0.1.0\"\nmin_ikesu_version = \"0.2.7\"\nplatforms = [\"macos\", \"linux\"]\n",
		"main.mjs":          "console.error('pairing fixture failed'); process.exitCode = 42;\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := &plugin.Store{ConfigRoot: t.TempDir(), StateRoot: t.TempDir(), Version: "0.4.184"}
	if _, err := store.Link(root, true, nil); err != nil {
		t.Fatal(err)
	}
	a.SetPluginHost(func() *plugin.Engine { return &plugin.Engine{Store: store} }, nil)

	owner := a.st.ActiveWorkspace().ID
	a.exec(model.CmdNewWorkspace{Cwd: t.TempDir()})
	workspace := a.st.ActiveWorkspace().ID
	v := a.NewViewer(io.Discard, 160, 55, nil)
	peer := a.NewViewer(io.Discard, 160, 55, nil)
	a.exec(model.CmdSelectWorkspace{Index: 0})
	v.with(a, func(ui *App) {
		if _, err := ui.OpenPluginPopup(root, []string{"/bin/cat"}, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		previous := ui.st.Popup
		oldPane := a.sup.Get(previous.PaneID)
		ui.openAndroidPair()
		popup := ui.st.Popup
		if !ui.st.PopupVisible() || popup == previous || popup.WorkspaceID != workspace || popup.Width.Percent != 95 || popup.Height.Percent != 95 {
			t.Fatalf("wrong pairing popup: %+v", popup)
		}
		pane := a.sup.Get(popup.PaneID)
		if pane == nil || pane == oldPane {
			t.Fatal("the pairing process did not replace the original PTY")
		}
		specWaitText(t, pane, "Enter")
		var output strings.Builder
		for _, row := range pane.BufferLines() {
			for _, cell := range row {
				output.WriteString(cell.Content)
			}
			output.WriteByte('\n')
		}
		screen := output.String()
		fixtureFailed := strings.Contains(screen, "pairing fixture failed") && strings.Contains(screen, "42")
		nodeUnavailable := strings.Contains(screen, "node") && strings.Contains(screen, "127")
		// On Go-only hosts, the shell should retain the actual Node launch error.
		_, nodeErr := exec.LookPath("node")
		if !fixtureFailed && (nodeErr == nil || !nodeUnavailable) {
			t.Fatalf("pairing error and exit status were not displayed: %q", screen)
		}
		if pane.Snapshot().Dead {
			t.Fatal("pairing shell exited before Enter")
		}
		peer.with(a, func(peerUI *App) {
			if !peerUI.st.PopupVisible() || peerUI.st.Popup != popup {
				t.Fatal("the second viewer of the same workspace did not see the popup")
			}
		})
		a.sup.Input(popup.PaneID, []byte("\r"))
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for ui.st.PopupVisible() {
			select {
			case event := <-a.sup.Events():
				if event.Kind == runtime.EventExited && event.Source == pane {
					ui.onRuntimeEvent(event)
				}
			case <-deadline.C:
				t.Fatal("Enter did not close the pairing popup")
			}
		}
	})
	if a.st.ActiveWorkspace().ID != owner || a.st.PopupVisible() {
		t.Fatal("pairing modified the owner's selection")
	}
}

func TestAndroidPairMenuAvailability(t *testing.T) {
	a := specRuntimeApp(t)
	a.clientMachines = true
	if menuIndexInItems(a.mainMenuItems(), "Android pairing QR...") < 0 {
		t.Fatal("machine menu has no Android pairing entry")
	}
	a.sidebarConfig.UI.NativeMenus = true
	if menuIndexInItems(a.mainMenuItems(), "Android pairing QR...") >= 0 {
		t.Fatal("native menu offers Android pairing")
	}
}

func TestAndroidPairUsesEnabledLinkedBridge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/existing/bin:/bin")
	store := &plugin.Store{ConfigRoot: filepath.Join(home, "custom config"), StateRoot: filepath.Join(home, "custom state"), Version: "0.4.179"}
	a := specRuntimeApp(t)
	a.SetPluginHost(func() *plugin.Engine { return &plugin.Engine{Store: store} }, nil)
	if _, err := a.androidPairLaunch(); err == nil || !strings.Contains(err.Error(), "link and enable") {
		t.Fatalf("unlinked bridge: %v", err)
	}
	root := filepath.Join(home, "linked bridge")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := "id = \"kazuph.android\"\nname = \"Android\"\nversion = \"0.1.0\"\nmin_ikesu_version = \"0.2.7\"\nplatforms = [\"macos\"]\n"
	if err := os.WriteFile(filepath.Join(root, "ikesu-plugin.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Link(root, true, nil); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "main.mjs")
	if _, err := a.androidPairLaunch(); err == nil || !strings.Contains(err.Error(), "main.mjs") {
		t.Fatalf("missing main.mjs: %v", err)
	}
	if err := os.Mkdir(script, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.androidPairLaunch(); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory main.mjs: %v", err)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, nil, 0600); err != nil {
		t.Fatal(err)
	}
	spec, err := a.androidPairLaunch()
	if err != nil {
		t.Fatal(err)
	}
	if spec.cwd != root {
		t.Errorf("cwd = %q, want %q", spec.cwd, root)
	}
	wantArgv := []string{"/bin/sh", "-c", androidPairShell, "android-pair", androidPairHostName()}
	if !slices.Equal(spec.argv, wantArgv) || spec.argv[4] == "" {
		t.Fatalf("argv = %q, want %q", spec.argv, wantArgv)
	}
	if !strings.HasPrefix(spec.argv[2], `node ./main.mjs pair --name "$1"`+"\n") {
		t.Errorf("pairing shell must pass the hostname as a quoted argument: %q", spec.argv[2])
	}
	wantEnv := map[string]string{
		"PATH":                    home + "/.local/bin:" + home + "/.local/share/mise/shims:/opt/homebrew/bin:/usr/local/bin:/home/linuxbrew/.linuxbrew/bin:/existing/bin:/bin",
		"IKESU_PLUGIN_CONFIG_DIR": filepath.Join(store.ConfigRoot, "plugins", "config", "kazuph.android"),
		"IKESU_PLUGIN_STATE_DIR":  filepath.Join(store.StateRoot, "plugins", "kazuph.android"),
	}
	for key, want := range wantEnv {
		if got := spec.env[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, err := store.SetEnabled("kazuph.android", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.androidPairLaunch(); err == nil || !strings.Contains(err.Error(), "link and enable") {
		t.Fatalf("disabled bridge: %v", err)
	}
	a.sidebarConfig.UI.NativeMenus = true
	if _, err := a.androidPairLaunch(); err == nil || err.Error() != "Android pairing is not available in mobile native menus" {
		t.Fatalf("mobile viewer: %v", err)
	}
}

func TestAndroidPairMenuShowsConfigWarning(t *testing.T) {
	a := specRuntimeApp(t)
	a.openMainMenu(0, 0)
	a.activateMenu(menuIndex(a, "Android pairing QR..."))
	const detail = "Android bridge is unavailable; link the kazuph.android plugin first"
	if a.st.PopupVisible() || a.configWarning != "Android pairing: "+detail {
		t.Fatalf("popup=%v, config warning=%q", a.st.PopupVisible(), a.configWarning)
	}
	if a.toast == nil || a.toast.title != "Android pairing" || a.toast.kind != "error" || a.toast.body != detail {
		t.Fatalf("toast = %+v", a.toast)
	}
}
