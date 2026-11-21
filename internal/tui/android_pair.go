package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// androidPairCommand resolves only the registered bridge, never the current workspace.
func (a *App) androidPairCommand() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("Android pairing requires a local macOS host")
	}
	if a.sidebarConfig.UI.NativeMenus {
		return "", fmt.Errorf("Android pairing is available on the Mac desktop")
	}
	if a.pluginHost == nil {
		return "", fmt.Errorf("Android bridge is unavailable; link the kazuph.android plugin first")
	}
	engine := a.pluginHost()
	if engine == nil || engine.Store == nil {
		return "", fmt.Errorf("Android bridge is unavailable; link the kazuph.android plugin first")
	}
	store := engine.Store
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if filepath.Clean(store.ConfigRoot) != filepath.Join(home, ".config", "ikesu") || filepath.Clean(store.StateRoot) != filepath.Join(home, ".local", "state", "ikesu") {
		return "", fmt.Errorf("Android pairing requires the default ikesu config and state directories")
	}
	entries, err := store.List("kazuph.android")
	if err != nil {
		return "", err
	}
	if len(entries) != 1 || !entries[0].Enabled || entries[0].Source.Kind != "local" || len(entries[0].Warnings) != 0 || entries[0].Manifest.ID != "kazuph.android" {
		return "", fmt.Errorf("link and enable the Android bridge plugin first (apps/android/README.md)")
	}
	script := filepath.Join(entries[0].Root, "pair.command")
	info, err := os.Stat(script)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("Android bridge pair.command is missing or not executable; update the linked bridge")
	}
	return script, nil
}

func (a *App) openAndroidPair() {
	script, err := a.androidPairCommand()
	if err == nil {
		cmd := exec.Command("/usr/bin/open", "-a", "Terminal", script)
		err = cmd.Start()
		if err == nil {
			go func() { _ = cmd.Wait() }()
		}
	}
	if err != nil {
		a.ShowToast("Android pairing", err.Error(), "error", -1)
	}
}
