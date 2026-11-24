package config

import (
	"os"
	"strings"
)

// LoadReloaded keeps the previous UI table when a UI item cannot be decoded.
// Startup decoding still uses defaults; live reload must not discard active UI settings.
func LoadReloaded(current Config) (Config, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		c := Config{}
		c.syncGuardDefaults()
		return c, nil
	}
	if err != nil {
		return Config{}, err
	}
	c, err := Decode(data)
	if err != nil {
		return Config{}, err
	}
	keepUI := false
	for i, warning := range c.Warnings {
		if (strings.HasPrefix(warning, "config parse error: ui.") || strings.HasPrefix(warning, "config parse error: ui =")) && strings.Contains(warning, "; invalid type,") {
			keepUI = true
			c.Warnings[i] = "invalid ui config: " + warning + "; keeping current ui settings"
		}
	}
	if keepUI {
		c.UI = current.UI
		if c.UI.Koi == nil {
			koi := current.KoiEnabled()
			c.UI.Koi = &koi
		}
		if c.UI.Sound.Enabled == nil {
			sound := current.SoundEnabled()
			c.UI.Sound.Enabled = &sound
		}
		if c.UI.SidebarPlugins.Enabled == nil {
			plugins := current.SidebarPluginsEnabled()
			c.UI.SidebarPlugins.Enabled = &plugins
		}
	}
	c.syncGuardDefaults()
	return c, nil
}

func (e Edit) ApplyTo(current Config) (Config, error) {
	if e.Kind == "reload" {
		return LoadReloaded(current)
	}
	return e.Apply()
}
