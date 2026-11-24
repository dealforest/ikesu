package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/model"
)

type androidPairSpec struct {
	cwd  string
	argv []string
	env  map[string]string
}

const androidPairShell = `node ./main.mjs pair --name "$1"
status=$?
if [ "$status" -ne 0 ] && [ "$status" -ne 130 ] && [ "$status" -ne 143 ]; then
  printf '\n終了コード %d で終了しました。内容を確認後、Enter または Esc で閉じてください。\n' "$status"
  IFS= read -r _
fi
exit "$status"`

// androidPairLaunch resolves the bridge from the plugin registry.
func (a *App) androidPairLaunch() (androidPairSpec, error) {
	if a.sidebarConfig.UI.NativeMenus {
		return androidPairSpec{}, fmt.Errorf("Android pairing is not available in mobile native menus")
	}
	if a.pluginHost == nil {
		return androidPairSpec{}, fmt.Errorf("Android bridge is unavailable; link the kazuph.android plugin first")
	}
	engine := a.pluginHost()
	if engine == nil || engine.Store == nil {
		return androidPairSpec{}, fmt.Errorf("Android bridge is unavailable; link the kazuph.android plugin first")
	}
	store := engine.Store
	entries, err := store.List("kazuph.android")
	if err != nil {
		return androidPairSpec{}, err
	}
	if len(entries) != 1 || !entries[0].Enabled || entries[0].Source.Kind != "local" || len(entries[0].Warnings) != 0 || entries[0].Manifest.ID != "kazuph.android" {
		return androidPairSpec{}, fmt.Errorf("link and enable the Android bridge plugin first (apps/android/README.md)")
	}
	mainScript := filepath.Join(entries[0].Root, "main.mjs")
	info, err := os.Stat(mainScript)
	if err != nil || !info.Mode().IsRegular() {
		return androidPairSpec{}, fmt.Errorf("Android bridge main.mjs is missing or is not a regular file; update the linked bridge")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return androidPairSpec{}, err
	}
	searchPath := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		"/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin",
	}
	if path := os.Getenv("PATH"); path != "" {
		searchPath = append(searchPath, path)
	}
	return androidPairSpec{
		cwd:  entries[0].Root,
		argv: []string{"/bin/sh", "-c", androidPairShell, "android-pair", androidPairHostName()},
		env: map[string]string{
			"PATH":                    strings.Join(searchPath, string(os.PathListSeparator)),
			"IKESU_PLUGIN_CONFIG_DIR": filepath.Join(store.ConfigRoot, "plugins", "config", "kazuph.android"),
			"IKESU_PLUGIN_STATE_DIR":  filepath.Join(store.StateRoot, "plugins", "kazuph.android"),
		},
	}, nil
}

func androidPairHostName() string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
			if name := strings.TrimSpace(string(out)); name != "" {
				return name
			}
		}
	}
	if name, err := os.Hostname(); err == nil && name != "" {
		return name
	}
	return "ikesu"
}

func (a *App) openAndroidPair() {
	spec, err := a.androidPairLaunch()
	if err == nil {
		if a.st.PopupVisible() {
			a.exec(model.CmdClosePopup{})
		}
		_, err = a.OpenPluginPopup(spec.cwd, spec.argv, spec.env, &api.PopupSize{Percent: 95}, &api.PopupSize{Percent: 95})
	}
	if err != nil {
		a.ShowToast("Android pairing", err.Error(), "error", -1)
		a.SetConfigWarning("Android pairing: " + err.Error())
	}
}
