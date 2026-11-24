package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestConfigCheckWithoutTerminalOrServer(t *testing.T) {
	teststate.Lock(t)
	p := filepath.Join(t.TempDir(), "config.toml")
	teststate.Setenv(t, config.EnvVar, p)
	for _, tc := range []struct {
		name, content, want string
		code                int
	}{
		{"missing", "", "config: ok\n", 0},
		{"valid", "onboarding=false\n", "config: ok\n", 0},
		{"unknown", "[ui]\nbogus=1\n", "config: ok\n", 0},
		{"type", "[ui]\nsidebar_width='bad'\n", "ui.sidebar_width", 1},
		{"binding", "[keys]\nnew_tab='prefix+zz'\n", "keys.new_tab", 1},
		{"kitty", "[experimental]\nkitty_graphics=true\n", "config: ok\n", 0},
		{"unsupported", "[update]\nversion_check=true\n", "update.version_check: unsupported", 1},
		{"syntax", "[ui\n", "config parse error:", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "missing" {
				if err := os.WriteFile(p, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out, stderr bytes.Buffer
			code := Run([]string{"config", "check"}, Options{Stdout: &out, Stderr: &stderr})
			if code != tc.code || !strings.Contains(out.String(), tc.want) || stderr.Len() != 0 {
				t.Fatal(code, out.String(), stderr.String())
			}
		})
	}
}

func TestConfigNotifyJSON(t *testing.T) {
	teststate.Lock(t)
	p := filepath.Join(t.TempDir(), "config.toml")
	teststate.Setenv(t, config.EnvVar, p)
	run := func(content string) (string, int) {
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		var out, stderr bytes.Buffer
		code := Run([]string{"config", "notify", "--json"}, Options{Stdout: &out, Stderr: &stderr})
		return out.String(), code
	}
	if out, code := run(""); code != 0 || out != `{"enabled":false,"reach_timeout_ms":30000,"channels":[],"warnings":[]}`+"\n" {
		t.Fatal(code, out)
	}
	good := "[notify]\nreach_timeout=\"30s\"\n[[notify.channel]]\nid=\"android\"\nsend=\"now\"\n[[notify.channel]]\nid=\"slack\"\nsend=\"unanswered\"\nafter=\"5m\"\nfallback_for=[\"android\"]\n"
	want := `{"enabled":true,"reach_timeout_ms":30000,"channels":[{"id":"android","send":"now","after_ms":0,"fallback_for":[]},{"id":"slack","send":"unanswered","after_ms":300000,"fallback_for":["android"]}],"warnings":[]}` + "\n"
	if out, code := run(good); code != 0 || out != want {
		t.Fatal(code, out)
	}
	if out, code := run("[ui\n"); code == 0 || out != "" {
		t.Fatal(code, out)
	}
	// check も notify の警告を出す。
	if err := os.WriteFile(p, []byte("[notify]\n[[notify.channel]]\nid=\"x\"\nsend=\"now\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"config", "check"}, Options{Stdout: &out, Stderr: &stderr}); code != 1 || !strings.Contains(out.String(), "notify.channel[0].id") {
		t.Fatal(code, out.String())
	}
}

func TestConfigMigrate(t *testing.T) {
	teststate.Lock(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	teststate.Setenv(t, config.EnvVar, path)
	original := []byte("onboarding=false\n[ui]\nkoi=false\n")
	for _, tc := range []struct {
		name   string
		flags  []string
		lines  []string
		mode   string
		active bool
	}{
		{"dry run", nil, []string{"+ config_version = 2", "- guard.enabled = true", "+ guard.enabled = false", "Dry run"}, "enforce", true},
		{"write", []string{"--write"}, []string{"+ config_version = 2", "- guard.mode = enforce", "+ guard.mode = record"}, "record", false},
		{"preserve", []string{"--write", "--preserve-v1"}, []string{"+ config_version = 2", "+ guard.enabled = true (preserves v1 default)"}, "enforce", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			code := Run(append([]string{"config", "migrate"}, tc.flags...), Options{Stdout: &out, Stderr: &stderr})
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit %d: %s %s", code, out.String(), stderr.String())
			}
			for _, line := range tc.lines {
				if !strings.Contains(out.String(), line) {
					t.Errorf("missing %q in %s", line, out.String())
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.flags) == 0 {
				if !bytes.Equal(data, original) {
					t.Fatal("dry run modified the file")
				}
			} else if !bytes.Contains(data, []byte("config_version = 2")) {
				t.Fatalf("missing v2 config_version in %s", data)
			}
			c, err := config.Decode(data)
			if err != nil || c.KoiEnabled() || c.GuardConfig().Active() != tc.active || c.GuardConfig().EffectiveMode() != tc.mode {
				t.Fatalf("unexpected migrated config: %+v (%v)", c, err)
			}
		})
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"config", "--help"}, Options{Stdout: &out, Stderr: &stderr}); code != 0 || !strings.Contains(out.String(), "migrate [--write] [--preserve-v1]") {
		t.Fatalf("config help exit %d: %s %s", code, out.String(), stderr.String())
	}
	out.Reset()
	if code := Run([]string{"config", "check"}, Options{Stdout: &out, Stderr: &stderr}); code != 0 || !strings.Contains(out.String(), "config_version: v2\n") {
		t.Fatalf("config check exit %d: %s %s", code, out.String(), stderr.String())
	}
}

func TestConfigMigratePreserveValidatesDryRunAndWriteEqually(t *testing.T) {
	teststate.Lock(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	teststate.Setenv(t, config.EnvVar, path)
	for _, tc := range []struct{ input, message string }{
		{"[ui]\nkoi = 'bad'", "invalid value"},
		{"ui = 'bad'", "ui is not a table"},
	} {
		if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		var dry, write, out bytes.Buffer
		dryExit := Run([]string{"config", "migrate", "--preserve-v1"}, Options{Stdout: &out, Stderr: &dry})
		writeExit := Run([]string{"config", "migrate", "--write", "--preserve-v1"}, Options{Stdout: &out, Stderr: &write})
		if dryExit != 1 || writeExit != dryExit || dry.String() != write.String() || !strings.Contains(dry.String(), tc.message) || out.Len() != 0 {
			t.Fatalf("dry run: %d %q, write: %d %q, stdout: %q", dryExit, dry.String(), writeExit, write.String(), out.String())
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != tc.input {
			t.Fatalf("migration changed input: %q, %v", got, err)
		}
	}
}
