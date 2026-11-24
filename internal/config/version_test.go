package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazuph/ikesu/internal/teststate"
)

func optInValues(c Config) map[string]bool {
	return map[string]bool{
		"guard.enabled":                    c.GuardConfig().Active(),
		"decisions.screen_prompts":         c.ScreenPrompts(),
		"session.resume_agents_on_restore": c.ResumeAgentsOnRestore(),
		"ui.koi":                           c.KoiEnabled(),
		"ui.sound.enabled":                 c.SoundEnabled(),
		"ui.sidebar_plugins.enabled":       c.SidebarPluginsEnabled(),
		"integrations.claude_teams":        c.ClaudeTeamsEnabled(),
	}
}

func TestFreshConfigDefaultsAndFirstSettingsWrite(t *testing.T) {
	teststate.Lock(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	teststate.Setenv(t, EnvVar, path)
	check := func(c Config, err error, fromFile bool) {
		t.Helper()
		if err != nil || c.LoadedFromFile() != fromFile || c.EffectiveConfigVersion() != 2 || c.GuardConfig().EffectiveMode() != "record" {
			t.Fatalf("version=%d file=%v mode=%s err=%v", c.EffectiveConfigVersion(), c.LoadedFromFile(), c.GuardConfig().EffectiveMode(), err)
		}
		for key, value := range optInValues(c) {
			if value {
				t.Errorf("%s unexpectedly enabled", key)
			}
		}
	}
	c, err := Load()
	check(c, err, false)
	example, err := Decode([]byte(DefaultExample))
	check(example, err, true)
	c, err = LoadReloaded(example)
	check(c, err, false)
	if err := SaveOnboarding(false); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	check(c, err, true)
}

func TestDecodeConfigVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		version    int
		mode       string
		overrides  map[string]bool
		warning    bool
	}{
		{name: "empty existing file", version: 1, mode: "enforce"},
		{name: "implicit v1", body: "onboarding=false", version: 1, mode: "enforce"},
		{name: "explicit v1", body: "config_version=1\n[ui]\nkoi=false", version: 1, mode: "enforce", overrides: map[string]bool{"ui.koi": false}},
		{name: "v2 opt-in", body: "config_version=2\n[ui.sidebar_plugins]\nenabled=true\n[integrations]\nclaude_teams=true", version: 2, mode: "record", overrides: map[string]bool{"ui.sidebar_plugins.enabled": true, "integrations.claude_teams": true}},
		{name: "guard record", body: "config_version=2\n[guard]\nenabled=true", version: 2, mode: "record", overrides: map[string]bool{"guard.enabled": true}},
		{name: "guard enforce", body: "config_version=2\n[guard]\nenabled=true\nmode='enforce'", version: 2, mode: "enforce", overrides: map[string]bool{"guard.enabled": true}},
		{name: "unsupported", body: "config_version=99", version: 1, mode: "enforce", warning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Decode([]byte(tc.body))
			if err != nil || !c.LoadedFromFile() || c.EffectiveConfigVersion() != tc.version || c.GuardConfig().EffectiveMode() != tc.mode {
				t.Fatalf("version=%d file=%v mode=%s err=%v", c.EffectiveConfigVersion(), c.LoadedFromFile(), c.GuardConfig().EffectiveMode(), err)
			}
			if warning := strings.Contains(c.Warning(), "config_version: unsupported value; using default"); warning != tc.warning {
				t.Fatalf("warnings = %v", c.Warnings)
			}
			for key, got := range optInValues(c) {
				want, explicit := tc.overrides[key]
				if !explicit {
					want = tc.version == 1
				}
				if got != want {
					t.Errorf("%s = %t, want %t", key, got, want)
				}
			}
		})
	}
}

