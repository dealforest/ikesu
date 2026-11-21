package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kazuph/ikesu/internal/plugin"
)

func TestAndroidPairUsesEnabledLinkedBridge(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Terminal is macOS-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := &plugin.Store{ConfigRoot: filepath.Join(home, ".config", "ikesu"), StateRoot: filepath.Join(home, ".local", "state", "ikesu"), Version: "0.4.179"}
	a := specRuntimeApp(t)
	a.SetPluginHost(func() *plugin.Engine { return &plugin.Engine{Store: store} }, nil)
	if _, err := a.androidPairCommand(); err == nil {
		t.Fatal("unlinked bridge accepted")
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
	script := filepath.Join(root, "pair.command")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.androidPairCommand(); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("non-executable: %v", err)
	}
	if err := os.Chmod(script, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := a.androidPairCommand(); err != nil || got != script {
		t.Fatalf("got %q: %v", got, err)
	}
	a.sidebarConfig.UI.NativeMenus = true
	if _, err := a.androidPairCommand(); err == nil {
		t.Fatal("mobile viewer accepted")
	}
}
