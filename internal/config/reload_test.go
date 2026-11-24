package config

import (
	"github.com/kazuph/ikesu/internal/teststate"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReloadedPreservesUIAndDisablesInvalidBinding(t *testing.T) {
	teststate.Lock(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	teststate.Setenv(t, EnvVar, path)
	previous, err := Decode([]byte("[ui]\nsidebar_width=38\nshow_tab_bar=false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[ui]\nsidebar_width='bad'\nshow_tab_bar=true\n[keys]\nnew_tab='prefix+zz'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadReloaded(previous)
	if err != nil || c.ShowTabBar() || *c.UI.SidebarWidth != 38 {
		t.Fatal(c, err)
	}
	if b := c.ActionBindings("new_tab"); len(b) != 1 || b[0] != "" {
		t.Fatal(b)
	}
	if !strings.Contains(c.Warning(), "disabling binding") || !strings.Contains(c.Warning(), "keeping current ui settings") {
		t.Fatal(c.Warnings)
	}
	// The observed 0.2.57 startup also disables an invalid binding.
	start, err := Load()
	if err != nil || !start.ShowTabBar() || start.ActionBindings("new_tab")[0] != "" {
		t.Fatal(start, err)
	}
}

func TestLoadReloadedKeepsV2UIDefaultsWhenFallbackConfigIsV1(t *testing.T) {
	teststate.Lock(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	teststate.Setenv(t, EnvVar, path)
	current, err := Decode([]byte("config_version = 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[ui]\nsidebar_width = 'bad'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadReloaded(current)
	if err != nil {
		t.Fatal(err)
	}
	if c.EffectiveConfigVersion() != ConfigVersionV1 || !strings.Contains(c.Warning(), "keeping current ui settings") {
		t.Fatalf("expected a partial v1 reload, version=%d warnings=%v", c.EffectiveConfigVersion(), c.Warnings)
	}
	if c.UI.Koi == nil || c.UI.Sound.Enabled == nil || c.UI.SidebarPlugins.Enabled == nil ||
		c.KoiEnabled() || c.SoundEnabled() || c.SidebarPluginsEnabled() {
		t.Fatal("partial reload did not pin the previous disabled UI defaults")
	}
	if current.UI.Koi != nil || current.UI.Sound.Enabled != nil || current.UI.SidebarPlugins.Enabled != nil {
		t.Fatal("partial reload mutated the prior config")
	}
	if err := os.WriteFile(path, []byte("ui = 'bad'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	topBad, err := LoadReloaded(current)
	if err != nil {
		t.Fatal(err)
	}
	if topBad.EffectiveConfigVersion() != ConfigVersionV1 || !strings.Contains(topBad.Warning(), "keeping current ui settings") {
		t.Fatalf("expected top-level ui type error to keep ui settings, version=%d warnings=%v", topBad.EffectiveConfigVersion(), topBad.Warnings)
	}
	if topBad.UI.Koi == nil || topBad.UI.Sound.Enabled == nil || topBad.UI.SidebarPlugins.Enabled == nil ||
		topBad.KoiEnabled() || topBad.SoundEnabled() || topBad.SidebarPluginsEnabled() {
		t.Fatal("top-level ui type error did not pin the previous disabled UI defaults")
	}
}