func TestConfigMigration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, mode, action string
		preserve           bool
	}{
		{"adopt", "record", "adopt_v2_default", false},
		{"preserve", "enforce", "preserve_explicit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			original := []byte("[ui]\nkoi=false\n[unknown]\nkey='keep me'\n")
			if err := os.WriteFile(path, original, 0644); err != nil {
				t.Fatal(err)
			}
			opened, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			before, err := opened.Stat()
			if err != nil {
				t.Fatal(err)
			}
			dry, err := PlanMigrate(path)
			if err != nil || dry.Path != path || dry.CurrentVersion != 1 || dry.TargetVersion != 2 || dry.AlreadyCurrent || len(dry.Changes) != 7 {
				t.Fatalf("dry run: %+v, %v", dry, err)
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("dry run changed the file: %s (%v)", data, err)
			}
			plan, err := ApplyMigrate(path, tc.preserve)
			if err != nil || plan.AlreadyCurrent || len(plan.Changes) != len(dry.Changes) {
				t.Fatalf("apply: %+v, %v", plan, err)
			}
			for _, change := range plan.Changes {
				if change.Key == "ui.koi" || change.Action != tc.action {
					t.Errorf("unexpected change: %+v", change)
				}
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if os.SameFile(before, after) || after.Mode().Perm() != 0600 {
				t.Fatalf("expected atomic replacement and mode 0600, got %+v → %+v", before, after)
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Contains(data, []byte("keep me")) {
				t.Fatalf("unknown key was lost: %s (%v)", data, err)
			}
			c, err := Decode(data)
			if err != nil || c.EffectiveConfigVersion() != 2 || c.GuardConfig().EffectiveMode() != tc.mode {
				t.Fatalf("decoded config: %+v, %v", c, err)
			}
			if c.UI.Koi == nil || *c.UI.Koi {
				t.Fatal("explicit false was lost")
			}
			for key, got := range optInValues(c) {
				if want := tc.preserve && key != "ui.koi"; got != want {
					t.Errorf("%s = %t, want %t", key, got, want)
				}
			}
			next, err := ApplyMigrate(path, tc.preserve)
			if err != nil || !next.AlreadyCurrent || len(next.Changes) != 0 {
				t.Fatalf("second apply: %+v, %v", next, err)
			}
			again, err := os.Stat(path)
			if err != nil || !os.SameFile(after, again) {
				t.Fatalf("second apply replaced the file: %v", err)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
				t.Fatalf("temporary files left behind: %v (%v)", entries, err)
			}
		})
	}
}

func TestPreserveMigrationHandlesExplicitDefaultAndInvalidValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	old := []byte("[guard]\nenabled=true\nmode=''\n[ui]\nkoi='invalid'\n")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	dry, err := PlanMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]bool{}
	for _, item := range dry.Changes {
		changed[item.Key] = true
	}
	if !changed["ui.koi"] || !changed["guard.mode"] || changed["guard.enabled"] {
		t.Fatalf("wrong plan for explicit settings and fallbacks: %+v", dry.Changes)
	}
	if _, err := ApplyMigrate(path, true); err == nil || !strings.Contains(err.Error(), "ui.koi") {
		t.Fatalf("preserve operation accepted a conflicting invalid value: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(old, data) {
		t.Fatalf("failed operation changed config: %s (%v)", data, err)
	}
	if err := os.WriteFile(path, []byte("[guard]\nenabled=true\nmode=''\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyMigrate(path, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Decode(data)
	if err != nil || !c.GuardConfig().Active() || c.GuardConfig().EffectiveMode() != "enforce" {
		t.Fatalf("omitted mode lost its v1 behavior: %v, err=%v", c.GuardConfig(), err)
	}
}

func TestMigrateRejectsUnsupportedVersionWithoutWriting(t *testing.T) {
	for _, version := range []string{"99", "0", "-1", `"two"`, "2.0"} {
		t.Run(version, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			original := []byte("config_version = " + version + "\n# preserve\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := PlanMigrate(path); err == nil || !strings.Contains(err.Error(), "unsupported config_version:") {
				t.Fatalf("plan returned %v", err)
			}
			for _, preserve := range []bool{false, true} {
				if _, err := ApplyMigrate(path, preserve); err == nil || !strings.Contains(err.Error(), "unsupported config_version:") {
					t.Fatalf("write with preserve=%t returned %v", preserve, err)
				}
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("unsupported config was changed: %q, %v", got, err)
			}
		})
	}
}

func TestMigrateAtomicallyUpdatesSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "actual")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "config.toml")
	original := []byte("[ui]\nkoi=false\n")
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "config.toml")
	relative := filepath.Join("actual", "config.toml")
	if err := os.Symlink(relative, link); err != nil {
		t.Fatal(err)
	}
	oldFile, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer oldFile.Close()
	before, err := oldFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyMigrate(link, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(target)
	if err != nil || os.SameFile(before, after) || after.Mode().Perm() != 0600 {
		t.Fatalf("target was not replaced atomically with mode 0600: %v, %v", after, err)
	}
	if got, err := os.Readlink(link); err != nil || got != relative {
		t.Fatalf("link changed to %q: %v", got, err)
	}
	data, err := os.ReadFile(link)
	if err != nil || !bytes.Contains(data, []byte("config_version = 2")) || !bytes.Contains(data, []byte("koi = false")) {
		t.Fatalf("wrong target content: %q, %v", data, err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyMigrate(link, false); !os.IsNotExist(err) {
		t.Fatalf("dangling symlink returned %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != relative {
		t.Fatalf("dangling link changed to %q: %v", got, err)
	}
}
